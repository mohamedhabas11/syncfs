package locks

import (
	"errors"
	"testing"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
)

var ino = [16]byte{5}

func TestTryAndConflict(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessA", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(a); err != nil {
		t.Fatal(err)
	}
	b := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessB", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(b); !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("conflict must be WouldBlock, got %v", err)
	}
	// shared-only ranges don't clash
	s := Lock{InodeID: ino, Family: FamilyFcntl, Owner: "sessB", Mode: Shared, Start: 200, End: 300}
	if err := m.Try(s); err != nil {
		t.Fatalf("non-conflicting shared must pass: %v", err)
	}
	// release then lock
	m.Release(a)
	if err := m.Try(b); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestSessionExpiryDropsLocks(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessA", Mode: Exclusive}
	b := Lock{InodeID: ino, Family: FamilyOFD, Owner: "sessB", Mode: Exclusive, Start: 500, End: 600}
	_ = m.Try(a)
	_ = m.Try(b)
	m.ApplyReleases("sessA")
	c := Lock{InodeID: ino, Family: FamilyFlock, Owner: "sessC", Mode: Exclusive}
	if err := m.Try(c); err != nil {
		t.Fatalf("sessC after sessA expiry: %v", err)
	}
}

func TestFamilySeparationIsTotal(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "A", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(a); err != nil {
		t.Fatal(err)
	}
	b := Lock{InodeID: ino, Family: FamilyFcntl, Owner: "B", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(b); err != nil {
		t.Fatalf("fcntl must not conflict with flock: %v", err)
	}
	c := Lock{InodeID: ino, Family: FamilyOFD, Owner: "C", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(c); err != nil {
		t.Fatalf("OFD must not conflict with flock/fcntl: %v", err)
	}
}

func TestSameOwnerNeverSelfBlocks(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "A", Mode: Shared, Start: 0, End: 100}
	if err := m.Try(a); err != nil {
		t.Fatal(err)
	}
	// same-owner exclusive upgrade over overlapping range merges, no WouldBlock.
	upgrade := Lock{InodeID: ino, Family: FamilyFlock, Owner: "A", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(upgrade); err != nil {
		t.Fatalf("same-owner upgrade must not self-block: %v", err)
	}
	// idempotent re-try is success
	if err := m.Try(upgrade); err != nil {
		t.Fatalf("idempotent re-try: %v", err)
	}
}

func TestBlockingGrantAcquiresAfterRelease(t *testing.T) {
	m := NewManager()
	a := Lock{InodeID: ino, Family: FamilyFlock, Owner: "A", Mode: Exclusive, Start: 0, End: 100}
	if err := m.Try(a); err != nil {
		t.Fatal(err)
	}
	b := Lock{InodeID: ino, Family: FamilyFlock, Owner: "B", Mode: Exclusive, Start: 0, End: 100}
	done := make(chan error, 1)
	go func() { done <- m.BlockingGrant(b) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("B must wait while A holds the lock")
	default:
	}
	m.Release(a)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("B must acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BlockingGrant did not acquire after Release (starvation/panic)")
	}
}

func TestBindProtoCarriesInodeAndRange(t *testing.T) {
	m := NewManager()
	inoA := [16]byte{7}
	inoB := [16]byte{9}
	mk := func(ino [16]byte) *syncfsapi.Command {
		return &syncfsapi.Command{
			Kind: syncfsapi.CommandKind_LOCK_GRANT,
			Payload: &syncfsapi.Command_LockGrant{LockGrant: &syncfsapi.LockGrantPayload{
				InodeId: ino[:], Family: uint32(FamilyFlock), OwnerKey: "A", RangeStart: 0, RangeEnd: 100, Mode: uint32(Exclusive),
			}},
		}
	}
	if err := m.BindProto(mk(inoA), 1); err != nil {
		t.Fatal(err)
	}
	// Same owner/range on a different inode must not conflict.
	if err := m.BindProto(mk(inoB), 2); err != nil {
		t.Fatalf("different inode must not conflict (BindProto dropped inode): %v", err)
	}
	// Release path drops the owner's locks on that inode.
	rel := &syncfsapi.Command{
		Kind: syncfsapi.CommandKind_LOCK_RELEASE,
		Payload: &syncfsapi.Command_LockRelease{LockRelease: &syncfsapi.LockReleasePayload{
			InodeId: inoA[:], OwnerKey: "A",
		}},
	}
	if err := m.BindProto(rel, 3); err != nil {
		t.Fatal(err)
	}
	// After release, another owner can take inoA.
	mkB := &syncfsapi.Command{
		Kind: syncfsapi.CommandKind_LOCK_GRANT,
		Payload: &syncfsapi.Command_LockGrant{LockGrant: &syncfsapi.LockGrantPayload{
			InodeId: inoA[:], Family: uint32(FamilyFlock), OwnerKey: "B", RangeStart: 0, RangeEnd: 100, Mode: uint32(Exclusive),
		}},
	}
	if err := m.BindProto(mkB, 4); err != nil {
		t.Fatalf("after release B must acquire: %v", err)
	}
}
