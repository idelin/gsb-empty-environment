package main

import "testing"

func evalStr(t *testing.T, src string, assignment uint64, vars []string) bool {
	t.Helper()
	e, err := ParseExpr(src)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", src, err)
	}
	return e.Eval(assignment, vars)
}

func TestParseEval(t *testing.T) {
	vars := []string{"a", "b", "c"}
	cases := []struct {
		src        string
		assignment uint64 // bit0=a, bit1=b, bit2=c
		want       bool
	}{
		{"a", 1, true},
		{"a", 0, false},
		{"!a", 0, true},
		{"a & b", 3, true},
		{"a & b", 1, false},
		{"a | b", 2, true},
		{"a ^ b", 3, false},
		{"a ^ b", 1, true},
		{"a & (b | c)", 5, true},
		{"a & (b | c)", 1, false},
		{"(a & b) | (!a & c)", 4, true},
		{"a && !b || c", 4, true},
		{"not a and b", 2, true},
		{"a and b xor c", 7, false}, // (a&b)^c
		{"1", 0, true},
		{"0", 0, false},
		{"a | true", 0, true},
		{"a & false", 7, false},
	}
	for _, tc := range cases {
		if got := evalStr(t, tc.src, tc.assignment, vars); got != tc.want {
			t.Errorf("eval(%q, %03b) = %v, want %v", tc.src, tc.assignment, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"", "a &", "(a", "a )", "a b", "& a", "a $ b"} {
		if _, err := ParseExpr(src); err == nil {
			t.Errorf("ParseExpr(%q): expected error, got nil", src)
		}
	}
}

func TestSortedVars(t *testing.T) {
	ea, _ := ParseExpr("b & aa")
	eb, _ := ParseExpr("c | aa")
	got := SortedVars(ea, eb)
	want := []string{"aa", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("SortedVars = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedVars = %v, want %v", got, want)
		}
	}
}
