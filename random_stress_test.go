package netsec

import (
	"errors"
	"math/rand"
	"testing"
)

func TestRandomizedSmallProgramsSoundness(t *testing.T) {
	rng := rand.New(rand.NewSource(20261115))
	vars := []string{"o", "q", "p", "r", "x"}

	genValue := func(depth int) Expr {
		choices := 7
		if depth <= 0 {
			choices = 4
		}
		switch rng.Intn(choices) {
		case 0:
			return NewObject()
		case 1:
			return Var(vars[rng.Intn(len(vars))])
		case 2:
			return Lit(stringDomain[rng.Intn(len(stringDomain))])
		case 3:
			return External()
		default:
			base := Expr(Var(vars[rng.Intn(len(vars))]))
			if rng.Intn(3) == 0 {
				base = Get(base, Lit("constructor"))
			}
			key := Expr(Lit(stringDomain[rng.Intn(len(stringDomain))]))
			if rng.Intn(4) == 0 {
				key = External()
			}
			return Get(base, key)
		}
	}

	var genStmt func(depth int) Stmt
	genStmt = func(depth int) Stmt {
		switch rng.Intn(6) {
		case 0:
			return Let(vars[rng.Intn(len(vars))], genValue(1))
		case 1:
			return SetField(Var(vars[rng.Intn(len(vars))]), genValue(0), genValue(1))
		case 2:
			base := Get(Var(vars[rng.Intn(len(vars))]), Lit("__proto__"))
			return SetField(base, genValue(0), genValue(1))
		case 3:
			if depth > 0 {
				return If(&EqExpr{Left: genValue(0), Right: genValue(1)},
					[]Stmt{genStmt(depth - 1)},
					[]Stmt{genStmt(depth - 1)},
				)
			}
			return Let(vars[rng.Intn(len(vars))], genValue(0))
		case 4:
			if depth > 0 {
				n := 1 + rng.Intn(3)
				body := make([]Stmt, 0, n)
				for i := 0; i < n; i++ {
					body = append(body, genStmt(depth-1))
				}
				return Repeat(1+rng.Intn(3), body...)
			}
			return SetField(Var("o"), Lit("__proto__"), NewObject())
		default:
			return Let("k", External())
		}
	}

	const programs = 120
	for i := 0; i < programs; i++ {
		n := 1 + rng.Intn(6)
		stmts := make([]Stmt, 0, n)
		for j := 0; j < n; j++ {
			stmts = append(stmts, genStmt(2))
		}
		p := Seq(stmts...)
		if err := Validate(p); err != nil {
			continue
		}
		res, err := CheckSoundness(p, 200000)
		if err != nil {
			if errors.Is(err, ErrExecutionLimit) || errors.Is(err, ErrTooManyObjects) {
				continue
			}
			t.Fatalf("program %d: %v\n%s", i, err, ProgramString(p))
		}
		if len(res.MissedNodes) != 0 {
			t.Fatalf("program %d missed nodes %v\n%s", i, res.MissedNodes, ProgramString(p))
		}
		if len(res.AnalyzerOnlyNodes) > 0 {
			// Analyzer-only nodes are allowed for branches, but keep a hint
			// available when debugging precision regressions.
			t.Logf("program %d analyzer-only nodes: %v", i, res.AnalyzerOnlyNodes)
		}
	}
}
