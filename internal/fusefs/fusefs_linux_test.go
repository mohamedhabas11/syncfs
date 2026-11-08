package fusefs

import (
	"os"
	"testing"
)

func TestFUSEmountSmoke(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("no /dev/fuse in this environment")
	}
	target := t.TempDir() + "-mount"
	_ = os.Mkdir(target, 0755)
	server, err := KernelMount(target, NewHandler("smoke"))
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	if !MountpointExists(target) {
		server.Unmount()
		t.Fatalf("mountpoint not responsive after mount")
	}
	server.Unmount()
}
