// Command demo exercises the unit inference engine on three scenarios:
// a fully determined physics formula, an underdetermined expression, and
// an inconsistent system where the minimal conflict set is reported.
package main

import (
	"fmt"

	"unitinfer"
)

var dims = []string{"L", "M", "T"}

func main() {
	kineticEnergy()
	underdetermined()
	conflict()
}

func kineticEnergy() {
	fmt.Println("== E = m * v^2, m: M, v: L/T ==")
	b := unitinfer.NewBuilder()
	m := b.Var("m")
	v := b.Var("v")
	e := b.Bin(unitinfer.OpMul, m, b.Pow(v, 2))

	in := unitinfer.NewInferencer(3)
	rv := in.Infer(e)
	constrain(in, m, unitinfer.BaseTerm(0, 1, 0), "m is a mass")
	constrain(in, v, unitinfer.BaseTerm(1, 0, -1), "v is a velocity")

	sol, ok := unitinfer.Solve(3, in.Constraints(), in.Vars())
	if !ok {
		fmt.Println("  inconsistent!")
		return
	}
	fmt.Printf("  unit(E) = %s   (free params: %d)\n\n", sol.UnitString(rv, dims), sol.NumParams())
}

func underdetermined() {
	fmt.Println("== x + y, units unknown ==")
	b := unitinfer.NewBuilder()
	x := b.Var("x")
	y := b.Var("y")
	e := b.Bin(unitinfer.OpAdd, x, y)

	in := unitinfer.NewInferencer(2)
	rv := in.Infer(e)

	sol, ok := unitinfer.Solve(2, in.Constraints(), in.Vars())
	if !ok {
		fmt.Println("  inconsistent!")
		return
	}
	fmt.Printf("  unit(x) = %s\n", sol.UnitString(in.UnitVar(x), dims))
	fmt.Printf("  unit(y) = %s\n", sol.UnitString(in.UnitVar(y), dims))
	fmt.Printf("  unit(x+y) = %s   (free params: %d)\n", sol.UnitString(rv, dims), sol.NumParams())

	rep, err := unitinfer.CheckInclusion(sol, in.Constraints(), -2, 2)
	if err != nil {
		fmt.Println("  inclusion check FAILED:", err)
		return
	}
	fmt.Printf("  enumeration [-2,2]: %d solutions, free vars: %d, collapsed: %d (none pinned to 1)\n\n",
		len(rep.Solutions), len(rep.FreeVars), len(rep.Collapsed))
}

func conflict() {
	fmt.Println("== (a * b) + c with a: L, b: T, c: M ==")
	b := unitinfer.NewBuilder()
	a := b.Var("a")
	bv := b.Var("b")
	c := b.Var("c")
	e := b.Bin(unitinfer.OpAdd, b.Bin(unitinfer.OpMul, a, bv), c)

	in := unitinfer.NewInferencer(3)
	in.Infer(e)
	constrain(in, a, unitinfer.BaseTerm(1, 0, 0), "a is a length")
	constrain(in, bv, unitinfer.BaseTerm(0, 0, 1), "b is a time")
	constrain(in, c, unitinfer.BaseTerm(0, 1, 0), "c is a mass")

	cons := in.Constraints()
	if unitinfer.Consistent(3, cons, in.Vars()) {
		fmt.Println("  unexpectedly consistent")
		return
	}
	core := unitinfer.MinimalConflict(3, cons, in.Vars())
	fmt.Printf("  inconsistent; minimal conflict = %d of %d constraints, nodes %v\n",
		len(core), len(cons), unitinfer.ConflictNodes(core))
	for _, cc := range core {
		fmt.Println("   ", cc.String())
	}
}

func constrain(in *unitinfer.Inferencer, e *unitinfer.Expr, u unitinfer.Term, desc string) {
	in.ConstrainVar(e, u, desc)
}
