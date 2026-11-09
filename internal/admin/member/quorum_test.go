package member

import "testing"

func TestQuorumExcludesLearners(t *testing.T) {
	c := NewCluster([]uint64{1, 2, 3})
	if c.Quorum() != 3 {
		t.Fatalf("3 voters want 3, got %d", c.Quorum())
	}
	if err := c.BeginAdd(4); err != nil {
		t.Fatal(err)
	}
	if c.Quorum() != 3 {
		t.Fatalf("learner must not count, got %d", c.Quorum())
	}
	c.SetCommit(10)
	_ = c.ObserveCatchup(4, 10)
	if _, err := c.RequestPromotion(4); err != nil {
		t.Fatal(err)
	}
	if c.Quorum() != 3 {
		t.Fatalf("proposed-but-unconfirmed must not count, got %d", c.Quorum())
	}
	if err := c.ConfirmPromotion(4); err != nil {
		t.Fatal(err)
	}
	if c.Quorum() != 4 {
		t.Fatalf("confirmed voter must count, got %d", c.Quorum())
	}
}
