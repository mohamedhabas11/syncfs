package main

import (
	"os"
	"strings"
	"testing"
)

func TestAdminCLIParsing(t *testing.T) {
	// Missing required flags fail with usage exit 2 (not silent success).
	if code := runAdmin([]string{"bootstrap"}); code != 2 {
		t.Fatalf("bootstrap without flags must exit 2, got %d", code)
	}
	if code := runAdmin([]string{"status"}); code != 2 {
		t.Fatalf("status without endpoint must exit 2, got %d", code)
	}
	if code := runAdmin([]string{"volume"}); code != 2 {
		t.Fatalf("volume without --volume must exit 2, got %d", code)
	}
	if code := runAdmin([]string{"member"}); code != 2 {
		t.Fatalf("member without --node must exit 2, got %d", code)
	}
	out := t.TempDir() + "/fleet"
	if code := runAdmin([]string{"bootstrap", "--cluster-id", "c1", "--out", out}); code != 0 {
		t.Fatalf("bootstrap with flags must exit 0, got %d", code)
	}
	if _, err := os.Stat(out + "/ca.pem"); err != nil {
		t.Fatalf("bootstrap must write fleet CA: %v", err)
	}
	// Status probes the endpoint: unreachable is exit 1 (not a stub 0).
	if code := runAdmin([]string{"status", "--endpoint", "127.0.0.1:1"}); code != 1 {
		t.Fatalf("status unreachable must exit 1, got %d", code)
	}
	if code := runAdmin([]string{"volume", "--volume", "default", "--op", "status"}); code != 0 {
		t.Fatalf("volume with flags must exit 0, got %d", code)
	}
	if code := runAdmin([]string{"member", "--node", "n2", "--op", "status"}); code != 0 {
		t.Fatalf("member with flags must exit 0, got %d", code)
	}
	// daemon requires an existing config file
	if code := runDaemon([]string{"--config", "/nonexistent-syncfs-test.yaml"}); code != 1 {
		t.Fatalf("daemon with missing config must exit 1, got %d", code)
	}
	// quorum < 2 is rejected (no silent default to 1 for a 3-voter profile).
	badCfg := t.TempDir() + "/daemon-quorum1.json"
	if err := os.WriteFile(badCfg, []byte(`{"listen":"127.0.0.1:0","quorum":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := runDaemon([]string{"--config", badCfg}); code != 1 {
		t.Fatalf("daemon quorum 1 must be rejected with 1, got %d", code)
	}
	// mount requires an existing directory target
	if code := runMount([]string{"--volume", "default", "--target", "/nonexistent-syncfs-mnt"}); code != 1 {
		t.Fatalf("mount with missing target must exit 1, got %d", code)
	}
	// Success path mounts (Linux) or fails closed without /dev/fuse (dev host).
	// Now requires --daemon for durability.
	if _, err := os.Stat("/dev/fuse"); err != nil {
		if code := runMount([]string{"--volume", "default", "--target", t.TempDir(), "--daemon", "http://127.0.0.1:18081"}); code != 1 {
			t.Fatalf("mount without /dev/fuse must fail closed (1), got %d", code)
		}
	}
	// unknown subcommand is a usage error
	if code := run([]string{"bogus"}); code != 2 {
		t.Fatalf("unknown subcommand must exit 2, got %d", code)
	}
}

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
