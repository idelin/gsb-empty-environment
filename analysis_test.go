package taskpkg

import "testing"

func TestWitnessShortestAndCrossCheck(t *testing.T) {
	san := leafCheck(0, "url", "san", true, EmitOnTrue)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(2, checkStmt(1, san), forwardStmt(3, "fwd",
			Mark{Field: "url", Name: "san", Trusted: true})),
	}
	prog := mustCompile(t, rule)
	req := Mark{Field: "url", Name: "san", Trusted: true}

	w, err := prog.ShortestMissingWitness(3, req)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Reachable || w.Assignment[0] {
		t.Fatalf("want reachable witness with input0=false, got %+v", w)
	}
	tr, err := prog.Evaluate(w.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Decision != DecisionDenied {
		t.Fatalf("witness assignment must deny, got %s", tr.Decision)
	}

	// Forward with no producing check: always reachable and always denying.
	rule2 := &Rule{Inputs: 1, Body: forwardStmt(9, "lonely",
		Mark{Field: "x", Name: "never", Trusted: true})}
	prog2 := mustCompile(t, rule2)
	w2, err := prog2.ShortestMissingWitness(9, Mark{Field: "x", Name: "never", Trusted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !w2.Reachable {
		t.Fatal("forward with no producing check must be reachable and denying")
	}
}

func TestWitnessPrefersShorterPath(t *testing.T) {
	// Two branches reach the same forward; the branch without the producing
	// check gives the shortest missing-mark witness.
	san := leafCheck(0, "url", "san", true, EmitAlways)
	cond := checkExpr(4, pureCheck(1, "switch"))
	withSan := seqStmt(5, checkStmt(2, san), forwardStmt(3, "fwd",
		Mark{Field: "url", Name: "san", Trusted: true}))
	withoutSan := forwardStmt(6, "fwd",
		Mark{Field: "url", Name: "san", Trusted: true})
	rule := &Rule{
		Inputs: 2,
		Body:   branchStmt(7, cond, withSan, withoutSan),
	}
	prog := mustCompile(t, rule)
	w, err := prog.ShortestMissingWitness(6, Mark{Field: "url", Name: "san", Trusted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Reachable {
		t.Fatal("witness should be reachable")
	}
	// The shortest path goes through the else branch (no sanitizer op).
	if w.Assignment[1] {
		t.Fatalf("shortest witness should take the else branch, got %+v", w.Assignment)
	}
}

func TestEnumerateGuarantees(t *testing.T) {
	san := leafCheck(0, "url", "san", true, EmitOnTrue)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(2, checkStmt(1, san), forwardStmt(3, "fwd",
			Mark{Field: "url", Name: "san", Trusted: true})),
	}
	prog := mustCompile(t, rule)
	guarantees, err := prog.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if len(guarantees) != 1 {
		t.Fatalf("want 1 forward guarantee, got %d", len(guarantees))
	}
	g := guarantees[0]
	if !g.Reachable ||
		len(g.ReachableMarks) != 2 ||
		len(g.Denied) != 1 ||
		len(g.ForwardedMarks) != 1 ||
		len(g.AlwaysPresent) != 0 {
		t.Fatalf("unexpected guarantee: %+v", g)
	}
}

// TestWitnessMatchesEnumeration cross-checks BFS witnesses against brute-force
// enumeration on the differential rule battery.
func TestWitnessMatchesEnumeration(t *testing.T) {
	for ri, rule := range buildDifferentialRules() {
		prog := mustCompile(t, rule)
		forwardIDs := collectForwardIDs(rule)
		for _, fid := range forwardIDs {
			fwd := findForwardInRule(rule, fid)
			for _, req := range fwd.Required {
				w, err := prog.ShortestMissingWitness(fid, req)
				if err != nil {
					t.Fatalf("rule %d forward %d: %v", ri, fid, err)
				}
				brute := bruteForceShortest(prog, fid, req)
				if brute == nil && w.Reachable {
					t.Fatalf("rule %d: BFS found witness but brute force did not", ri)
				}
				if brute != nil {
					if !w.Reachable {
						t.Fatalf("rule %d: brute force found witness but BFS did not", ri)
					}
					if w.Steps != brute.Steps {
						t.Fatalf("rule %d forward %d: BFS steps=%d brute=%d",
							ri, fid, w.Steps, brute.Steps)
					}
					tr, err := prog.Evaluate(w.Assignment)
					if err != nil {
						t.Fatal(err)
					}
					if tr.Decision != DecisionDenied || tr.ForwardName != fwd.Name {
						t.Fatalf("rule %d: witness does not deny at target forward", ri)
					}
				}
			}
		}
	}
}

func collectForwardIDs(r *Rule) []int {
	var ids []int
	var walk func(s *Stmt)
	walk = func(s *Stmt) {
		if s == nil {
			return
		}
		if s.Kind == StmtForward {
			ids = append(ids, s.ID)
		}
		walk(s.First)
		walk(s.Second)
		walk(s.Then)
		walk(s.Else)
		walk(s.Handler)
	}
	walk(r.Body)
	return ids
}

func findForwardInRule(r *Rule, id int) *Forward {
	var found *Forward
	var walk func(s *Stmt)
	walk = func(s *Stmt) {
		if s == nil || found != nil {
			return
		}
		if s.Kind == StmtForward && s.ID == id {
			found = s.Forward
			return
		}
		walk(s.First)
		walk(s.Second)
		walk(s.Then)
		walk(s.Else)
		walk(s.Handler)
	}
	walk(r.Body)
	return found
}

func bruteForceShortest(prog *Program, fid int, wanted Mark) *Witness {
	var best *Witness
	n := prog.rule.Inputs
	for mask := 0; mask < 1<<n; mask++ {
		assignment := decode(mask, n)
		var reachedTarget bool
		var targetMarks []Mark
		var targetDenied bool
		leafCount := 0
		var stepsAtTarget int
		k := newKernel(prog, assignment, modeCompiled)
		k.runWithHook(func(op *Op) {
			if op.Kind == opLeaf {
				leafCount++
			}
			if op.Kind != opForward || op.NodeID != fid {
				return
			}
			reachedTarget = true
			stepsAtTarget = leafCount
			targetMarks = append([]Mark(nil), k.marks...)
			if _, bad := MissingRequired(k.marks, op.Fwd.Required); bad {
				targetDenied = true
			}
		})
		k.run()
		if !reachedTarget || !targetDenied {
			continue
		}
		if !HasMark(targetMarks, wanted) {
			if best == nil || stepsAtTarget < best.Steps {
				best = &Witness{Assignment: assignment, Steps: stepsAtTarget, Reachable: true}
			}
		}
	}
	return best
}
