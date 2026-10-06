package receipt

import "testing"

func mustRegister(t *testing.T, s *Store, item string, epoch uint64) {
	t.Helper()
	if err := s.Register(item, epoch); err != nil {
		t.Fatalf("register %s: %v", item, err)
	}
}

func mustApply(t *testing.T, s *Store, r Receipt) {
	t.Helper()
	if err := s.Apply(r); err != nil {
		t.Fatalf("apply %+v: %v", r, err)
	}
}

func headIs(t *testing.T, s *Store, item string, epoch, revision uint64, status Status) {
	t.Helper()
	head, ok := s.Head(item)
	if !ok {
		t.Fatalf("no head for %s", item)
	}
	if head.Epoch != epoch || head.Revision != revision || head.Status != status {
		t.Fatalf("head = %+v, want epoch=%d rev=%d status=%s", head, epoch, revision, status)
	}
}

func TestConflictSameRevision(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	base := Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusRunning, Payload: "x"}
	mustApply(t, s, base)

	diffStatus := base
	diffStatus.Status = StatusSucceeded
	if err := s.Apply(diffStatus); err != ErrConflict {
		t.Fatalf("different status, same revision: got %v, want ErrConflict", err)
	}

	diffPayload := base
	diffPayload.Payload = "y"
	if err := s.Apply(diffPayload); err != ErrConflict {
		t.Fatalf("different payload, same revision: got %v, want ErrConflict", err)
	}

	if err := s.Apply(base); err != nil {
		t.Fatalf("identical duplicate must be a no-op, got %v", err)
	}
	if n := len(s.Evidence("a")); n != 1 {
		t.Fatalf("evidence = %d, want 1", n)
	}
	headIs(t, s, "a", 1, 1, StatusRunning)
}

func TestLowerRevisionArchivedOnly(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 3, Status: StatusRunning})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusAccepted})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusRunning})
	headIs(t, s, "a", 1, 3, StatusRunning)
	if n := len(s.Evidence("a")); n != 3 {
		t.Fatalf("evidence = %d, want 3 (low revisions archived, not dropped)", n)
	}
}

func TestTerminalNeverReturnsToRunning(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 4, Status: StatusSucceeded})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 5, Status: StatusRunning})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 6, Status: StatusAccepted})
	headIs(t, s, "a", 1, 4, StatusSucceeded)
	if n := len(s.Evidence("a")); n != 3 {
		t.Fatalf("evidence = %d, want 3", n)
	}
}

func TestLateTerminalSupersedesNonTerminalHead(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 5, Status: StatusRunning})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusFailed})
	headIs(t, s, "a", 1, 2, StatusFailed)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 7, Status: StatusRunning})
	headIs(t, s, "a", 1, 2, StatusFailed)
}

func TestLaterTerminalReplacesEarlierTerminal(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 4, Status: StatusSucceeded})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 6, Status: StatusFailed})
	headIs(t, s, "a", 1, 6, StatusFailed)
}

func TestNewEpochBlockedUntilDecisionCommits(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusAccepted})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 2, Revision: 1, Status: StatusAccepted})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 2, Revision: 2, Status: StatusRunning})

	headIs(t, s, "a", 1, 1, StatusAccepted)
	if epoch, _ := s.CurrentEpoch("a"); epoch != 1 {
		t.Fatalf("current epoch = %d, want 1 before decision", epoch)
	}
	if n := len(s.Buffered("a")); n != 2 {
		t.Fatalf("buffered = %d, want 2", n)
	}

	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 2}); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	headIs(t, s, "a", 2, 2, StatusRunning)
	if n := len(s.Buffered("a")); n != 0 {
		t.Fatalf("buffered = %d after decision, want 0", n)
	}
}

func TestLateSuccessFromSupersededEpochRaisesCompensation(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusRunning})
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 2}); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 2, Revision: 1, Status: StatusAccepted})
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 9, Status: StatusSucceeded})

	headIs(t, s, "a", 2, 1, StatusAccepted)
	comps := s.PendingCompensations("a")
	if len(comps) != 1 || comps[0].Epoch != 1 || comps[0].Status != StatusSucceeded {
		t.Fatalf("compensations = %+v, want one for epoch 1 succeeded", comps)
	}
	if n := len(s.Evidence("a")); n != 3 {
		t.Fatalf("evidence = %d, want 3 (late success kept as evidence)", n)
	}
}

func TestSuccessBeforeSupersedeAlsoRaisesCompensation(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	mustApply(t, s, Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusSucceeded})
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 2}); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	comps := s.PendingCompensations("a")
	if len(comps) != 1 || comps[0].Epoch != 1 {
		t.Fatalf("compensations = %+v, want one for epoch 1", comps)
	}
}

func TestStaleDecisionRejected(t *testing.T) {
	s := New()
	mustRegister(t, s, "a", 1)
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 2, To: 3}); err != ErrStaleDecision {
		t.Fatalf("from != current: got %v, want ErrStaleDecision", err)
	}
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 1}); err != ErrStaleDecision {
		t.Fatalf("to <= current: got %v, want ErrStaleDecision", err)
	}
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 2}); err != nil {
		t.Fatalf("valid supersede: %v", err)
	}
	if err := s.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 3}); err != ErrStaleDecision {
		t.Fatalf("replayed old decision: got %v, want ErrStaleDecision", err)
	}
	if epoch, _ := s.CurrentEpoch("a"); epoch != 2 {
		t.Fatalf("current epoch = %d, want 2", epoch)
	}
}

func TestInvalidStatusAndUnknownItem(t *testing.T) {
	s := New()
	if err := s.Apply(Receipt{ItemID: "ghost", Epoch: 1, Revision: 1, Status: StatusRunning}); err != ErrUnknownItem {
		t.Fatalf("got %v, want ErrUnknownItem", err)
	}
	mustRegister(t, s, "a", 1)
	if err := s.Apply(Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: "bogus"}); err != ErrInvalidStatus {
		t.Fatalf("got %v, want ErrInvalidStatus", err)
	}
}
