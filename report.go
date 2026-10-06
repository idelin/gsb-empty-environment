package retention

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the outcome of a retention run: the selected recovery
// points with their rationale and dependency chains, the extra points
// protected only because selected points depend on them, the final
// deletion set, and any recovery-graph integrity issues.
type Report struct {
	Selected  []SelectedPoint
	Protected []Point
	DeleteSet []Point
	Issues    []Issue
}

// BuildReport runs policy selection over the graph and derives the
// deletion set. The deletion set never contains a point that a selected
// recovery point depends on, even if that selected point is currently
// unusable — deletion is destructive, so retention errs on the safe
// side and surfaces the problem instead of silently dropping data.
func BuildReport(g *Graph, p Policy, now time.Time) *Report {
	selected := Select(g, p, now)

	ids := make([]string, 0, len(selected))
	selectedSet := make(map[string]bool, len(selected))
	for _, sp := range selected {
		ids = append(ids, sp.Point.ID)
		selectedSet[sp.Point.ID] = true
	}
	keep := g.Closure(ids)

	rep := &Report{Selected: selected, Issues: g.Validate()}
	for _, pt := range g.Points() {
		switch {
		case selectedSet[pt.ID]:
			// already in Selected
		case keep[pt.ID]:
			rep.Protected = append(rep.Protected, pt)
		default:
			rep.DeleteSet = append(rep.DeleteSet, pt)
		}
	}
	return rep
}

func formatChain(chain []Point) string {
	parts := make([]string, len(chain))
	for i, p := range chain {
		parts[i] = fmt.Sprintf("%s(%s)", p.ID, p.Type)
	}
	return strings.Join(parts, " -> ")
}

// String renders a human-readable retention report.
func (r *Report) String() string {
	var b strings.Builder

	b.WriteString("== Selected recovery points ==\n")
	if len(r.Selected) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, sp := range r.Selected {
		reasons := append([]string(nil), sp.Reasons...)
		sort.Strings(reasons)
		fmt.Fprintf(&b, "  %s  %s  type=%s\n", sp.Point.ID,
			sp.Point.Time.Format("2006-01-02 15:04"), sp.Point.Type)
		fmt.Fprintf(&b, "    reasons: %s\n", strings.Join(reasons, ", "))
		fmt.Fprintf(&b, "    chain:   %s\n", formatChain(sp.Chain))
		if sp.Usable {
			b.WriteString("    status:  usable\n")
		} else {
			b.WriteString("    status:  UNUSABLE\n")
			for _, prob := range sp.Problems {
				fmt.Fprintf(&b, "      - %s\n", prob)
			}
		}
	}

	b.WriteString("== Protected dependencies (kept, not selected) ==\n")
	if len(r.Protected) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, pt := range r.Protected {
		fmt.Fprintf(&b, "  %s  %s  type=%s\n", pt.ID,
			pt.Time.Format("2006-01-02 15:04"), pt.Type)
	}

	b.WriteString("== Delete set ==\n")
	if len(r.DeleteSet) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, pt := range r.DeleteSet {
		fmt.Fprintf(&b, "  %s  %s  type=%s\n", pt.ID,
			pt.Time.Format("2006-01-02 15:04"), pt.Type)
	}

	b.WriteString("== Recovery graph issues ==\n")
	if len(r.Issues) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, iss := range r.Issues {
		fmt.Fprintf(&b, "  %s\n", iss)
	}
	return b.String()
}
