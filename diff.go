package linediff

// Op is the kind of a single edit step.
type Op int

const (
	// Keep consumes one old line and copies it to the output.
	Keep Op = iota
	// Delete consumes one old line without emitting it.
	Delete
	// Insert emits one new line without consuming an old line.
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

// Edit is one step of an edit script. Line is the content of the affected
// line (including its trailing "\n", if any): the consumed old line for
// Keep and Delete, the emitted line for Insert.
type Edit struct {
	Op   Op
	Line string
}

// Diff returns an edit script transforming oldLines into newLines with the
// fewest possible Insert+Delete operations (equivalently, a longest common
// subsequence alignment). Among all shortest scripts it is the one that
// keeps the earliest possible old lines; remaining ties are broken by
// keeping the earliest possible new lines.
//
// Formally: the kept (matched) old-line indices are lexicographically
// minimal across all longest common subsequences, and among those, the
// kept new-line indices are lexicographically minimal too. The walk below
// implements exactly that: each old line is kept whenever some shortest
// completion can still keep it, and it is matched to the earliest new-line
// position that keeps a longest completion reachable.
func Diff(oldLines, newLines []string) []Edit {
	n, m := len(oldLines), len(newLines)

	// lcs[i][j] = LCS length of oldLines[i:] and newLines[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var edits []Edit
	i, j := 0, 0
	for i < n && j < m {
		// Earliest new position that old line i can match while a longest
		// completion stays reachable. Keeping old line i at all is always
		// preferred (earlier old lines stay), and the earliest matchable
		// position keeps the earliest new line while leaving the largest
		// possible remainder for the rest of the walk.
		match := -1
		for k := j; k < m; k++ {
			if newLines[k] == oldLines[i] && lcs[i][j] == 1+lcs[i+1][k+1] {
				match = k
				break
			}
		}
		if match < 0 {
			edits = append(edits, Edit{Delete, oldLines[i]})
			i++
			continue
		}
		for ; j < match; j++ {
			edits = append(edits, Edit{Insert, newLines[j]})
		}
		edits = append(edits, Edit{Keep, oldLines[i]})
		i++
		j++
	}
	for ; i < n; i++ {
		edits = append(edits, Edit{Delete, oldLines[i]})
	}
	for ; j < m; j++ {
		edits = append(edits, Edit{Insert, newLines[j]})
	}
	return edits
}

// EditDistance returns the number of Insert+Delete operations in the
// shortest script, i.e. len(oldLines)+len(newLines)-2*LCS.
func EditDistance(oldLines, newLines []string) int {
	n, m := len(oldLines), len(newLines)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			if oldLines[i-1] == newLines[j-1] {
				cur[j] = prev[j-1]
			} else {
				cur[j] = prev[j] + 1
				if cur[j-1]+1 < cur[j] {
					cur[j] = cur[j-1] + 1
				}
			}
		}
		prev, cur = cur, prev
	}
	return prev[m]
}
