package fusefs

import (
	"testing"
)

func TestSkeletonSmoke(t *testing.T) {
	if MountpointExists("/nonexistent-syncfs-test-path") {
		t.Fatalf("nonexistent path must not exist")
	}
	h := &Handler{}
	if h == nil {
		t.Fatalf("handler must be non-nil")
	}
	opts := MountOpts()
	if opts.AllowOther {
		t.Fatalf("strict mount must not AllowOther")
	}
	if opts.FsName != "syncfs" {
		t.Fatalf("FSName must be syncfs")
	}
}
