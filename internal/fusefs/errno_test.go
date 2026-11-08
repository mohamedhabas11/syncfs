package fusefs

import (
	"strings"
	"syscall"
	"testing"
)

func TestErrnoTable(t *testing.T) {
	cases := map[string]syscall.Errno{
		"ENOENT":     syscall.ENOENT,
		"EACCES":     syscall.EACCES,
		"ENOSPC":     syscall.ENOSPC,
		"EIO":        syscall.EIO,
		"ESTALE":     syscall.ESTALE,
		"EOPNOTSUPP": syscall.EOPNOTSUPP,
	}
	for sym, want := range cases {
		if got := ErrnoFor(sym); got != want {
			t.Errorf("ErrnoFor(%q)=%d want %d", sym, got, want)
		}
	}
	if got := ErrnoFor("WHATEVER"); got != syscall.EIO {
		t.Errorf("unknown must map to EIO, got %d", got)
	}
	// lower-case works (normalized)
	if got := ErrnoFor("enospc"); got != syscall.ENOSPC {
		t.Errorf("lowercase enospc must map, got %d", got)
	}
}

func TestErrnoStrings(t *testing.T) {
	s := ErrnoString(syscall.ENOSPC)
	if !strings.Contains(strings.ToLower(s), "no space") {
		t.Fatalf("errnostring must mention the errno: %q", s)
	}
}
