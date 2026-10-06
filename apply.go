package linediff

import "fmt"

// Apply applies an edit script to oldLines and returns the resulting lines.
// It verifies that every Keep and Delete matches the actual old line at that
// position and that the script consumes oldLines exactly; any mismatch is
// reported as an error instead of silently producing wrong output.
func Apply(oldLines []string, edits []Edit) ([]string, error) {
	out := make([]string, 0, len(oldLines))
	idx := 0
	for k, e := range edits {
		switch e.Op {
		case Keep, Delete:
			if idx >= len(oldLines) {
				return nil, fmt.Errorf("edit %d (%s %q): old lines exhausted", k, e.Op, e.Line)
			}
			if oldLines[idx] != e.Line {
				return nil, fmt.Errorf("edit %d (%s %q): old line %d is %q", k, e.Op, e.Line, idx, oldLines[idx])
			}
			if e.Op == Keep {
				out = append(out, e.Line)
			}
			idx++
		case Insert:
			out = append(out, e.Line)
		default:
			return nil, fmt.Errorf("edit %d: unknown op %d", k, int(e.Op))
		}
	}
	if idx != len(oldLines) {
		return nil, fmt.Errorf("script consumed %d of %d old lines", idx, len(oldLines))
	}
	return out, nil
}

// ApplyString is a convenience wrapper: it splits oldText, applies the
// script, and joins the result. Combined with Diff it guarantees exact
// reconstruction, including the empty-file and missing-final-newline cases.
func ApplyString(oldText string, edits []Edit) (string, error) {
	out, err := Apply(SplitLines(oldText), edits)
	if err != nil {
		return "", err
	}
	return JoinLines(out), nil
}
