package attrgram

import (
	"reflect"
	"strings"
	"testing"
)

// sampleProgram builds:
//
//	1: a := 1 + 2
//	2: b := a * 2
//	3: c := b / 2
//	4: ok := c == 3
//	5: print ok
//	6: print a + b
func sampleProgram() *Program {
	p := func(line, col int) Pos { return Pos{Line: line, Col: col} }
	decl := func(line int, name string, init Expr) *Decl {
		return &Decl{Pos: p(line, 1), Name: name, Init: init}
	}
	return &Program{
		Pos: p(1, 1),
		Stmts: []Stmt{
			decl(1, "a", &Binary{Pos: p(1, 6), Op: "+", L: &IntLit{Pos: p(1, 6), Val: 1}, R: &IntLit{Pos: p(1, 10), Val: 2}}),
			decl(2, "b", &Binary{Pos: p(2, 6), Op: "*", L: &Var{Pos: p(2, 6), Name: "a"}, R: &IntLit{Pos: p(2, 10), Val: 2}}),
			decl(3, "c", &Binary{Pos: p(3, 6), Op: "/", L: &Var{Pos: p(3, 6), Name: "b"}, R: &IntLit{Pos: p(3, 10), Val: 2}}),
			decl(4, "ok", &Binary{Pos: p(4, 7), Op: "==", L: &Var{Pos: p(4, 7), Name: "c"}, R: &IntLit{Pos: p(4, 12), Val: 3}}),
			&Print{Pos: p(5, 1), Expr: &Var{Pos: p(5, 7), Name: "ok"}},
			&Print{Pos: p(6, 1), Expr: &Binary{Pos: p(6, 7), Op: "+", L: &Var{Pos: p(6, 7), Name: "a"}, R: &Var{Pos: p(6, 11), Name: "b"}}},
		},
	}
}

func TestInheritedAndSynthesizedAttributes(t *testing.T) {
	prog := sampleProgram()
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diags)
	}

	// Synthesized types propagate bottom-up.
	declA := prog.Stmts[0].(*Decl)
	if got := res.TypeOf(declA.Init); got != TInt {
		t.Errorf("type of (1+2) = %v, want int", got)
	}
	declOK := prog.Stmts[3].(*Decl)
	if got := res.TypeOf(declOK.Init); got != TBool {
		t.Errorf("type of (c==3) = %v, want bool", got)
	}

	// Synthesized values propagate bottom-up.
	if got := res.ValueOf(declA.Init); got != int64(3) {
		t.Errorf("value of (1+2) = %v, want 3", got)
	}
	if got := res.ValueOf(declOK.Init); got != true {
		t.Errorf("value of (c==3) = %v, want true", got)
	}

	// Inherited env flows top-down: each statement sees prior declarations.
	env := res.EnvAfter(prog.Stmts[3])
	want := Env{
		"a":  Symbol{Type: TInt, Val: int64(3)},
		"b":  Symbol{Type: TInt, Val: int64(6)},
		"c":  Symbol{Type: TInt, Val: int64(3)},
		"ok": Symbol{Type: TBool, Val: true},
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env after stmt 4 = %v, want %v", env, want)
	}

	if got := res.Prints; !reflect.DeepEqual(got, []string{"true", "9"}) {
		t.Errorf("prints = %v, want [true 9]", got)
	}
}

func TestTypeMismatchReportsNodePosition(t *testing.T) {
	// 1: x := 1 + true
	bin := &Binary{Pos: Pos{Line: 1, Col: 6}, Op: "+",
		L: &IntLit{Pos: Pos{Line: 1, Col: 6}, Val: 1},
		R: &BoolLit{Pos: Pos{Line: 1, Col: 10}, Val: true},
	}
	prog := &Program{Pos: Pos{1, 1}, Stmts: []Stmt{
		&Decl{Pos: Pos{Line: 1, Col: 1}, Name: "x", Init: bin},
	}}
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1", res.Diags)
	}
	d := res.Diags[0]
	if d.Pos != bin.Pos {
		t.Errorf("diagnostic at %v, want binary node position %v", d.Pos, bin.Pos)
	}
	if !strings.Contains(d.Msg, "type mismatch") || !strings.Contains(d.Msg, "int") || !strings.Contains(d.Msg, "bool") {
		t.Errorf("unexpected message: %q", d.Msg)
	}
	if got := res.TypeOf(bin); got != TUnknown {
		t.Errorf("type of mismatched expr = %v, want <error>", got)
	}
}

func TestUndefinedVariablePosition(t *testing.T) {
	// 1: y := z
	v := &Var{Pos: Pos{Line: 1, Col: 6}, Name: "z"}
	prog := &Program{Pos: Pos{1, 1}, Stmts: []Stmt{
		&Decl{Pos: Pos{Line: 1, Col: 1}, Name: "y", Init: v},
	}}
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1", res.Diags)
	}
	if res.Diags[0].Pos != v.Pos {
		t.Errorf("diagnostic at %v, want var position %v", res.Diags[0].Pos, v.Pos)
	}
	if !strings.Contains(res.Diags[0].Msg, `undefined variable "z"`) {
		t.Errorf("unexpected message: %q", res.Diags[0].Msg)
	}
}

func TestUseBeforeDeclaration(t *testing.T) {
	// Inherited env is threaded sequentially: a use before its decl fails.
	// 1: print x
	// 2: x := 1
	prog := &Program{Pos: Pos{1, 1}, Stmts: []Stmt{
		&Print{Pos: Pos{Line: 1, Col: 1}, Expr: &Var{Pos: Pos{Line: 1, Col: 7}, Name: "x"}},
		&Decl{Pos: Pos{Line: 2, Col: 1}, Name: "x", Init: &IntLit{Pos: Pos{Line: 2, Col: 6}, Val: 1}},
	}}
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 1 || !strings.Contains(res.Diags[0].Msg, "undefined variable") {
		t.Fatalf("diags = %v, want 1 undefined-variable error", res.Diags)
	}
	if res.Diags[0].Pos.Line != 1 {
		t.Errorf("diagnostic line = %d, want 1", res.Diags[0].Pos.Line)
	}
}

func TestRedeclaration(t *testing.T) {
	// 1: x := 1
	// 2: x := 2
	prog := &Program{Pos: Pos{1, 1}, Stmts: []Stmt{
		&Decl{Pos: Pos{Line: 1, Col: 1}, Name: "x", Init: &IntLit{Pos: Pos{Line: 1, Col: 6}, Val: 1}},
		&Decl{Pos: Pos{Line: 2, Col: 1}, Name: "x", Init: &IntLit{Pos: Pos{Line: 2, Col: 6}, Val: 2}},
	}}
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 1 || !strings.Contains(res.Diags[0].Msg, `variable "x" redeclared`) {
		t.Fatalf("diags = %v, want 1 redeclaration error", res.Diags)
	}
	if res.Diags[0].Pos.Line != 2 {
		t.Errorf("diagnostic line = %d, want 2", res.Diags[0].Pos.Line)
	}
	// Original binding survives.
	if sym := res.EnvAfter(prog.Stmts[1])["x"]; sym.Val != int64(1) {
		t.Errorf("x = %v after redeclaration, want 1", sym.Val)
	}
}

func TestDivisionByZero(t *testing.T) {
	// 1: x := 4 / 0
	bin := &Binary{Pos: Pos{Line: 1, Col: 6}, Op: "/",
		L: &IntLit{Pos: Pos{Line: 1, Col: 6}, Val: 4},
		R: &IntLit{Pos: Pos{Line: 1, Col: 10}, Val: 0},
	}
	prog := &Program{Pos: Pos{1, 1}, Stmts: []Stmt{
		&Decl{Pos: Pos{Line: 1, Col: 1}, Name: "x", Init: bin},
	}}
	res, err := Evaluate(prog)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Diags) != 1 || !strings.Contains(res.Diags[0].Msg, "division by zero") {
		t.Fatalf("diags = %v, want division-by-zero", res.Diags)
	}
	if res.Diags[0].Pos != bin.Pos {
		t.Errorf("diagnostic at %v, want %v", res.Diags[0].Pos, bin.Pos)
	}
}

func TestCycleDetection(t *testing.T) {
	a := &IntLit{Pos: Pos{Line: 1, Col: 1}, Val: 1}
	b := &IntLit{Pos: Pos{Line: 2, Col: 1}, Val: 2}
	idA := AttrID{a, "x"}
	idB := AttrID{b, "y"}

	eng := NewEngine()
	eng.Add(idA, []AttrID{idB}, func(get func(AttrID) interface{}) interface{} { return get(idB) })
	eng.Add(idB, []AttrID{idA}, func(get func(AttrID) interface{}) interface{} { return get(idA) })

	err := eng.Eval()
	cyc, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("err = %v, want *CycleError", err)
	}
	if len(cyc.Remaining) != 2 {
		t.Errorf("cycle involves %v, want 2 attributes", cyc.Remaining)
	}
	if !strings.Contains(cyc.Error(), "IntLit@1:1.x") || !strings.Contains(cyc.Error(), "IntLit@2:1.y") {
		t.Errorf("cycle message = %q", cyc.Error())
	}
}

func TestDeterministicTopologicalOrder(t *testing.T) {
	// Diamond dependency: d <- {b, c} <- a. b and c must run after a,
	// d after both, and ready nodes run in registration order.
	n := &IntLit{Pos: Pos{Line: 1, Col: 1}, Val: 0}
	id := func(name string) AttrID { return AttrID{n, name} }
	var order []string
	eng := NewEngine()
	mk := func(name string, deps ...AttrID) {
		eng.Add(id(name), deps, func(get func(AttrID) interface{}) interface{} {
			order = append(order, name)
			return nil
		})
	}
	mk("a")
	mk("b", id("a"))
	mk("c", id("a"))
	mk("d", id("b"), id("c"))
	if err := eng.Eval(); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b", "c", "d"}) {
		t.Errorf("order = %v, want [a b c d]", order)
	}
}
