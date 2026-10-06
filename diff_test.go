package linediff

import (
	"reflect"
	"testing"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"a",
		"a\n",
		"\n",
		"\n\n",
		"a\nb",
		"a\nb\n",
		"a\n\nb\n",
		"no newline at end\nbut newline in middle",
	}
	for _, s := range cases {
		if got := JoinLines(SplitLines(s)); got != s {
			t.Errorf("JoinLines(SplitLines(%q)) = %q", s, got)
		}
	}
	// Empty file vs. file holding one unterminated empty-ish line vs. one
	// newline-only line must stay distinct.
	if got := SplitLines(""); got != nil {
		t.Errorf("SplitLines(\"\") = %v, want nil", got)
	}
	if got := SplitLines("\n"); !reflect.DeepEqual(got, []string{"\n"}) {
		t.Errorf("SplitLines(\"\\n\") = %q", got)
	}
}

func TestDiffScripts(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want []Edit
	}{
		{"both empty", "", "", nil},
		{"empty to one line", "", "a\n", []Edit{{Insert, "a\n"}}},
		{"one line to empty", "a\n", "", []Edit{{Delete, "a\n"}}},
		{"identical", "a\nb\n", "a\nb\n", []Edit{{Keep, "a\n"}, {Keep, "b\n"}}},
		// Missing trailing newline is a real change: the two lines differ.
		{"trailing newline removed", "a\n", "a", []Edit{{Delete, "a\n"}, {Insert, "a"}}},
		{"trailing newline added", "a", "a\n", []Edit{{Delete, "a"}, {Insert, "a\n"}}},
		{"empty vs newline", "", "\n", []Edit{{Insert, "\n"}}},
		{"newline vs empty", "\n", "", []Edit{{Delete, "\n"}}},
		// Heavy duplication: keep the earliest old lines.
		{"dedup tail", "a\na\na\n", "a\n", []Edit{{Keep, "a\n"}, {Delete, "a\n"}, {Delete, "a\n"}}},
		{"dup tail", "a\n", "a\na\na\n", []Edit{{Keep, "a\n"}, {Insert, "a\n"}, {Insert, "a\n"}}},
		{"dup middle", "a\nb\na\n", "a\na\n", []Edit{{Keep, "a\n"}, {Delete, "b\n"}, {Keep, "a\n"}}},
		// Tie-break: prefer keeping the earlier old line.
		{"swap keeps earlier old", "A\nB\n", "B\nA\n",
			[]Edit{{Insert, "B\n"}, {Keep, "A\n"}, {Delete, "B\n"}}},
		{"swap reversed", "B\nA\n", "A\nB\n",
			[]Edit{{Insert, "A\n"}, {Keep, "B\n"}, {Delete, "A\n"}}},
		// Tie-break: among equal old choices, prefer the earlier new line.
		{"keep earlier new", "A\n", "A\nA\n", []Edit{{Keep, "A\n"}, {Insert, "A\n"}}},
		{"move to front", "X\nA\n", "A\nX\n",
			[]Edit{{Insert, "A\n"}, {Keep, "X\n"}, {Delete, "A\n"}}},
	}
	for _, c := range cases {
		oldLines, newLines := SplitLines(c.old), SplitLines(c.new)
		got := Diff(oldLines, newLines)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Diff(%q, %q) = %v, want %v", c.name, c.old, c.new, got, c.want)
			continue
		}
		out, err := ApplyString(c.old, got)
		if err != nil {
			t.Errorf("%s: ApplyString error: %v", c.name, err)
		} else if out != c.new {
			t.Errorf("%s: ApplyString(%q) = %q, want %q", c.name, c.old, out, c.new)
		}
	}
}

func TestApplyRejectsBadScripts(t *testing.T) {
	old := []string{"a\n", "b\n"}
	bad := [][]Edit{
		{{Keep, "x\n"}}, // wrong line content
		{{Keep, "a\n"}}, // does not consume all old lines
		{{Delete, "a\n"}, {Delete, "b\n"}, {Delete, "b\n"}}, // consumes too many
		{{Op(99), "a\n"}}, // unknown op
	}
	for i, script := range bad {
		if _, err := Apply(old, script); err == nil {
			t.Errorf("bad script %d: expected error", i)
		}
	}
}

// --- Exhaustive verification of the tie-breaking rules -----------------

// keptIndices returns the old-side and new-side indices of the Keep steps.
func keptIndices(edits []Edit) (oldIdx, newIdx []int) {
	i, j := 0, 0
	for _, e := range edits {
		switch e.Op {
		case Keep:
			oldIdx = append(oldIdx, i)
			newIdx = append(newIdx, j)
			i++
			j++
		case Delete:
			i++
		case Insert:
			j++
		}
	}
	return oldIdx, newIdx
}

func editCost(edits []Edit) int {
	c := 0
	for _, e := range edits {
		if e.Op != Keep {
			c++
		}
	}
	return c
}

func lessInts(a, b []int) bool {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}

// bestKept enumerates every shortest edit script between oldLines and
// newLines and returns the best kept-index pair under the library's rule:
// lexicographically smallest old indices first, then smallest new indices.
func bestKept(oldLines, newLines []string, dist int) (bestOld, bestNew []int) {
	n, m := len(oldLines), len(newLines)
	var curOld, curNew []int
	found := false
	var rec func(i, j, cost int)
	rec = func(i, j, cost int) {
		if cost > dist {
			return
		}
		// Cheapest possible completion needs at least this many indels.
		rem := (n - i) - (m - j)
		if rem < 0 {
			rem = -rem
		}
		if cost+rem > dist {
			return
		}
		if i == n && j == m {
			if cost == dist && (!found ||
				lessInts(curOld, bestOld) ||
				(reflect.DeepEqual(curOld, bestOld) && lessInts(curNew, bestNew))) {
				found = true
				bestOld = append([]int(nil), curOld...)
				bestNew = append([]int(nil), curNew...)
			}
			return
		}
		if i < n && j < m && oldLines[i] == newLines[j] {
			curOld = append(curOld, i)
			curNew = append(curNew, j)
			rec(i+1, j+1, cost)
			curOld = curOld[:len(curOld)-1]
			curNew = curNew[:len(curNew)-1]
		}
		if i < n {
			rec(i+1, j, cost+1) // delete oldLines[i]
		}
		if j < m {
			rec(i, j+1, cost+1) // insert newLines[j]
		}
	}
	rec(0, 0, 0)
	return bestOld, bestNew
}

func allSequences(alphabet []string, maxLen int) [][]string {
	var out [][]string
	var rec func(cur []string)
	rec = func(cur []string) {
		out = append(out, append([]string(nil), cur...))
		if len(cur) == maxLen {
			return
		}
		for _, s := range alphabet {
			rec(append(cur, s))
		}
	}
	rec(nil)
	return out
}

func checkPair(t *testing.T, oldLines, newLines []string) {
	script := Diff(oldLines, newLines)

	// The applier must reproduce the new side exactly.
	out, err := Apply(oldLines, script)
	if err != nil {
		t.Fatalf("Diff(%q, %q): apply error: %v", oldLines, newLines, err)
	}
	if !reflect.DeepEqual(out, newLines) && !(len(out) == 0 && len(newLines) == 0) {
		t.Fatalf("Diff(%q, %q): applied to %q, want %q", oldLines, newLines, out, newLines)
	}

	// The script must be shortest.
	dist := EditDistance(oldLines, newLines)
	if c := editCost(script); c != dist {
		t.Fatalf("Diff(%q, %q): cost %d, want minimal %d", oldLines, newLines, c, dist)
	}

	// Its kept lines must be the best among all shortest scripts.
	wantOld, wantNew := bestKept(oldLines, newLines, dist)
	gotOld, gotNew := keptIndices(script)
	if !reflect.DeepEqual(gotOld, wantOld) || !reflect.DeepEqual(gotNew, wantNew) {
		t.Fatalf("Diff(%q, %q): kept old=%v new=%v, want old=%v new=%v",
			oldLines, newLines, gotOld, gotNew, wantOld, wantNew)
	}
}

func TestExhaustiveShortSequences(t *testing.T) {
	sets := []struct {
		alphabet []string
		maxLen   int
	}{
		{[]string{"a", "b"}, 6},
		{[]string{"a", "b", "c"}, 4},
		{[]string{"a", "a\n"}, 4}, // newline-terminated vs. bare lines
	}
	for _, s := range sets {
		seqs := allSequences(s.alphabet, s.maxLen)
		for _, oldLines := range seqs {
			for _, newLines := range seqs {
				checkPair(t, oldLines, newLines)
			}
		}
	}
}
