package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWireSyncerRequiresDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
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
