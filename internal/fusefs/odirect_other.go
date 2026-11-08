//go:build !linux

package fusefs

// oDirectFlag is a no-op off Linux (dev machines run the unit tests there;
// the O_DIRECT rejection is a Linux kernel-boundary rule).
const oDirectFlag uint32 = 0
