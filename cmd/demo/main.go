// Command demo builds a small AST, runs the attribute-grammar evaluator,
// and prints synthesized types/values plus diagnostics.
package main

import (
	"fmt"

	"attrgram"
)

func p(line, col int) attrgram.Pos { return attrgram.Pos{Line: line, Col: col} }

func main() {
	prog := &attrgram.Program{
		Pos: p(1, 1),
		Stmts: []attrgram.Stmt{
			// 1: a := 1 + 2
			&attrgram.Decl{Pos: p(1, 1), Name: "a", Init: &attrgram.Binary{Pos: p(1, 6), Op: "+",
				L: &attrgram.IntLit{Pos: p(1, 6), Val: 1},
				R: &attrgram.IntLit{Pos: p(1, 10), Val: 2}}},
			// 2: b := a * 2
			&attrgram.Decl{Pos: p(2, 1), Name: "b", Init: &attrgram.Binary{Pos: p(2, 6), Op: "*",
				L: &attrgram.Var{Pos: p(2, 6), Name: "a"},
				R: &attrgram.IntLit{Pos: p(2, 10), Val: 2}}},
			// 3: print a + b
			&attrgram.Print{Pos: p(3, 1), Expr: &attrgram.Binary{Pos: p(3, 7), Op: "+",
				L: &attrgram.Var{Pos: p(3, 7), Name: "a"},
				R: &attrgram.Var{Pos: p(3, 11), Name: "b"}}},
			// 4: bad := 1 + true   (type mismatch, reported at 4:8)
			&attrgram.Decl{Pos: p(4, 1), Name: "bad", Init: &attrgram.Binary{Pos: p(4, 8), Op: "+",
				L: &attrgram.IntLit{Pos: p(4, 8), Val: 1},
				R: &attrgram.BoolLit{Pos: p(4, 12), Val: true}}},
			// 5: print missing     (undefined variable, reported at 5:7)
			&attrgram.Print{Pos: p(5, 1), Expr: &attrgram.Var{Pos: p(5, 7), Name: "missing"}},
		},
	}

	res, err := attrgram.Evaluate(prog)
	if err != nil {
		fmt.Println("evaluation failed:", err)
		return
	}

	fmt.Println("== inferred types & values ==")
	for _, s := range prog.Stmts {
		if d, ok := s.(*attrgram.Decl); ok {
			fmt.Printf("%s: %s : %s = %v\n", d.Pos, d.Name, res.TypeOf(d.Init), res.ValueOf(d.Init))
		}
	}
	fmt.Println("== print output ==")
	for _, line := range res.Prints {
		fmt.Println(line)
	}
	fmt.Println("== diagnostics ==")
	if len(res.Diags) == 0 {
		fmt.Println("(none)")
	}
	for _, d := range res.Diags {
		fmt.Println(d)
	}
}
