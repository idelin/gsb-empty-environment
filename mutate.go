package main

import (
	"fmt"
	"sort"
)

// ---------- Generic expression-tree transform ----------

// walkExpr applies f to e top-down, then recurses into the (possibly
// replaced) children. AST nodes are immutable values, so the result is
// a rebuilt tree; untouched subtrees are shared.
func walkExpr(e Expr, f func(Expr) Expr) Expr {
	e = f(e)
	switch n := e.(type) {
	case Bin:
		return Bin{n.Op, walkExpr(n.L, f), walkExpr(n.R, f)}
	case Neg:
		return Neg{walkExpr(n.E, f)}
	default:
		return e
	}
}

func walkProg(stmts []Stmt, f func(Expr) Expr) []Stmt {
	out := make([]Stmt, len(stmts))
	for i, s := range stmts {
		switch s := s.(type) {
		case Assign:
			out[i] = Assign{s.Name, walkExpr(s.E, f)}
		case If:
			out[i] = If{walkExpr(s.Cond, f), walkProg(s.Then, f), walkProg(s.Else, f)}
		case Return:
			out[i] = Return{walkExpr(s.E, f)}
		default:
			out[i] = s
		}
	}
	return out
}

// ---------- Mutants ----------

// A Mutant replaces the expression node visited at index Node (in the
// pre-order traversal of walkProg) with Repl.
type Mutant struct {
	ID   int
	Desc string
	Node int
	Repl Expr
}

var cmpOps = []string{"<", "<=", ">", ">=", "==", "!="}

func isCmp(op string) bool {
	for _, c := range cmpOps {
		if c == op {
			return true
		}
	}
	return false
}

// boundAtNodes computes, for every expression node (identified by its
// pre-order index, matching walkProg), the set of variables that are
// definitely bound when that expression is evaluated: inputs plus every
// assignment that dominates the node. After an if, only variables
// assigned on *both* branches count.
func boundAtNodes(prog []Stmt) map[int]map[string]bool {
	sets := map[int]map[string]bool{}
	idx := 0
	var scanExpr func(e Expr, bound map[string]bool)
	scanExpr = func(e Expr, bound map[string]bool) {
		cp := make(map[string]bool, len(bound))
		for k, v := range bound {
			cp[k] = v
		}
		sets[idx] = cp
		idx++
		switch n := e.(type) {
		case Bin:
			scanExpr(n.L, bound)
			scanExpr(n.R, bound)
		case Neg:
			scanExpr(n.E, bound)
		}
	}
	var scanStmts func(ss []Stmt, bound map[string]bool) map[string]bool
	scanStmts = func(ss []Stmt, bound map[string]bool) map[string]bool {
		cur := bound
		for _, s := range ss {
			switch s := s.(type) {
			case Input:
				next := make(map[string]bool, len(cur)+len(s.Names))
				for k, v := range cur {
					next[k] = v
				}
				for _, n := range s.Names {
					next[n] = true
				}
				cur = next
			case Assign:
				scanExpr(s.E, cur)
				next := make(map[string]bool, len(cur)+1)
				for k, v := range cur {
					next[k] = v
				}
				next[s.Name] = true
				cur = next
			case If:
				scanExpr(s.Cond, cur)
				b1 := scanStmts(s.Then, cur)
				b2 := scanStmts(s.Else, cur)
				inter := map[string]bool{}
				for k := range b1 {
					if b2[k] {
						inter[k] = true
					}
				}
				cur = inter
			case Return:
				scanExpr(s.E, cur)
			}
		}
		return cur
	}
	scanStmts(prog, map[string]bool{})
	return sets
}

// genMutants produces all single-point mutants of the program:
//   - arithmetic operator swap:        + <-> -
//   - comparison operator replacement: each op -> the 5 others
//   - constant adjustment:             c -> c+1, c-1 (int8 wrap), 0
//   - variable replacement:            v -> each other variable that is
//     definitely bound at that program point
func genMutants(prog []Stmt) []Mutant {
	bound := boundAtNodes(prog)
	var muts []Mutant
	idx := 0
	walkProg(prog, func(e Expr) Expr {
		add := func(desc string, repl Expr) {
			muts = append(muts, Mutant{ID: len(muts) + 1, Desc: desc, Node: idx, Repl: repl})
		}
		switch n := e.(type) {
		case Bin:
			switch {
			case n.Op == "+":
				add(fmt.Sprintf("arithmetic %s -> %s", exprString(e), exprString(Bin{"-", n.L, n.R})),
					Bin{"-", n.L, n.R})
			case n.Op == "-":
				add(fmt.Sprintf("arithmetic %s -> %s", exprString(e), exprString(Bin{"+", n.L, n.R})),
					Bin{"+", n.L, n.R})
			default:
				for _, op := range cmpOps {
					if op != n.Op {
						add(fmt.Sprintf("comparison %s -> %s", exprString(e), exprString(Bin{op, n.L, n.R})),
							Bin{op, n.L, n.R})
					}
				}
			}
		case Num:
			for _, d := range []int8{1, -1} {
				nv := n.Val + d // int8 wraparound, e.g. 127+1 -> -128
				add(fmt.Sprintf("constant %d -> %d", n.Val, nv), Num{nv})
			}
			if n.Val != 0 {
				add(fmt.Sprintf("constant %d -> 0", n.Val), Num{0})
			}
		case Var:
			var vs []string
			for v := range bound[idx] {
				if v != n.Name {
					vs = append(vs, v)
				}
			}
			sort.Strings(vs)
			for _, v := range vs {
				add(fmt.Sprintf("variable %s -> %s", n.Name, v), Var{v})
			}
		}
		idx++
		return e
	})
	return muts
}

// applyMutant returns a new program with the mutant applied.
func applyMutant(prog []Stmt, m Mutant) []Stmt {
	idx := 0
	return walkProg(prog, func(e Expr) Expr {
		if idx == m.Node {
			idx++
			return m.Repl
		}
		idx++
		return e
	})
}
