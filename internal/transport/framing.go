package transport

import (
	"errors"
	"io"
	"sync"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"
)

// Stream classes (spec 8.1). Priorities are a local scheduler policy:
// QUIC has no wire priority (RFC 9000 §2.3). Raft/control ride urgency 0;
// bulk chunk/snapshot ride urgency 7 so they never out-schedule control
// traffic on the sender's scheduler (connection congestion still applies).
type StreamClass int

const (
	ClassRaft StreamClass = iota
	ClassControl
	ClassClient
	ClassChunk
	ClassSnapshot
)

// Urgency returns the RFC-9218-shaped urgency for a class.
func (c StreamClass) Urgency() int8 {
	switch c {
	case ClassRaft, ClassControl:
		return 0
	case ClassClient:
		return 1
	default: // Chunk, Snapshot
		return 7
	}
}

// OpenClassifiedStream opens a stream and applies the class priority.
func OpenClassifiedStream(conn *quic.Conn, class StreamClass) (*quic.Stream, error) {
	st, err := conn.OpenStream()
	if err != nil {
		return nil, err
	}
	st.SetPriority(class.Urgency(), false)
	return st, nil
}

// Framing caps (spec 8.1): length-prefixed protobuf.
const (
	MaxControlFrame  = 64 << 10 // 64 KiB
	MaxManifestFrame = 1 << 20  // 1 MiB
	maxFrameSize     = MaxManifestFrame
	errorPreview     = 512
)

var ErrFrameTooLarge = errors.New("transport: frame exceeds size cap")
var ErrZeroLength = errors.New("transport: zero-length frame")

// frameMu serializes WriteFrame calls so concurrent writers on one stream
// cannot interleave header/payload (documented single-writer is still
// preferred; the mutex makes concurrent use safe).
var frameMu sync.Mutex

// WriteFrame appends a 4-byte length prefix + protobuf payload.
// Control-plane frames are capped at MaxControlFrame (64 KiB); chunk and
// snapshot payloads must use WriteFrameForClass which allows up to
// MaxManifestFrame.
func WriteFrame(w io.Writer, msg proto.Message) error {
	return writeFrameCapped(w, msg, MaxControlFrame)
}

// WriteFrameForClass writes msg with the cap for its stream class:
// chunk/snapshot may use the 1 MiB manifest cap, all others the 64 KiB
// control cap.
func WriteFrameForClass(w io.Writer, class StreamClass, msg proto.Message) error {
	capBytes := MaxControlFrame
	if class == ClassChunk || class == ClassSnapshot {
		capBytes = MaxManifestFrame
	}
	return writeFrameCapped(w, msg, capBytes)
}

func writeFrameCapped(w io.Writer, msg proto.Message, capBytes int) error {
	frameMu.Lock()
	defer frameMu.Unlock()
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return ErrZeroLength
	}
	if len(b) > capBytes {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(b))
	buf[0], buf[1], buf[2], buf[3] = byte(len(b)>>24), byte(len(b)>>16), byte(len(b)>>8), byte(len(b))
	copy(buf[4:], b)
	_, err = w.Write(buf)
	return err
}

// ReadFrame decodes one framed message into msg (freshly zeroed by caller).
func ReadFrame(r io.Reader, msg proto.Message) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := uint32(hdr[0])<<24 | uint32(hdr[1])<<16 | uint32(hdr[2])<<8 | uint32(hdr[3])
	if n == 0 {
		return ErrZeroLength
	}
	if n > maxFrameSize {
		return ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return proto.Unmarshal(buf, msg)
}
