package locks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBlockingGrantCancelled(t *testing.T) {
	m := NewManager()
	held := Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "A", Start: 0, End: 100, Mode: Exclusive}
	if err := m.Try(held); err != nil {
		t.Fatal(err)
	}
	want := Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "B", Start: 0, End: 100, Mode: Exclusive}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.BlockingGrantCtx(ctx, want); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled wait must return ctx error, got %v", err)
	}
}

func TestDeadlockDetected(t *testing.T) {
	m := NewManager()
	// A holds X, B holds Y; A waits for Y -> cycle when B waits for X.
	x := Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "A", Start: 0, End: 100, Mode: Exclusive}
	y := Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "B", Start: 200, End: 300, Mode: Exclusive}
	if err := m.Try(x); err != nil {
		t.Fatal(err)
	}
	if err := m.Try(y); err != nil {
		t.Fatal(err)
	}
	// A now wants Y's range (queued in a goroutine).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.BlockingGrantCtx(ctx, Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "A", Start: 200, End: 300, Mode: Exclusive})
	}()
	time.Sleep(20 * time.Millisecond) // let A's waiter enqueue
	// B wants X's range -> cycle (A waits on B, B waits on A).
	if err := m.BlockingGrantCtx(context.Background(), Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "B", Start: 0, End: 100, Mode: Exclusive}); !errors.Is(err, ErrDeadlock) {
		t.Fatalf("cycle must return ErrDeadlock, got %v", err)
	}
	cancel()
	<-done
}

func TestFamilyQueueIsolation(t *testing.T) {
	m := NewManager()
	held := Lock{InodeID: [16]byte{1}, Family: FamilyFlock, Owner: "F", Start: 0, End: 100, Mode: Exclusive}
	if err := m.Try(held); err != nil {
		t.Fatal(err)
	}
	// fcntl request must not be blocked behind the flock queue/head.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.BlockingGrantCtx(ctx, Lock{InodeID: [16]byte{1}, Family: FamilyFcntl, Owner: "G", Start: 0, End: 100, Mode: Exclusive}); err != nil {
		t.Fatalf("different family must grant immediately: %v", err)
	}
}
