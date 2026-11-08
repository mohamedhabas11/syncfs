package transport

import (
	"errors"
	"io"

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

// WriteFrame appends a 4-byte length prefix + protobuf payload.
func WriteFrame(w io.Writer, msg proto.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if len(b) > maxFrameSize {
		return ErrFrameTooLarge
	}
	var hdr [4]byte
	hdr[0], hdr[1], hdr[2], hdr[3] = byte(len(b)>>24), byte(len(b)>>16), byte(len(b)>>8), byte(len(b))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadFrame decodes one framed message into msg (freshly zeroed by caller).
func ReadFrame(r io.Reader, msg proto.Message) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := uint32(hdr[0])<<24 | uint32(hdr[1])<<16 | uint32(hdr[2])<<8 | uint32(hdr[3])
	if n > maxFrameSize {
		return ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return proto.Unmarshal(buf, msg)
}
