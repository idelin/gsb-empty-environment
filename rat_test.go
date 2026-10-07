package timemap

import (
	"encoding/json"
	"testing"
)

func mustRat(t *testing.T, s string) Rat {
	t.Helper()
	r, err := ParseRat(s)
	if err != nil {
		t.Fatalf("ParseRat(%q): %v", s, err)
	}
	return r
}

func TestParseRat(t *testing.T) {
	cases := map[string]string{
		"3/2":  "3/2",
		"1.25": "5/4",
		"7":    "7",
		"-0.5": "-1/2",
		"2/4":  "1/2",
	}
	for in, want := range cases {
		got := mustRat(t, in)
		if got.String() != want {
			t.Errorf("ParseRat(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRatJSON(t *testing.T) {
	var r Rat
	if err := json.Unmarshal([]byte(`"1/30"`), &r); err != nil {
		t.Fatal(err)
	}
	if r.String() != "1/30" {
		t.Errorf("got %s", r)
	}
	if err := json.Unmarshal([]byte(`2.5`), &r); err != nil {
		t.Fatal(err)
	}
	if r.String() != "5/2" {
		t.Errorf("got %s", r)
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1/2", 0},   // tie -> even 0
		{"3/2", 2},   // tie -> even 2
		{"5/2", 2},   // tie -> even 2
		{"7/2", 4},   // tie -> even 4
		{"-1/2", 0},  // tie -> even 0
		{"-3/2", -2}, // tie -> even -2
		{"2.4", 2},
		{"2.6", 3},
		{"-2.6", -3},
		{"10", 10},
	}
	for _, c := range cases {
		got := RoundHalfEven(mustRat(t, c.in))
		if got.Int64() != c.want {
			t.Errorf("RoundHalfEven(%s) = %s, want %d", c.in, got, c.want)
		}
	}
}
