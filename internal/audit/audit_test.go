package audit

import "testing"

func TestChainVerifyAndPersist(t *testing.T) {
	s := NewSink()
	s.Record("admin", "member.add", "node-4", "")
	s.Record("admin", "member.promote", "node-4", "")
	if got := s.Records(); len(got) != 2 || got[1].PrevHash == "" {
		t.Fatalf("chain broken: %+v", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	path := t.TempDir() + "/audit.jsonl"
	if err := s.WriteJSONL(path); err != nil {
		t.Fatal(err)
	}
	if got := s.Record("sys", "recover", "node-1", ""); got.Seq != 2 {
		t.Fatalf("seq %d want 2", got.Seq)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("verify after persist: %v", err)
	}
}
