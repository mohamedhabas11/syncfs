//go:build linux

package fusefs

import "golang.org/x/sys/unix"

// oDirectFlag is the Linux O_DIRECT flag (strict-mode exclusion only
// applies on the production kernel boundary).
const oDirectFlag = unix.O_DIRECT
