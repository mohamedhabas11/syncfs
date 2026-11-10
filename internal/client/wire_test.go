package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"google.golang.org/protobuf/proto"
)

func TestWireSyncerRequiresDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"receipts":[]}`))
	}))
	defer srv.Close()
	w := NewWireSyncer(srv.URL)
	if err := w.OnWrite("f", 0, []byte("hello")); err != nil {
		t.Fatalf("onwrite against ok server: %v", err)
	}
	// Unreachable daemon must surface, never silent.
	bad := NewWireSyncer("http://127.0.0.1:1")
	if err := bad.OnWrite("f", 0, []byte("x")); err == nil || !strings.Contains(err.Error(), "1/volume/chunks") {
		t.Fatalf("unreachable must surface daemon error, got %v", err)
	}
}

// TestWireSyncerAttachesReceipts: /volume/chunks responses must decode into
// durable receipts that are attached to the WRITE_RANGES command
// (Iter-8-07: FUSE → daemon write path).
func TestWireSyncerAttachesReceipts(t *testing.T) {
	var seenCommand bool
	var cmdBuf []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/volume/chunks" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"receipts":[{"chunk_id":"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff","peer_id":2}]}`))
			return
		}
		if r.URL.Path == "/volume/command" {
			b, _ := io.ReadAll(r.Body)
			cmdBuf = b
			seenCommand = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	w := NewWireSyncer(srv.URL)
	if err := w.OnWrite("f", 0, []byte("hello")); err != nil {
		t.Fatalf("onwrite: %v", err)
	}
	if !seenCommand {
		t.Fatalf("expected /volume/command to be called for WRITE_RANGES")
	}
	var cmd syncfsapi.Command
	if err := proto.Unmarshal(cmdBuf, &cmd); err != nil {
		t.Fatalf("decode WRITE_RANGES: %v", err)
	}
	if got := len(cmd.GetPreparedChunks()); got != 1 {
		t.Fatalf("WRITE_RANGES must carry 1 prepared chunk receipt, got %d", got)
	}
}
