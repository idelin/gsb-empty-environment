package mini

import (
	"strings"
	"testing"
)

func check(t *testing.T, src string) *Result {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	res, err := Check(prog)
	if err != nil {
		t.Fatalf("evaluation error: %v", err)
	}
	return res
}

func envType(t *testing.T, env *Env, name string) Type {
	t.Helper()
	typ, ok := env.Lookup(name)
	if !ok {
		t.Fatalf("variable %q not in final environment %v", name, env)
	}
	return typ
}

// TestTypeInference covers the acceptance boundary: declarations with and
// without explicit types, inherited env flowing top-down into expressions,
// synthesized types flowing bottom-up.
func TestTypeInference(t *testing.T) {
	res := check(t, `
var x int = 1 + 2
var y = x * 3
var f float = y
var g = f / 2.0
var s string = "hello" + " " + "world"
var b bool = x < y && !(x == y)
var r = s > "abc"
var m = 7 % 3
`)
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	want := map[string]Type{
		"x": TInt, "y": TInt, "f": TFloat, "g": TFloat,
		"s": TString, "b": TBool, "r": TBool, "m": TInt,
	}
	for name, w := range want {
		if got := envType(t, res.Env, name); got != w {
			t.Errorf("type of %q = %s, want %s", name, got, w)
		}
	}
}

// TestInheritedEnvTopDown verifies that a variable is only visible to
// statements after its declaration (inherited env threading).
func TestInheritedEnvTopDown(t *testing.T) {
	res := check(t, "var y = x + 1\nvar x int = 2\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Pos != (Pos{Line: 1, Col: 9}) {
		t.Errorf("diagnostic position = %s, want 1:9", d.Pos)
	}
	if !strings.Contains(d.Msg, `undefined variable "x"`) {
		t.Errorf("unexpected message: %s", d.Msg)
	}
}

// TestBinaryTypeMismatch checks precise operator-position reporting.
func TestBinaryTypeMismatch(t *testing.T) {
	res := check(t, "var s string = \"a\"\nvar bad = 1 + s\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	// Column 13 is the '+' in `var bad = 1 + s`.
	if d.Pos != (Pos{Line: 2, Col: 13}) {
		t.Errorf("diagnostic position = %s, want 2:13", d.Pos)
	}
	if !strings.Contains(d.Msg, `cannot apply operator "+" to int and string`) {
		t.Errorf("unexpected message: %s", d.Msg)
	}
	if got := envType(t, res.Env, "bad"); got != TError {
		t.Errorf("type of bad = %s, want <error>", got)
	}
}

// TestDeclTypeMismatch checks the declared-type vs initializer-type check.
func TestDeclTypeMismatch(t *testing.T) {
	res := check(t, "var q string = 42\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Pos != (Pos{Line: 1, Col: 1}) {
		t.Errorf("diagnostic position = %s, want 1:1", d.Pos)
	}
	if !strings.Contains(d.Msg, `cannot use int value as string in declaration of "q"`) {
		t.Errorf("unexpected message: %s", d.Msg)
	}
	// The declared type still wins in the environment.
	if got := envType(t, res.Env, "q"); got != TString {
		t.Errorf("type of q = %s, want string", got)
	}
}

func TestUndefinedVariable(t *testing.T) {
	res := check(t, "print(nope)\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	if res.Diagnostics[0].Pos != (Pos{Line: 1, Col: 7}) {
		t.Errorf("position = %s, want 1:7", res.Diagnostics[0].Pos)
	}
}

func TestNoCascadingErrors(t *testing.T) {
	// `oops` is undefined; the surrounding + and the var declaration must
	// not produce follow-on errors.
	res := check(t, "var a = oops + 1\nvar b = a * 2\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want exactly 1 diagnostic (no cascade), got %v", res.Diagnostics)
	}
}

func TestRedeclaration(t *testing.T) {
	res := check(t, "var x = 1\nvar x = 2\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Msg, `variable "x" redeclared`) {
		t.Errorf("unexpected message: %s", res.Diagnostics[0].Msg)
	}
}

func TestUnaryOperators(t *testing.T) {
	res := check(t, "var a = -1\nvar b = !true\nvar c = - 1.5\n")
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	if got := envType(t, res.Env, "a"); got != TInt {
		t.Errorf("type of a = %s, want int", got)
	}
	if got := envType(t, res.Env, "b"); got != TBool {
		t.Errorf("type of b = %s, want bool", got)
	}
	if got := envType(t, res.Env, "c"); got != TFloat {
		t.Errorf("type of c = %s, want float", got)
	}

	res = check(t, "var d = !1\n")
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %v", res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Msg, `cannot apply unary operator "!" to int`) {
		t.Errorf("unexpected message: %s", res.Diagnostics[0].Msg)
	}
}

func TestIntWidensToFloat(t *testing.T) {
	res := check(t, "var f float = 1\nvar g = f + 2\n")
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	if got := envType(t, res.Env, "g"); got != TFloat {
		t.Errorf("type of g = %s, want float", got)
	}
}

func TestOperatorTable(t *testing.T) {
	bad := []string{
		`var x = 1 - "s"`,
		`var x = true && 1`,
		`var x = 1 < "s"`,
		`var x = 1 == "s"`,
		`var x = 1.5 % 2.0`,
		`var x = "a" * "b"`,
	}
	for _, src := range bad {
		if res := check(t, src+"\n"); len(res.Diagnostics) != 1 {
			t.Errorf("%s: want 1 diagnostic, got %v", src, res.Diagnostics)
		}
	}
	good := []string{
		`var x = 1 == 1.5`,
		`var x = "a" < "b"`,
		`var x = 7 % 2`,
		`var x = 2.5 * 4`,
	}
	for _, src := range good {
		if res := check(t, src+"\n"); len(res.Diagnostics) != 0 {
			t.Errorf("%s: unexpected diagnostics %v", src, res.Diagnostics)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"var = 1\n",
		"var x int\n",
		"print(1\n",
		"1 +\n",
		"var x = 1 2\n",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("%q: expected parse error", src)
		}
	}
}
