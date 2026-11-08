package wal

import (
	"errors"
	"testing"

	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

func TestSaveReadyReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	r := raft.Ready{
		Entries: []raftpb.Entry{
			{Term: 1, Index: 1, Data: []byte("create")},
			{Term: 1, Index: 2, Data: []byte("write")},
		},
		HardState: raftpb.HardState{Term: 1, Vote: 1, Commit: 1},
	}
	if err := s.SaveReady(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if li, _ := s2.LastIndex(); li != 2 {
		t.Fatalf("LastIndex=%d want 2", li)
	}
	ents, err := s2.Entries(1, 3, 1024)
	if err != nil || len(ents) != 2 || string(ents[1].Data) != "write" {
		t.Fatalf("entries: %+v %v", ents, err)
	}
	hs, _, err := s2.InitialState()
	if err != nil || hs.Term != 1 || hs.Commit != 1 {
		t.Fatalf("hardstate: %+v %v", hs, err)
	}
}

func TestCrashHooksFire(t *testing.T) {
	var kinds []string
	s, err := Open(Options{Dir: t.TempDir(), Hooks: &Hooks{
		AfterRecordPersist: func(c HookContext) { kinds = append(kinds, c.Kind) },
		AfterSync:          func(c HookContext) { kinds = append(kinds, "sync:"+c.Kind) },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := raft.Ready{Entries: []raftpb.Entry{{Term: 1, Index: 1}}, HardState: raftpb.HardState{Term: 1}}
	if err := s.SaveReady(r); err != nil {
		t.Fatal(err)
	}
	// deterministic sequence: record(entry) sync(entry) record(hardstate) sync(hardstate)
	want := []string{"entry", "sync:entry", "hardstate", "sync:hardstate"}
	if len(kinds) != len(want) {
		t.Fatalf("hooks: %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("hook[%d]=%q want %q (all %v)", i, kinds[i], want[i], kinds)
		}
	}
}

func TestTruncatedErrVisible(t *testing.T) {
	s, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// write a torn tail: valid header, truncated payload
	s.file.Write([]byte{0xaa, 0xbb, 0xcc})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	s2, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.TruncatedErr() == nil {
		t.Fatalf("expected truncated record error after reopen")
	}
	if !errors.Is(s2.TruncatedErr(), ErrTornRecord) {
		t.Fatalf("wrong truncated error: %v", s2.TruncatedErr())
	}
}

func TestIndexGapDetected(t *testing.T) {
	s, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// indexes must be contiguous starting at 1 (no snapshot)
	r := raft.Ready{Entries: []raftpb.Entry{{Term: 1, Index: 5, Data: []byte("x")}}}
	if err := s.SaveReady(r); err == nil {
		s.Close()
		t.Fatalf("gap must be rejected")
	} else if !errors.Is(err, ErrIndexGap) {
		s.Close()
		t.Fatalf("wrong error: %v", err)
	}
	s.Close()
}
