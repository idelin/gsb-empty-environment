package main

import "fmt"

// Mutant is a program differing from the original by exactly one mutation.
type Mutant struct {
	ID   int
	Op   string // AOR, ROR, CRR, CND
	Desc string
	Prog *Program
}

// Mutation operators:
//   AOR: arithmetic operator replacement, + <-> -
//   ROR: relational operator replacement among == != < <= > >=
//   CRR: constant replacement, c -> c+1 and c -> c-1 (wrapping)
//   CND: condition negation, if c -> if !c
func GenerateMutants(p *Program) []Mutant {
	var ms []Mutant
	add := func(op, desc string, target, repl Expr) {
		prog := &Program{Inputs: p.Inputs, Body: replaceInStmts(p.Body, target, repl)}
		ms = append(ms, Mutant{ID: len(ms) + 1, Op: op, Desc: desc, Prog: prog})
	}

	var walkExpr func(e Expr)
	walkExpr = func(e Expr) {
		switch n := e.(type) {
		case *Const:
			add("CRR", fmt.Sprintf("line %d: constant %d -> %d", n.Line, n.Val, n.Val+1),
				e, &Const{Val: n.Val + 1, Line: n.Line})
			add("CRR", fmt.Sprintf("line %d: constant %d -> %d", n.Line, n.Val, n.Val-1),
				e, &Const{Val: n.Val - 1, Line: n.Line})
		case *Bin:
			other := "-"
			if n.Op == "-" {
				other = "+"
			}
			add("AOR", fmt.Sprintf("line %d: %s -> %s", n.Line, n.Op, other),
				e, &Bin{Op: other, L: n.L, R: n.R, Line: n.Line})
			walkExpr(n.L)
			walkExpr(n.R)
		case *Cmp:
			for _, alt := range cmpOps {
				if alt != n.Op {
					add("ROR", fmt.Sprintf("line %d: %s -> %s", n.Line, n.Op, alt),
						e, &Cmp{Op: alt, L: n.L, R: n.R, Line: n.Line})
				}
			}
			walkExpr(n.L)
			walkExpr(n.R)
		case *Neg:
			walkExpr(n.E)
		case *Not:
			walkExpr(n.E)
		}
	}

	var walkStmts func(ss []Stmt)
	walkStmts = func(ss []Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *Assign:
				walkExpr(n.E)
			case *If:
				add("CND", fmt.Sprintf("line %d: negate condition", n.Line),
					n.Cond, &Not{E: n.Cond, Line: n.Line})
				walkExpr(n.Cond)
				walkStmts(n.Then)
				walkStmts(n.Else)
			case *Return:
				walkExpr(n.E)
			}
		}
	}
	walkStmts(p.Body)
	return ms
}

// replaceExpr rebuilds e, swapping the pointer-identical target node for repl.
// Untouched subtrees are shared with the original (ASTs are never mutated
// in place, so sharing is safe).
func replaceExpr(e, target, repl Expr) Expr {
	if e == target {
		return repl
	}
	switch n := e.(type) {
	case *Neg:
		return &Neg{E: replaceExpr(n.E, target, repl), Line: n.Line}
	case *Bin:
		return &Bin{Op: n.Op, L: replaceExpr(n.L, target, repl), R: replaceExpr(n.R, target, repl), Line: n.Line}
	case *Cmp:
		return &Cmp{Op: n.Op, L: replaceExpr(n.L, target, repl), R: replaceExpr(n.R, target, repl), Line: n.Line}
	case *Not:
		return &Not{E: replaceExpr(n.E, target, repl), Line: n.Line}
	}
	return e
}

func replaceInStmts(ss []Stmt, target, repl Expr) []Stmt {
	out := make([]Stmt, len(ss))
	for i, s := range ss {
		switch n := s.(type) {
		case *Assign:
			out[i] = &Assign{Name: n.Name, E: replaceExpr(n.E, target, repl), Line: n.Line}
		case *If:
			out[i] = &If{
				Cond: replaceExpr(n.Cond, target, repl),
				Then: replaceInStmts(n.Then, target, repl),
				Else: replaceInStmts(n.Else, target, repl),
				Line: n.Line,
			}
		case *Return:
			out[i] = &Return{E: replaceExpr(n.E, target, repl), Line: n.Line}
		}
	}
	return out
}
