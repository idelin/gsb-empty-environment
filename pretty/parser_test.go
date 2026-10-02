package pretty

import (
	"reflect"
	"testing"
)

func TestParsePrecedence(t *testing.T) {
	// 1 + 2 * 3 must parse as 1 + (2 * 3).
	e, err := Parse("1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	want := &Binary{Op: "+", X: &Number{Value: "1"},
		Y: &Binary{Op: "*", X: &Number{Value: "2"}, Y: &Number{Value: "3"}}}
	if !reflect.DeepEqual(e, want) {
		t.Fatalf("got %#v, want %#v", e, want)
	}
}

func TestParseCallAndUnary(t *testing.T) {
	e, err := Parse(`-f(a, "hi")(b) && !ok`)
	if err != nil {
		t.Fatal(err)
	}
	want := &Binary{Op: "&&",
		X: &Unary{Op: "-", X: &Call{
			Func: &Call{Func: &Ident{Name: "f"},
				Args: []Expr{&Ident{Name: "a"}, &String{Value: "hi"}}},
			Args: []Expr{&Ident{Name: "b"}}}},
		Y: &Unary{Op: "!", X: &Ident{Name: "ok"}}}
	if !reflect.DeepEqual(e, want) {
		t.Fatalf("got %#v, want %#v", e, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"1 +", "(a", "f(a,", "a b", "@", `"unterminated`} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", src)
		}
	}
}
