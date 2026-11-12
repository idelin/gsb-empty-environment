package netsec

import (
	"math/rand"
	"testing"
)

// bruteForceLinearizable checks every permutation that respects the
// intervals, used as an independent oracle for small histories.
func bruteForceLinearizable(h History) bool {
	limit := h.WrongLimit
	if limit <= 0 {
		limit = DefaultWrongLimit
	}
	perm := make([]int, len(h.Operations))
	for i := range perm {
		perm[i] = i
	}
	var dfs func(int) bool
	dfs = func(pos int) bool {
		if pos == len(perm) {
			return true
		}
		minReturn := int64(maxInt64Value)
		for k := pos; k < len(perm); k++ {
			if r := h.Operations[perm[k]].Return; r < minReturn {
				minReturn = r
			}
		}
		for j := pos; j < len(perm); j++ {
			perm[pos], perm[j] = perm[j], perm[pos]
			op := h.Operations[perm[pos]]
			ready := op.Invoke <= minReturn
			if ready {
				spec := NewSpec(limit)
				ok := true
				for _, idx := range perm[:pos+1] {
					if !spec.Apply(h.Operations[idx]) {
						ok = false
						break
					}
				}
				if ok && dfs(pos+1) {
					return true
				}
			}
			perm[pos], perm[j] = perm[j], perm[pos]
		}
		return false
	}
	return dfs(0)
}

func randomHistory(rng *rand.Rand, n int) History {
	ops := make([]Operation, 0, n)
	current := ""
	known := map[string]bool{}
	nextID := 1
	nextChallenge := 0

	newChallenge := func() string {
		nextChallenge++
		id := "c" + string(rune('a'+nextChallenge-1))
		known[id] = true
		return id
	}

	for len(ops) < n {
		invoke := rng.Int63n(int64(2 * n))
		ret := invoke + rng.Int63n(4)
		op := Operation{ID: nextID, Invoke: invoke, Return: ret}
		nextID++

		choice := rng.Intn(5)
		switch {
		case choice == 0 || current == "":
			id := newChallenge()
			op.Kind = Create
			op.ChallengeID = id
			op.Answer = "ok"
			op.Result = OK
			current = id
		case choice == 1 && current != "":
			id := newChallenge()
			op.Kind = Replace
			op.ChallengeID = current
			op.ReplacementID = id
			op.Answer = "ok"
			op.Result = OK
			current = id
		case choice == 2 && len(known) > 0:
			op.Kind = Answer
			var ids []string
			for id := range known {
				ids = append(ids, id)
			}
			op.ChallengeID = ids[rng.Intn(len(ids))]
			if rng.Intn(2) == 0 {
				op.Answer = "ok"
				op.Result = OK
			} else {
				op.Answer = "bad"
				op.Result = ErrWrongAnswer
			}
		case choice == 3 && len(known) > 0:
			op.Kind = Consume
			var ids []string
			for id := range known {
				ids = append(ids, id)
			}
			op.ChallengeID = ids[rng.Intn(len(ids))]
			op.Result = OK
		case len(known) > 0:
			op.Kind = Grant
			var ids []string
			for id := range known {
				ids = append(ids, id)
			}
			op.ChallengeID = ids[rng.Intn(len(ids))]
			op.Result = OK
		default:
			continue
		}

		if rng.Intn(3) == 0 {
			mutated, ok := alternativeResult(op.Result)
			if ok {
				op.Result = mutated
			}
		}
		ops = append(ops, op)
	}
	return History{Operations: ops, WrongLimit: 2}
}

func TestDifferentialAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(6)
		h := randomHistory(rng, n)
		if err := h.Validate(); err != nil {
			t.Fatalf("generated invalid history: %v", err)
		}
		want := bruteForceLinearizable(h)
		report, err := Check(h)
		if err != nil {
			t.Fatalf("check error: %v", err)
		}
		if report.Linearizable != want {
			t.Fatalf("iter %d: checker=%v brute=%v history=%+v", iter, report.Linearizable, want, h.Operations)
		}
		if report.Linearizable {
			if err := VerifyOrder(h, report.Order); err != nil {
				t.Fatalf("iter %d: bad order: %v", iter, err)
			}
		} else if report.Counterexample == nil {
			t.Fatalf("iter %d: missing counterexample", iter)
		}
	}
}

func TestMinimalCounterexampleIsSubset(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	found := 0
	for iter := 0; iter < 200 && found < 20; iter++ {
		h := randomHistory(rng, 5+rng.Intn(3))
		report, err := Check(h)
		if err != nil || report.Linearizable {
			continue
		}
		found++
		// The returned counterexample is itself non-linearizable and is a
		// subsequence obtained by removing operations one at a time.
		ids := map[int]bool{}
		for _, id := range report.Counterexample.OperationIDs {
			if ids[id] {
				t.Fatal("duplicate id in counterexample")
			}
			ids[id] = true
		}
		var subset []Operation
		for _, op := range h.Operations {
			if ids[op.ID] {
				subset = append(subset, op)
			}
		}
		if len(subset) != len(ids) {
			t.Fatal("counterexample ids must reference history operations")
		}
		ok, err := IsLinearizable(History{Operations: subset, WrongLimit: h.WrongLimit})
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("reported counterexample subset is linearizable")
		}
		if len(subset) >= len(h.Operations) && len(h.Operations) > 1 {
			t.Fatal("minimization did not remove any operation")
		}
	}
	if found == 0 {
		t.Fatal("random run produced no counterexamples")
	}
}
