package main

import (
	"fmt"
	"strings"
)

// Reserved device names (Windows-style). Compared case-insensitively on the
// folded base name (the part before the first dot).
var reservedNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"con", "prn", "aux", "nul"} {
		m[n] = true
	}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("com%d", i)] = true
		m[fmt.Sprintf("lpt%d", i)] = true
	}
	return m
}()

// Segment is the normalization result for one path segment.
type Segment struct {
	Orig     string
	Folded   string // after table folding and trailing space/dot stripping
	Reserved bool
}

// NormalizeSegment folds a single path segment, then applies the trailing
// space/dot rule (trailing ' ' and '.' are stripped, Windows-style), then
// checks the result against the reserved-name set.
func NormalizeSegment(t *FoldTable, seg string) Segment {
	folded := strings.TrimRight(t.Fold(seg), " .")
	base := folded
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return Segment{
		Orig:     seg,
		Folded:   folded,
		Reserved: reservedNames[strings.ToLower(base)],
	}
}
