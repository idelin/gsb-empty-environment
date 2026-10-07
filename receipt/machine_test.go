package receipt

import (
	"errors"
	"fmt"
	"sort"
	"testing"
)

// permutations returns all permutations of [0, n).
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			cp := make([]int, n)
			copy(cp, perm)
			out = append(out, cp)
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
	return out
}

func evidenceKey(rs []Receipt) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = fmt.Sprintf("%s/%d/%s/%s", r.Generation, r.Revision, r.Status, r.Evidence)
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

func TestRevisionRules(t *testing.T) {
	m := NewMachine()
	if err := m.Bootstrap("g1"); err != nil {
		t.Fatal(err)
	}
	submit := func(rev int64, st Status) error {
		return m.applyReceipt(Receipt{Generation: "g1", Revision: rev, Status: st, Evidence: fmt.Sprintf("e%d", rev)})
	}
	if err := submit(1, StatusAccepted); err != nil {
		t.Fatal(err)
	}
	if err := submit(2, StatusRunning); err != nil {
		t.Fatal(err)
	}
	// Lower revision: archived only, state unchanged.
	if err := submit(1, StatusAccepted); err != nil {
		t.Fatal(err)
	}
	v, _ := m.View("g1")
	if v.Revision != 2 || v.Status != StatusRunning {
		t.Fatalf("duplicate/lower revision changed state: %+v", v)
	}
	// Same revision, contradictory status: conflict.
	err := submit(2, StatusSucceeded)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	v, _ = m.View("g1")
	if v.Status != StatusRunning {
		t.Fatalf("conflict mutated state: %+v", v)
	}
	// Higher revision advances, then terminal is absorbing.
	if err := submit(3, StatusSucceeded); err != nil {
		t.Fatal(err)
	}
	err = submit(4, StatusRunning)
	var reg *RegressionError
	if !errors.As(err, &reg) {
		t.Fatalf("expected RegressionError, got %v", err)
	}
	v, _ = m.View("g1")
	if v.Status != StatusSucceeded || v.Revision != 3 {
		t.Fatalf("terminal state regressed: %+v", v)
	}
	// The regressed receipt is archived: no evidence lost.
	if got := len(m.Evidence("g1")); got != 4 {
		t.Fatalf("evidence lost: got %d receipts, want 4", got)
	}
	if len(v.Archived) != 1 || v.Archived[0].Revision != 4 {
		t.Fatalf("regression not archived: %+v", v.Archived)
	}
}

// TestPermutationsConverge feeds valid receipt streams in every possible
// delivery order and requires identical final state and complete evidence.
func TestPermutationsConverge(t *testing.T) {
	streams := map[string][]Receipt{
		"success-path": {
			{Generation: "g", Revision: 1, Status: StatusAccepted, Evidence: "e1"},
			{Generation: "g", Revision: 2, Status: StatusRunning, Evidence: "e2"},
			{Generation: "g", Revision: 3, Status: StatusRunning, Evidence: "e3"},
			{Generation: "g", Revision: 4, Status: StatusSucceeded, Evidence: "e4"},
			{Generation: "g", Revision: 5, Status: StatusSucceeded, Evidence: "e5"},
		},
		"failure-path": {
			{Generation: "g", Revision: 1, Status: StatusAccepted, Evidence: "e1"},
			{Generation: "g", Revision: 2, Status: StatusRunning, Evidence: "e2"},
			{Generation: "g", Revision: 3, Status: StatusFailed, Evidence: "e3"},
		},
		"with-duplicates": {
			{Generation: "g", Revision: 1, Status: StatusAccepted, Evidence: "e1"},
			{Generation: "g", Revision: 2, Status: StatusRunning, Evidence: "e2"},
			{Generation: "g", Revision: 2, Status: StatusRunning, Evidence: "e2"},
			{Generation: "g", Revision: 3, Status: StatusSucceeded, Evidence: "e3"},
		},
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			// Exact duplicates are idempotent: evidence is the unique set.
			uniq := map[string]Receipt{}
			for _, r := range stream {
				uniq[evidenceKey([]Receipt{r})] = r
			}
			var want []Receipt
			for _, r := range uniq {
				want = append(want, r)
			}
			wantEvidence := evidenceKey(want)
			var wantState *GenerationView
			for i, perm := range permutations(len(stream)) {
				m := NewMachine()
				if err := m.Bootstrap("g"); err != nil {
					t.Fatal(err)
				}
				for _, idx := range perm {
					if err := m.applyReceipt(stream[idx]); err != nil {
						t.Fatalf("perm %d: unexpected error: %v", i, err)
					}
				}
				v, _ := m.View("g")
				if wantState == nil {
					wantState = &v
				} else if v.Status != wantState.Status || v.Revision != wantState.Revision {
					t.Fatalf("perm %d diverged: got %v@%d want %v@%d",
						i, v.Status, v.Revision, wantState.Status, wantState.Revision)
				}
				if got := evidenceKey(m.Evidence("g")); got != wantEvidence {
					t.Fatalf("perm %d lost evidence:\ngot  %s\nwant %s", i, got, wantEvidence)
				}
			}
		})
	}
}

// TestSupersedePermutations shuffles receipts and the resolution in every
// order and checks: unique current generation, successor advances only after
// the resolution commits, late success of the old generation never
// overwrites the new one and is recorded as pending compensation, and no
// evidence is lost.
func TestSupersedePermutations(t *testing.T) {
	type step struct {
		kind      string // "receipt" or "supersede"
		receipt   Receipt
		supersede SupersedeRecord
	}
	steps := []step{
		{kind: "receipt", receipt: Receipt{Generation: "A", Revision: 1, Status: StatusAccepted, Evidence: "a1"}},
		{kind: "receipt", receipt: Receipt{Generation: "A", Revision: 2, Status: StatusRunning, Evidence: "a2"}},
		{kind: "receipt", receipt: Receipt{Generation: "B", Revision: 1, Status: StatusAccepted, Evidence: "b1"}},
		{kind: "receipt", receipt: Receipt{Generation: "B", Revision: 2, Status: StatusSucceeded, Evidence: "b2"}},
		{kind: "supersede", supersede: SupersedeRecord{Old: "A", New: "B"}},
		{kind: "receipt", receipt: Receipt{Generation: "A", Revision: 3, Status: StatusSucceeded, Evidence: "a3"}},
	}
	supersedeIdx := 4
	for i, perm := range permutations(len(steps)) {
		m := NewMachine()
		if err := m.Bootstrap("A"); err != nil {
			t.Fatal(err)
		}
		superseded := false
		for _, idx := range perm {
			st := steps[idx]
			if st.kind == "supersede" {
				if err := m.applySupersede(st.supersede); err != nil {
					t.Fatalf("perm %d: supersede: %v", i, err)
				}
				superseded = true
				continue
			}
			err := m.applyReceipt(st.receipt)
			if st.receipt.Generation == "B" && !superseded {
				// B cannot advance business before the resolution commits.
				var unk *UnknownGenerationError
				if !errors.As(err, &unk) {
					t.Fatalf("perm %d: pre-resolution B receipt: got %v", i, err)
				}
				if m.Current() != "A" {
					t.Fatalf("perm %d: current moved before resolution", i)
				}
				if v, _ := m.View("B"); v.HasStatus {
					t.Fatalf("perm %d: B advanced business before resolution", i)
				}
				continue
			}
			if err != nil {
				t.Fatalf("perm %d: receipt %+v: %v", i, st.receipt, err)
			}
		}

		// Unique current generation, chain recorded.
		if m.Current() != "B" {
			t.Fatalf("perm %d: current = %q, want B", i, m.Current())
		}
		if v, _ := m.View("A"); v.SupersededBy != "B" {
			t.Fatalf("perm %d: A not marked superseded by B", i)
		}
		// Successor reached its own terminal state regardless of order.
		if v, _ := m.View("B"); v.Status != StatusSucceeded || v.Revision != 2 {
			t.Fatalf("perm %d: B state = %v@%d", i, v.Status, v.Revision)
		}
		// Late success of A never overwrote B; it is either the legit final
		// state of A (arrived before the resolution) or a pending
		// compensation (arrived after).
		va, _ := m.View("A")
		if superseded {
			// resolution committed before a3 arrived only if a3 is after it
		}
		a3late := false
		for pos, idx := range perm {
			if idx == len(steps)-1 {
				a3late = pos > indexOf(perm, supersedeIdx)
			}
		}
		if a3late {
			if len(va.Compensations) != 1 || va.Compensations[0].Status != StatusSucceeded {
				t.Fatalf("perm %d: late success not recorded as compensation: %+v", i, va.Compensations)
			}
			if va.Status == StatusSucceeded && va.Revision == 3 {
				t.Fatalf("perm %d: late success overwrote old generation head", i)
			}
		} else if len(va.Compensations) != 0 {
			t.Fatalf("perm %d: unexpected compensation: %+v", i, va.Compensations)
		}
		// No evidence lost on either generation.
		var allA, allB []Receipt
		for _, st := range steps {
			if st.kind != "receipt" {
				continue
			}
			switch st.receipt.Generation {
			case "A":
				allA = append(allA, st.receipt)
			case "B":
				allB = append(allB, st.receipt)
			}
		}
		if got := evidenceKey(m.Evidence("A")); got != evidenceKey(allA) {
			t.Fatalf("perm %d: A evidence lost", i)
		}
		if got := evidenceKey(m.Evidence("B")); got != evidenceKey(allB) {
			t.Fatalf("perm %d: B evidence lost", i)
		}
	}
}

func indexOf(perm []int, v int) int {
	for i, x := range perm {
		if x == v {
			return i
		}
	}
	return -1
}

// TestSupersedeChain keeps a unique current generation across a chain.
func TestSupersedeChain(t *testing.T) {
	m := NewMachine()
	m.Bootstrap("A")
	if err := m.applySupersede(SupersedeRecord{Old: "A", New: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := m.applySupersede(SupersedeRecord{Old: "B", New: "C"}); err != nil {
		t.Fatal(err)
	}
	if m.Current() != "C" {
		t.Fatalf("current = %q, want C", m.Current())
	}
	// A stale resolution cannot resurrect a retired generation.
	if err := m.applySupersede(SupersedeRecord{Old: "A", New: "D"}); err == nil {
		t.Fatal("superseding a non-current generation must fail")
	}
	if m.Current() != "C" {
		t.Fatalf("current changed by stale resolution")
	}
}
