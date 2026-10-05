// Command demo exercises the unit inference engine on a few examples.
package main

import (
	"fmt"

	ui "unitinfer"
)

func main() {
	dims := []string{"L", "M", "T"}

	// 1. kinetic energy: e = m * (d / t)^2 with d : L, t : T, e : L^2*M*T^-2.
	in := ui.NewInfer(3, dims)
	e := in.Gen(ui.Mul{
		L: ui.Var{Name: "m"},
		R: ui.Pow{Base: ui.Div{L: ui.Var{Name: "d"}, R: ui.Var{Name: "t"}}, N: 2},
	})
	in.ConstrainEq(e, ui.ConstTerm([]int{2, 1, -2}), "e is an energy")
	in.ConstrainEq(in.Gen(ui.Var{Name: "d"}), ui.ConstTerm([]int{1, 0, 0}), "d is a length")
	in.ConstrainEq(in.Gen(ui.Var{Name: "t"}), ui.ConstTerm([]int{0, 0, 1}), "t is a time")
	res := in.Solve()
	fmt.Println("== kinetic energy ==")
	if res.OK {
		fmt.Println(res.Solution.String())
	} else {
		fmt.Println("conflict:", res.Conflict)
	}

	// 2. underdetermined: x * y is a length -> one free direction remains.
	in2 := ui.NewInfer(3, dims)
	p := in2.Gen(ui.Mul{L: ui.Var{Name: "x"}, R: ui.Var{Name: "y"}})
	in2.ConstrainEq(p, ui.ConstTerm([]int{1, 0, 0}), "product is a length")
	res2 := in2.Solve()
	fmt.Println("\n== x*y : L (underdetermined) ==")
	fmt.Println(res2.Solution.String())
	fmt.Println("underdetermined:", res2.Solution.Underdetermined())
	if err := in2.Verify(res2.Solution, -2, 2); err != nil {
		fmt.Println("verification failed:", err)
	} else {
		fmt.Println("enumeration check on [-2,2]: ok")
	}

	// 3. conflict: f + 1[L] and f + 1[T].
	in3 := ui.NewInfer(3, dims)
	in3.Gen(ui.Add{L: ui.Var{Name: "f"}, R: ui.Const{Unit: []int{1, 0, 0}}})
	in3.Gen(ui.Add{L: ui.Var{Name: "f"}, R: ui.Const{Unit: []int{0, 0, 1}}})
	res3 := in3.Solve()
	fmt.Println("\n== conflicting adds ==")
	if !res3.OK {
		for _, c := range res3.Conflict {
			fmt.Println("conflict constraint:", c.Where)
		}
	}

	// 4. unit-polymorphic function: square = forall a. a -> a^2.
	square := &ui.Signature{
		Name: "square", NumVars: 1,
		Params: []ui.Term{ui.VarTerm(0)},
		Result: ui.VarTerm(0).Scale(2),
	}
	in4 := ui.NewInfer(3, dims)
	r := in4.Gen(ui.Call{Fn: square, Args: []ui.Expr{ui.Var{Name: "v"}}})
	in4.ConstrainEq(r, ui.ConstTerm([]int{2, 0, -2}), "v^2 is (m/s)^2")
	res4 := in4.Solve()
	fmt.Println("\n== square(v) : L^2*T^-2 ==")
	fmt.Println(res4.Solution.String())
}
