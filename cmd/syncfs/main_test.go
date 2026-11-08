package main

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	u, err := RenderUnit("daemon", "/etc/syncfs/node.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "ExecStart=/usr/bin/syncfs daemon --config /etc/syncfs/node.yaml") {
		t.Fatalf("unit must embed config: %q", u)
	}
	if !strings.Contains(u, "Restart=on-failure") {
		t.Fatalf("unit must restart on failure")
	}
}
