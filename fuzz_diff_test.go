package taskpkg

import (
	"math/rand"
	"testing"
)

// TestRandomizedDifferential generates random valid rules and compares both
// executors over every assignment. Inputs stay <= MaxInputs.
func TestRandomizedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 40; iter++ {
		rule := generateRule(rng, 3+rng.Intn(MaxInputs-3))
		prog, err := Compile(rule)
		if err != nil {
			t.Fatalf("iter %d compile: %v", iter, err)
		}
		for mask := 0; mask < 1<<rule.Inputs; mask++ {
			inputs := decode(mask, rule.Inputs)
			want, err := Interpret(rule, inputs)
			if err != nil {
				t.Fatalf("iter %d interpret: %v", iter, err)
			}
			got, err := prog.Evaluate(inputs)
			if err != nil {
				t.Fatalf("iter %d evaluate: %v", iter, err)
			}
			if err := CompareTraces(want, got); err != nil {
				t.Fatalf("iter %d mask %b divergence: %v", iter, mask, err)
			}
		}

		// Witness BFS must agree with brute force on every forward requirement.
		for _, fid := range collectForwardIDs(rule) {
			fwd := findForwardInRule(rule, fid)
			for _, req := range fwd.Required {
				w, err := prog.ShortestMissingWitness(fid, req)
				if err != nil {
					t.Fatalf("iter %d witness: %v", iter, err)
				}
				brute := bruteForceShortest(prog, fid, req)
				if (brute == nil) == w.Reachable {
					t.Fatalf("iter %d forward %d: reachability mismatch", iter, fid)
				}
				if brute != nil && w.Steps != brute.Steps {
					t.Fatalf("iter %d forward %d: steps bfs=%d brute=%d",
						iter, fid, w.Steps, brute.Steps)
				}
			}
		}
	}
}

type gen struct {
	rng     *rand.Rand
	inputs  int
	nodeSeq int
	checks  map[int]*Check
}

func generateRule(rng *rand.Rand, inputs int) *Rule {
	g := &gen{rng: rng, inputs: inputs, checks: map[int]*Check{}}
	body := g.genStmt(3)
	return &Rule{Inputs: inputs, Body: body}
}

func (g *gen) nextID() int {
	g.nodeSeq++
	return g.nodeSeq
}

func (g *gen) checkFor(id int) *Check {
	if c, ok := g.checks[id]; ok {
		return c
	}
	c := &Check{ID: id, Field: "f"}
	switch g.rng.Intn(3) {
	case 0:
		c.Emit = EmitNever
	case 1:
		c.Emit = EmitOnTrue
		c.MarkName = "m"
		c.Trusted = g.rng.Intn(2) == 0
	default:
		if g.rng.Intn(2) == 0 {
			b := g.rng.Intn(2) == 0
			c.RaiseOn = &b
		} else {
			c.Emit = EmitAlways
			c.MarkName = "m"
		}
	}
	g.checks[id] = c
	return c
}

func (g *gen) genExpr(depth int) *Expr {
	if depth <= 0 || g.rng.Intn(3) == 0 {
		id := g.rng.Intn(g.inputs)
		return &Expr{ID: g.nextID(), Kind: ExprCheck, Check: g.checkFor(id)}
	}
	switch g.rng.Intn(3) {
	case 0:
		return &Expr{ID: g.nextID(), Kind: ExprNot, Child: g.genExpr(depth - 1)}
	case 1:
		return &Expr{ID: g.nextID(), Kind: ExprAnd,
			Left: g.genExpr(depth - 1), Right: g.genExpr(depth - 1)}
	default:
		return &Expr{ID: g.nextID(), Kind: ExprOr,
			Left: g.genExpr(depth - 1), Right: g.genExpr(depth - 1)}
	}
}

func (g *gen) genStmt(depth int) *Stmt {
	if depth <= 0 {
		id := g.rng.Intn(g.inputs)
		return &Stmt{ID: g.nextID(), Kind: StmtCheck, Check: g.checkFor(id)}
	}
	switch g.rng.Intn(6) {
	case 0:
		id := g.rng.Intn(g.inputs)
		return &Stmt{ID: g.nextID(), Kind: StmtCheck, Check: g.checkFor(id)}
	case 1:
		return &Stmt{ID: g.nextID(), Kind: StmtReject, Reject: "r"}
	case 2:
		req := []Mark{{Field: "f", Name: "m", Trusted: g.rng.Intn(2) == 0}}
		return &Stmt{ID: g.nextID(), Kind: StmtForward,
			Forward: &Forward{Name: "fwd", Required: req}}
	case 3:
		return &Stmt{ID: g.nextID(), Kind: StmtSeq,
			First: g.genStmt(depth - 1), Second: g.genStmt(depth - 1)}
	case 4:
		var els *Stmt
		if g.rng.Intn(2) == 0 {
			els = g.genStmt(depth - 1)
		}
		return &Stmt{ID: g.nextID(), Kind: StmtBranch,
			Cond: g.genExpr(depth), Then: g.genStmt(depth - 1), Else: els}
	default:
		var handler *Stmt
		if g.rng.Intn(2) == 0 {
			handler = g.genStmt(depth - 1)
		}
		return &Stmt{ID: g.nextID(), Kind: StmtTry,
			Then: g.genStmt(depth - 1), Handler: handler}
	}
}
