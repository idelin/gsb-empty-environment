package diff

import "fmt"

// Apply replays script against oldLines and returns the resulting lines.
//
// Every keep and delete step is checked against the next unconsumed old
// line, and the script must consume oldLines exactly; any mismatch is
// reported as an error. Applying the script returned by Diff(old, new)
// to old therefore reproduces new exactly.
func Apply(oldLines []string, script []Edit) ([]string, error) {
	var out []string
	i := 0
	for k, e := range script {
		switch e.Op {
		case Keep, Delete:
			if i >= len(oldLines) {
				return nil, fmt.Errorf("edit %d (%s %q): old lines exhausted", k, e.Op, e.Line)
			}
			if oldLines[i] != e.Line {
				return nil, fmt.Errorf("edit %d (%s %q): old line %d is %q", k, e.Op, e.Line, i, oldLines[i])
			}
			if e.Op == Keep {
				out = append(out, e.Line)
			}
			i++
		case Insert:
			out = append(out, e.Line)
		default:
			return nil, fmt.Errorf("edit %d: unknown op %d", k, int(e.Op))
		}
	}
	if i != len(oldLines) {
		return nil, fmt.Errorf("script consumed %d of %d old lines", i, len(oldLines))
	}
	return out, nil
}
