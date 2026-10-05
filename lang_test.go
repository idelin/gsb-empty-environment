package main

import "testing"

func runSrc(t *testing.T, src string, inputs map[string]int8) int8 {
	t.Helper()
	prog, err := parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	v, err := run(prog, inputs)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	return v
}

func TestEvalArithmetic(t *testing.T) {
	got := runSrc(t, "input a, b\ns = a + b\nd = a - b\nreturn s + d\n",
		map[string]int8{"a": 7, "b": 3})
	if got != 14 { // (7+3) + (7-3)
		t.Fatalf("got %d, want 14", got)
	}
}

func TestOverflowWraps(t *testing.T) {
	cases := []struct {
		src   string
		input int8
		want  int8
	}{
		{"input a\nreturn a + 1\n", 127, -128},  // max + 1 wraps to min
		{"input a\nreturn a - 1\n", -128, 127},  // min - 1 wraps to max
		{"input a\nreturn a + a\n", 100, -56},   // 200 wraps to -56
		{"input a\nreturn 0 - a\n", -128, -128}, // negating min wraps to itself
	}
	for _, c := range cases {
		if got := runSrc(t, c.src, map[string]int8{"a": c.input}); got != c.want {
			t.Errorf("%s with a=%d: got %d, want %d", c.src, c.input, got, c.want)
		}
	}
}

func TestComparisonsAndBranch(t *testing.T) {
	src := `input a, b
m = a
if a < b
  m = b
end
return m
`
	if got := runSrc(t, src, map[string]int8{"a": 3, "b": 9}); got != 9 {
		t.Fatalf("max(3,9): got %d", got)
	}
	if got := runSrc(t, src, map[string]int8{"a": 9, "b": 3}); got != 9 {
		t.Fatalf("max(9,3): got %d", got)
	}
}

func TestIfElseAndComparisonValue(t *testing.T) {
	src := `input a
if a >= 0
  return 1
else
  return 0 - 1
end
`
	if got := runSrc(t, src, map[string]int8{"a": 5}); got != 1 {
		t.Fatalf("sign(5): got %d", got)
	}
	if got := runSrc(t, src, map[string]int8{"a": -5}); got != -1 {
		t.Fatalf("sign(-5): got %d", got)
	}
}

func TestNegativeLiteral(t *testing.T) {
	if got := runSrc(t, "return -128\n", nil); got != -128 {
		t.Fatalf("got %d, want -128", got)
	}
	if got := runSrc(t, "input a\nreturn a + -128\n", map[string]int8{"a": 1}); got != -127 {
		t.Fatalf("got %d, want -127", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"input a\nreturn a + 129\n",  // literal out of range
		"input a\nif a\n return 1\n", // missing end
		"input a\nreturn b\n",        // parses, but b undefined at run time
		"return\n",                   // missing expression
	} {
		prog, err := parse(src)
		if err != nil {
			continue // parse-time error: fine
		}
		if _, err := run(prog, map[string]int8{"a": 1}); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}
