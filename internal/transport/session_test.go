package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/auth"
	"github.com/quic-go/quic-go"
)

func tlsCert(t *testing.T, leaf *auth.Leaf) tls.Certificate {
	t.Helper()
	return tls.Certificate{Certificate: [][]byte{leaf.DER}, PrivateKey: leaf.Key}
}

func mustServer(t *testing.T, ca *auth.CA, addr *string, ln **quic.Listener) {
	t.Helper()
	srvLeaf, err := ca.IssueNode("srv-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ServerTLSConfig([]tls.Certificate{tlsCert(t, srvLeaf)}, ca.Pool(), ca.ID)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(*addr, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	*ln = listener
	*addr = listener.Addr().String()
}

func dialConn(t *testing.T, srvAddr string, ca *auth.CA, id string) (*quic.Conn, func()) {
	t.Helper()
	leaf, err := ca.IssueClient(id)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientTLSConfig([]tls.Certificate{tlsCert(t, leaf)}, ca.Pool(), ca.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, err := Dial(ctx, srvAddr, cfg, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return conn, cancel
}

func TestRoundTripHelloOverQUIC(t *testing.T) {
	ca, err := auth.NewCA("cl-1")
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:0"
	var ln *quic.Listener
	mustServer(t, ca, &addr, &ln)
	defer ln.Close()

	hello := &syncfsapi.Hello{
		ClusterId:       []byte("cl-1"),
		ProtocolVersion: 1,
		RequestId:       42,
		PrincipalType:   syncfsapi.PrincipalType_CLIENT,
		Role:            0,
		SanId:           "spiffe://syncfs.cluster/cl-1/client/app-1",
		Nonce:           []byte("n"),
		Alpn:            ALPN,
	}

	serverGot := make(chan *syncfsapi.Hello, 1)
	go func() {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			serverGot <- nil
			return
		}
		st, err := conn.AcceptStream(context.Background())
		if err != nil {
			serverGot <- nil
			return
		}
		var h syncfsapi.Hello
		if err := ReadFrame(st, &h); err != nil {
			serverGot <- nil
			return
		}
		serverGot <- &h
	}()

	conn, cancel := dialConn(t, addr, ca, "app-1")
	defer cancel()
	defer conn.CloseWithError(0, "done")

	st, err := OpenClassifiedStream(conn, auth.Identity{ClusterID: "cl-1", PrincipalType: "client", ID: "app-1"}, ClassClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(st, hello); err != nil {
		t.Fatal(err)
	}
	st.Close()

	got := <-serverGot
	if got == nil {
		t.Fatalf("server did not receive hello")
	}
	if got.RequestId != 42 || got.Alpn != ALPN {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if conn.ConnectionState().Used0RTT {
		t.Fatalf("0-RTT must not be used")
	}
}

func TestWrongClusterRejected(t *testing.T) {
	ca1, _ := auth.NewCA("cl-1")
	ca2, _ := auth.NewCA("cl-2")
	addr := "127.0.0.1:0"
	var ln *quic.Listener
	mustServer(t, ca1, &addr, &ln)
	defer ln.Close()

	leaf, err := ca2.IssueClient("intruder")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientTLSConfig([]tls.Certificate{tlsCert(t, leaf)}, ca2.Pool(), "cl-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = Dial(ctx, addr, cfg, nil)
	if err == nil {
		t.Fatalf("dial with foreign cluster must fail")
	}
}

func TestFrameTooLarge(t *testing.T) {
	h := &syncfsapi.Hello{Nonce: make([]byte, MaxManifestFrame+1)}
	err := WriteFrame(ioDiscard{}, h)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize frame must fail, got %v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestStreamClasses(t *testing.T) {
	for c, want := range map[StreamClass]int8{
		ClassRaft:     0,
		ClassControl:  0,
		ClassClient:   1,
		ClassChunk:    7,
		ClassSnapshot: 7,
	} {
		if c.Urgency() != want {
			t.Fatalf("class %v urgency %d != %d", c, c.Urgency(), want)
		}
	}
}
