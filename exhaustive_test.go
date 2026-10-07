package diff

import (
	"fmt"
	"reflect"
	"testing"
)

// lcsLength is an independent (memoized recursive) LCS implementation
// used only by the tests.
func lcsLength(oldLines, newLines []string) [][]int {
	n, m := len(oldLines), len(newLines)
	memo := make([][]int, n+1)
	for i := range memo {
		memo[i] = make([]int, m+1)
		for j := range memo[i] {
			memo[i][j] = -1
		}
	}
	var solve func(i, j int) int
	solve = func(i, j int) int {
		if i == n || j == m {
			return 0
		}
		if memo[i][j] >= 0 {
			return memo[i][j]
		}
		best := solve(i+1, j)
		if v := solve(i, j+1); v > best {
			best = v
		}
		if oldLines[i] == newLines[j] {
			if v := solve(i+1, j+1) + 1; v > best {
				best = v
			}
		}
		memo[i][j] = best
		return best
	}
	out := make([][]int, n+1)
	for i := range out {
		out[i] = make([]int, m+1)
		for j := range out[i] {
			out[i][j] = solve(i, j)
		}
	}
	return out
}

// keptIndices returns the old-line and new-line indices a script keeps.
func keptIndices(script []Edit) (keptOld, keptNew []int) {
	i, j := 0, 0
	for _, e := range script {
		switch e.Op {
		case Keep:
			keptOld = append(keptOld, i)
			keptNew = append(keptNew, j)
			i++
			j++
		case Delete:
			i++
		case Insert:
			j++
		}
	}
	return keptOld, keptNew
}

func lexCompare(a, b []int) int {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			if a[k] < b[k] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// compareScripts orders scripts by the documented tie-break rules:
// fewer delete+insert edits first, then earlier kept old lines, then
// earlier kept new lines. It returns 0 for scripts the rules cannot
// tell apart.
func compareScripts(a, b []Edit) int {
	editsA, editsB := 0, 0
	for _, e := range a {
		if e.Op != Keep {
			editsA++
		}
	}
	for _, e := range b {
		if e.Op != Keep {
			editsB++
		}
	}
	if editsA != editsB {
		if editsA < editsB {
			return -1
		}
		return 1
	}
	oldA, newA := keptIndices(a)
	oldB, newB := keptIndices(b)
	if c := lexCompare(oldA, oldB); c != 0 {
		return c
	}
	return lexCompare(newA, newB)
}

// bestScriptByEnumeration enumerates every shortest edit script turning
// oldLines into newLines and returns a winner under the tie-break rules.
// It is exponential and only meant for short test inputs.
func bestScriptByEnumeration(oldLines, newLines []string) []Edit {
	lcs := lcsLength(oldLines, newLines)
	n, m := len(oldLines), len(newLines)
	var best []Edit
	var cur []Edit
	var dfs func(i, j int)
	dfs = func(i, j int) {
		if i == n && j == m {
			if best == nil || compareScripts(cur, best) < 0 {
				best = append([]Edit(nil), cur...)
			}
			return
		}
		if i < n && j < m && oldLines[i] == newLines[j] && lcs[i][j] == lcs[i+1][j+1]+1 {
			cur = append(cur, Edit{Op: Keep, Line: oldLines[i]})
			dfs(i+1, j+1)
			cur = cur[:len(cur)-1]
		}
		if j < m && lcs[i][j+1] == lcs[i][j] {
			cur = append(cur, Edit{Op: Insert, Line: newLines[j]})
			dfs(i, j+1)
			cur = cur[:len(cur)-1]
		}
		if i < n && lcs[i+1][j] == lcs[i][j] {
			cur = append(cur, Edit{Op: Delete, Line: oldLines[i]})
			dfs(i+1, j)
			cur = cur[:len(cur)-1]
		}
	}
	dfs(0, 0)
	return best
}

// allSequences yields every sequence over alphabet with length up to
// maxLen, including the empty sequence.
func allSequences(alphabet []string, maxLen int) [][]string {
	var out [][]string
	var build func(prefix []string)
	build = func(prefix []string) {
		out = append(out, append([]string(nil), prefix...))
		if len(prefix) == maxLen {
			return
		}
		for _, s := range alphabet {
			build(append(prefix, s))
		}
	}
	build(nil)
	return out
}

func TestDiffMatchesExhaustiveEnumeration(t *testing.T) {
	alphabets := []struct {
		symbols []string
		maxLen  int
	}{
		{[]string{"a", "b"}, 6},
		{[]string{"a", "b", "c"}, 5},
	}
	for _, ab := range alphabets {
		seqs := allSequences(ab.symbols, ab.maxLen)
		for _, oldLines := range seqs {
			for _, newLines := range seqs {
				label := fmt.Sprintf("alphabet=%d old=%q new=%q", len(ab.symbols), oldLines, newLines)

				got := Diff(oldLines, newLines)
				want := bestScriptByEnumeration(oldLines, newLines)
				if c := compareScripts(got, want); c != 0 {
					t.Fatalf("%s: Diff = %v loses to enumeration best %v (compare=%d)", label, got, want, c)
				}

				// The script is minimal: delete+insert count equals
				// len(old)+len(new)-2*LCS.
				lcs := lcsLength(oldLines, newLines)
				edits := 0
				for _, e := range got {
					if e.Op != Keep {
						edits++
					}
				}
				if wantEdits := len(oldLines) + len(newLines) - 2*lcs[0][0]; edits != wantEdits {
					t.Fatalf("%s: %d edits, want minimal %d", label, edits, wantEdits)
				}

				// Applying the script to old reproduces new exactly.
				out, err := Apply(oldLines, got)
				if err != nil {
					t.Fatalf("%s: Apply: %v", label, err)
				}
				if !reflect.DeepEqual(out, newLines) && (len(out) != 0 || len(newLines) != 0) {
					t.Fatalf("%s: Apply produced %q", label, out)
				}
			}
		}
	}
}
