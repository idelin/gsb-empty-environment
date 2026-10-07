// Package diff computes minimal line-based edit scripts.
//
// A script turns one list of lines into another using keep, delete and
// insert steps. Scripts produced by Diff are shortest possible (fewest
// delete+insert steps, i.e. they keep an LCS of the two inputs). Among
// all shortest scripts Diff prefers keeping earlier old lines; ties that
// remain are broken by preferring earlier new lines.
package diff

// Op is the kind of a single edit step.
type Op int

const (
	// Keep copies the current old line to the output unchanged.
	Keep Op = iota
	// Delete drops the current old line.
	Delete
	// Insert emits a new line that has no old counterpart.
	Insert
)

func (op Op) String() string {
	switch op {
	case Keep:
		return "keep"
	case Delete:
		return "delete"
	case Insert:
		return "insert"
	}
	return "unknown"
}

// Edit is one step of an edit script. Line is the kept, deleted or
// inserted line, including its trailing "\n" when it has one.
type Edit struct {
	Op   Op
	Line string
}

// Diff returns a shortest edit script turning oldLines into newLines.
//
// Among all shortest scripts it applies two tie-break rules, in order:
//
//  1. keep earlier old lines (formally: the list of kept old-line
//     indices is lexicographically minimal);
//  2. then keep earlier new lines (the list of kept new-line indices is
//     lexicographically minimal among scripts satisfying rule 1).
//
// The algorithm is an LCS dynamic program followed by a greedy walk.
// Equal lines are always kept: some shortest script keeps the old line,
// and matching it with the earliest available new line is best for both
// rules. Otherwise at least one of insert/delete stays on a shortest
// path. When both do, the old line is inserted around (kept available)
// exactly when some shortest script can still keep it; if no shortest
// script can keep it, deleting it is at least as good and gives the
// current new line a chance to be kept, serving rule 2.
//
// It uses O(len(oldLines)*len(newLines)) time and memory.
func Diff(oldLines, newLines []string) []Edit {
	n, m := len(oldLines), len(newLines)

	// lcs[i][j] is the LCS length of oldLines[i:] and newLines[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	// keepable[i][j] reports whether some shortest script for
	// oldLines[i:], newLines[j:] keeps oldLines[i], given that inserting
	// newLines[j] also stays on a shortest path. Scanning new lines from
	// j+1 rightward, oldLines[i] stays keepable while lines differ from
	// it and insertion stays optimal; it is keepable exactly when the
	// scan reaches a line equal to it.
	keepable := make([][]bool, n)
	for i := 0; i < n; i++ {
		keepable[i] = make([]bool, m)
		for j := m - 1; j >= 0; j-- {
			k := j + 1
			switch {
			case k >= m:
				keepable[i][j] = false
			case newLines[k] == oldLines[i]:
				keepable[i][j] = true
			case lcs[i][k+1] != lcs[i][k]:
				keepable[i][j] = false
			default:
				keepable[i][j] = keepable[i][j+1]
			}
		}
	}

	var script []Edit
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && oldLines[i] == newLines[j]:
			script = append(script, Edit{Op: Keep, Line: oldLines[i]})
			i++
			j++
		default:
			insertOK := j < m && lcs[i][j+1] == lcs[i][j]
			deleteOK := i < n && lcs[i+1][j] == lcs[i][j]
			if insertOK && (!deleteOK || keepable[i][j]) {
				script = append(script, Edit{Op: Insert, Line: newLines[j]})
				j++
			} else {
				script = append(script, Edit{Op: Delete, Line: oldLines[i]})
				i++
			}
		}
	}
	return script
}
