package diff

import (
	"reflect"
	"testing"
)

func TestDiffEmpty(t *testing.T) {
	if got := Diff(nil, nil); len(got) != 0 {
		t.Fatalf("Diff(nil, nil) = %v, want empty", got)
	}
	want := []Edit{{Op: Insert, Line: "a\n"}}
	if got := Diff(nil, []string{"a\n"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff(nil, [a]) = %v, want %v", got, want)
	}
	want = []Edit{{Op: Delete, Line: "a\n"}}
	if got := Diff([]string{"a\n"}, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff([a], nil) = %v, want %v", got, want)
	}
}

func TestDiffTieBreakPrefersEarlierOldLines(t *testing.T) {
	// Both shortest scripts keep one line; keeping old line 0 wins.
	old := []string{"x", "a"}
	new := []string{"a", "x"}
	want := []Edit{
		{Op: Insert, Line: "a"},
		{Op: Keep, Line: "x"},
		{Op: Delete, Line: "a"},
	}
	if got := Diff(old, new); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff(%v, %v) = %v, want %v", old, new, got, want)
	}

	old = []string{"a", "x"}
	new = []string{"x", "a"}
	want = []Edit{
		{Op: Insert, Line: "x"},
		{Op: Keep, Line: "a"},
		{Op: Delete, Line: "x"},
	}
	if got := Diff(old, new); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff(%v, %v) = %v, want %v", old, new, got, want)
	}
}

func TestDiffTieBreakPrefersEarlierNewLines(t *testing.T) {
	// The single old "a" can match either new "a"; the earlier one wins.
	old := []string{"a"}
	new := []string{"a", "a"}
	want := []Edit{
		{Op: Keep, Line: "a"},
		{Op: Insert, Line: "a"},
	}
	if got := Diff(old, new); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff(%v, %v) = %v, want %v", old, new, got, want)
	}
}

func TestDiffRepeatedLines(t *testing.T) {
	// With heavy repetition the earliest old lines are kept, so the
	// deletion lands on the last copy.
	old := []string{"a\n", "a\n", "a\n"}
	new := []string{"a\n", "a\n"}
	want := []Edit{
		{Op: Keep, Line: "a\n"},
		{Op: Keep, Line: "a\n"},
		{Op: Delete, Line: "a\n"},
	}
	if got := Diff(old, new); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff(%v, %v) = %v, want %v", old, new, got, want)
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"\n", []string{"\n"}},
		{"a", []string{"a"}},
		{"a\n", []string{"a\n"}},
		{"a\nb", []string{"a\n", "b"}},
		{"a\nb\n", []string{"a\n", "b\n"}},
		{"a\n\nb\n", []string{"a\n", "\n", "b\n"}},
	}
	for _, c := range cases {
		if got := SplitLines(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := JoinLines(c.want); got != c.in {
			t.Errorf("JoinLines(SplitLines(%q)) = %q", c.in, got)
		}
	}
}

func TestDiffTextMissingTrailingNewline(t *testing.T) {
	// "a" and "a\n" are different lines, so the change is a
	// delete+insert pair, and applying it restores the bytes exactly.
	want := []Edit{
		{Op: Delete, Line: "a"},
		{Op: Insert, Line: "a\n"},
	}
	script := DiffText("a", "a\n")
	if !reflect.DeepEqual(script, want) {
		t.Fatalf(`DiffText("a", "a\n") = %v, want %v`, script, want)
	}
	out, err := Apply(SplitLines("a"), script)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := JoinLines(out); got != "a\n" {
		t.Fatalf("round trip = %q, want %q", got, "a\n")
	}

	// Empty file vs. a single newline-only line stay distinct.
	script = DiffText("", "\n")
	want = []Edit{{Op: Insert, Line: "\n"}}
	if !reflect.DeepEqual(script, want) {
		t.Fatalf(`DiffText("", "\n") = %v, want %v`, script, want)
	}
	out, err = Apply(SplitLines(""), script)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := JoinLines(out); got != "\n" {
		t.Fatalf("round trip = %q, want %q", got, "\n")
	}
}

func TestApplyRoundTrip(t *testing.T) {
	pairs := [][2]string{
		{"", ""},
		{"", "a\nb\n"},
		{"a\nb\n", ""},
		{"a\nb\nc\n", "a\nc\n"},
		{"one\ntwo\nthree", "one\nTWO\nthree\n"},
		{"x\nx\nx\ny\n", "y\nx\nx\n"},
		{"no newline", "no newline"},
		{"no newline", "no newline\n"},
	}
	for _, p := range pairs {
		oldLines, newLines := SplitLines(p[0]), SplitLines(p[1])
		script := Diff(oldLines, newLines)
		out, err := Apply(oldLines, script)
		if err != nil {
			t.Errorf("Apply(%q -> %q): %v", p[0], p[1], err)
			continue
		}
		if got := JoinLines(out); got != p[1] {
			t.Errorf("round trip %q -> %q: got %q", p[0], p[1], got)
		}
	}
}

func TestApplyRejectsBadScripts(t *testing.T) {
	old := []string{"a\n", "b\n"}
	cases := []struct {
		name   string
		script []Edit
	}{
		{"keep mismatch", []Edit{{Op: Keep, Line: "z\n"}}},
		{"delete mismatch", []Edit{{Op: Delete, Line: "z\n"}}},
		{"old exhausted", []Edit{{Op: Keep, Line: "a\n"}, {Op: Keep, Line: "b\n"}, {Op: Delete, Line: "c\n"}}},
		{"old left over", []Edit{{Op: Keep, Line: "a\n"}}},
		{"unknown op", []Edit{{Op: Op(42), Line: "a\n"}}},
	}
	for _, c := range cases {
		if _, err := Apply(old, c.script); err == nil {
			t.Errorf("%s: Apply(%v) succeeded, want error", c.name, c.script)
		}
	}
}
