package main

import (
	"fmt"

	"unify"
)

func main() {
	// A successful unification: print the most general unifier.
	s := unify.NewSolver()
	a := unify.Fun(unify.Var("α"), unify.Tuple(unify.Var("β"), unify.Var("γ")))
	b := unify.Fun(unify.Fun(unify.Rigid("Int"), unify.Var("γ")), unify.Var("δ"))
	if c := s.Unify(a, b); c != nil {
		fmt.Println(c)
		return
	}
	fmt.Printf("%s\n= %s\nMGU:\n", a, b)
	for name, t := range s.Subst() {
		fmt.Printf("  %s := %s\n", name, t)
	}

	// A failing session: print the shortest conflict chain.
	fmt.Println("---")
	s2 := unify.NewSolver()
	s2.Unify(unify.Var("α"), unify.Var("β"))
	s2.Unify(unify.Var("β"), unify.Var("γ"))
	s2.Unify(unify.Var("γ"), unify.Rigid("Int"))
	if c := s2.Unify(unify.Var("α"), unify.Rigid("Bool")); c != nil {
		fmt.Println(c)
	}

	// Occurs check: recursive type rejected with its explanation.
	fmt.Println("---")
	s3 := unify.NewSolver()
	s3.Unify(unify.Var("α"), unify.Fun(unify.Var("β"), unify.Rigid("Int")))
	if c := s3.Unify(unify.Var("β"), unify.Tuple(unify.Var("α"))); c != nil {
		fmt.Println(c)
	}
}
