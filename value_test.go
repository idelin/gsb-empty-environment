package netsec

import "testing"

func TestNot(t *testing.T) {
	cases := []struct {
		in, want Value
	}{
		{Pass, Fail},
		{Fail, Pass},
		{Unknown, Unknown},
	}
	for _, tc := range cases {
		if got := Not(tc.in); got != tc.want {
			t.Errorf("Not(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAndTruthTable(t *testing.T) {
	values := []Value{Fail, Unknown, Pass}
	want := [3][3]Value{
		{Fail, Fail, Fail},
		{Fail, Unknown, Unknown},
		{Fail, Unknown, Pass},
	}
	for i, left := range values {
		for j, right := range values {
			if got := And(left, right); got != want[i][j] {
				t.Errorf("And(%v, %v) = %v, want %v", left, right, got, want[i][j])
			}
		}
	}
}

func TestOrTruthTable(t *testing.T) {
	values := []Value{Fail, Unknown, Pass}
	want := [3][3]Value{
		{Fail, Unknown, Pass},
		{Unknown, Unknown, Pass},
		{Pass, Pass, Pass},
	}
	for i, left := range values {
		for j, right := range values {
			if got := Or(left, right); got != want[i][j] {
				t.Errorf("Or(%v, %v) = %v, want %v", left, right, got, want[i][j])
			}
		}
	}
}

func TestValueStringAndParse(t *testing.T) {
	for _, v := range []Value{Pass, Fail, Unknown} {
		if got := ParseValue(v.String()); got != v {
			t.Errorf("ParseValue(%q) = %v, want %v", v.String(), got, v)
		}
	}
	if ParseValue("nonsense") != Unknown {
		t.Error("ParseValue should fall back to Unknown")
	}
	if (Value(42)).String() != "unknown" {
		t.Error("unexpected Value should render as unknown")
	}
}

func TestDecisionMapping(t *testing.T) {
	cases := []struct {
		v    Value
		want Decision
	}{
		{Fail, Reject},
		{Unknown, Pending},
		{Pass, Allow},
	}
	for _, tc := range cases {
		if got := Decide(tc.v); got != tc.want {
			t.Errorf("Decide(%v) = %v, want %v", tc.v, got, tc.want)
		}
	}
	if Reject.String() != "reject" || Pending.String() != "pending" || Allow.String() != "allow" {
		t.Fatal("decision string labels changed")
	}
}
