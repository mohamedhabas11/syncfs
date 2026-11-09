package metadata

import (
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/metadata/locks"
)

// TestLockGrantAppliesE2E proves LOCK_GRANT commands now apply through the
// state machine (BindProto wiring): a later Try on the same range conflicts.
func TestLockGrantAppliesE2E(t *testing.T) {
	m := New()
	inode := [16]byte{1}
	cmd := &syncfsapi.Command{Kind: syncfsapi.CommandKind_LOCK_GRANT, Payload: &syncfsapi.Command_LockGrant{
		LockGrant: &syncfsapi.LockGrantPayload{InodeId: inode[:], Family: 2, OwnerKey: "alice", RangeStart: 0, RangeEnd: 100, Mode: 2}}}
	if err := m.Apply([16]byte{1}, 1, cmd); err != nil {
		t.Fatalf("lock grant apply: %v", err)
	}
	// A second owner on the same range must now conflict (manager knows).
	other := locks.Lock{InodeID: inode, Family: locks.FamilyFcntl, Owner: "bob", Start: 0, End: 100, Mode: locks.Exclusive}
	if err := m.LockMgr.Try(other); err == nil {
		t.Fatalf("lock state must be shared between Apply and Try (E2E wiring)")
	}
	// ACL: release via command frees the range.
	rel := &syncfsapi.Command{Kind: syncfsapi.CommandKind_LOCK_RELEASE, Payload: &syncfsapi.Command_LockRelease{
		LockRelease: &syncfsapi.LockReleasePayload{InodeId: inode[:], OwnerKey: "alice"}}}
	if err := m.Apply([16]byte{2}, 2, rel); err != nil {
		t.Fatalf("lock release apply: %v", err)
	}
	if err := m.LockMgr.Try(other); err != nil {
		t.Fatalf("after release the range must be free: %v", err)
	}
}
