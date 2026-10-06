package receipt

import "testing"

func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := make([]int, 0, len(p)+1)
			q = append(q, p[:i]...)
			q = append(q, n-1)
			q = append(q, p[i:]...)
			out = append(out, q)
		}
	}
	return out
}

func TestPermutationsSingleEpoch(t *testing.T) {
	receipts := []Receipt{
		{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusAccepted, Payload: "p1"},
		{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusRunning, Payload: "p2"},
		{ItemID: "a", Epoch: 1, Revision: 3, Status: StatusRunning, Payload: "p3"},
		{ItemID: "a", Epoch: 1, Revision: 4, Status: StatusSucceeded, Payload: "p4"},
		{ItemID: "a", Epoch: 1, Revision: 5, Status: StatusRunning, Payload: "p5"},
		{ItemID: "a", Epoch: 1, Revision: 6, Status: StatusFailed, Payload: "p6"},
	}
	for _, perm := range permutations(len(receipts)) {
		s := New()
		mustRegister(t, s, "a", 1)
		for _, i := range perm {
			if err := s.Apply(receipts[i]); err != nil {
				t.Fatalf("perm %v: apply %+v: %v", perm, receipts[i], err)
			}
		}
		headIs(t, s, "a", 1, 6, StatusFailed)
		if n := len(s.Evidence("a")); n != len(receipts) {
			t.Fatalf("perm %v: evidence = %d, want %d (no receipt may be lost)", perm, n, len(receipts))
		}
	}
}

func TestPermutationsNoTerminal(t *testing.T) {
	receipts := []Receipt{
		{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusAccepted},
		{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusRunning},
		{ItemID: "a", Epoch: 1, Revision: 3, Status: StatusRunning},
		{ItemID: "a", Epoch: 1, Revision: 4, Status: StatusAccepted},
		{ItemID: "a", Epoch: 1, Revision: 5, Status: StatusRunning},
	}
	for _, perm := range permutations(len(receipts)) {
		s := New()
		mustRegister(t, s, "a", 1)
		for _, i := range perm {
			mustApply(t, s, receipts[i])
		}
		headIs(t, s, "a", 1, 5, StatusRunning)
		if n := len(s.Evidence("a")); n != len(receipts) {
			t.Fatalf("perm %v: evidence = %d, want %d", perm, n, len(receipts))
		}
	}
}

type testEvent struct {
	receipt  *Receipt
	decision *SupersedeDecision
}

func TestPermutationsWithSupersede(t *testing.T) {
	events := []testEvent{
		{receipt: &Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusAccepted}},
		{receipt: &Receipt{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusRunning}},
		{receipt: &Receipt{ItemID: "a", Epoch: 1, Revision: 3, Status: StatusSucceeded, Payload: "late-or-early"}},
		{decision: &SupersedeDecision{ItemID: "a", From: 1, To: 2, Reason: "retry"}},
		{receipt: &Receipt{ItemID: "a", Epoch: 2, Revision: 1, Status: StatusAccepted}},
		{receipt: &Receipt{ItemID: "a", Epoch: 2, Revision: 2, Status: StatusRunning}},
		{receipt: &Receipt{ItemID: "a", Epoch: 2, Revision: 3, Status: StatusSucceeded, Payload: "final"}},
	}
	perms := permutations(len(events))
	if len(perms) != 5040 {
		t.Fatalf("got %d permutations, want 5040", len(perms))
	}
	for _, perm := range perms {
		s := New()
		mustRegister(t, s, "a", 1)
		for _, i := range perm {
			e := events[i]
			var err error
			if e.decision != nil {
				err = s.Supersede(*e.decision)
			} else {
				err = s.Apply(*e.receipt)
			}
			if err != nil {
				t.Fatalf("perm %v: event %+v: %v", perm, e, err)
			}
		}

		if epoch, _ := s.CurrentEpoch("a"); epoch != 2 {
			t.Fatalf("perm %v: current epoch = %d, want unique current epoch 2", perm, epoch)
		}
		headIs(t, s, "a", 2, 3, StatusSucceeded)
		if n := len(s.Evidence("a")); n != 6 {
			t.Fatalf("perm %v: evidence = %d, want 6", perm, n)
		}
		comps := s.PendingCompensations("a")
		if len(comps) != 1 || comps[0] != (Compensation{ItemID: "a", Epoch: 1, Status: StatusSucceeded}) {
			t.Fatalf("perm %v: compensations = %+v, want exactly one for epoch 1", perm, comps)
		}
		if n := len(s.Buffered("a")); n != 0 {
			t.Fatalf("perm %v: buffered = %d, want 0", perm, n)
		}
		old := s.items["a"].Epochs[1]
		if !old.HasHead || old.Head.Status != StatusSucceeded || old.Head.Revision != 3 {
			t.Fatalf("perm %v: old epoch head = %+v, want succeeded@3 recorded as history", perm, old.Head)
		}
	}
}
