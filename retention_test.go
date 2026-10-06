package retention

import (
	"strings"
	"testing"
	"time"
)

// buildCatalog creates weeks*7 daily backups starting at start: a full
// baseline on day 0 of each week and incrementals on the other days,
// each incremental depending on the previous day's backup.
func buildCatalog(start time.Time, weeks int) []Point {
	var pts []Point
	var prevID string
	for w := 0; w < weeks; w++ {
		for d := 0; d < 7; d++ {
			t := start.AddDate(0, 0, w*7+d)
			if d == 0 {
				id := fullID(t)
				pts = append(pts, Point{ID: id, Time: t, Type: Full})
				prevID = id
			} else {
				id := incrID(t)
				pts = append(pts, Point{ID: id, Time: t, Type: Incremental,
					DependsOn: []string{prevID}})
				prevID = id
			}
		}
	}
	return pts
}

func fullID(t time.Time) string { return "full-" + t.Format("2006-01-02") }
func incrID(t time.Time) string { return "incr-" + t.Format("2006-01-02") }

func mustGraph(t *testing.T, pts []Point) *Graph {
	t.Helper()
	g, err := NewGraph(pts)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func findSelected(rep *Report, id string) *SelectedPoint {
	for i := range rep.Selected {
		if rep.Selected[i].Point.ID == id {
			return &rep.Selected[i]
		}
	}
	return nil
}

func inDeleteSet(rep *Report, id string) bool {
	for _, p := range rep.DeleteSet {
		if p.ID == id {
			return true
		}
	}
	return false
}

func TestSelectAssignsReasonsAndChains(t *testing.T) {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC) // a Monday
	now := start.AddDate(0, 0, 77)                       // one day past the 11-week catalog
	g := mustGraph(t, buildCatalog(start, 11))

	rep := BuildReport(g, Policy{Daily: 7, Weekly: 4, Monthly: 2}, now)
	if len(rep.Selected) == 0 {
		t.Fatal("expected selected points")
	}
	for _, sp := range rep.Selected {
		if len(sp.Reasons) == 0 {
			t.Errorf("%s: no selection reasons", sp.Point.ID)
		}
		if len(sp.Chain) == 0 {
			t.Fatalf("%s: empty chain", sp.Point.ID)
		}
		if head := sp.Chain[0]; head.Type != Full {
			t.Errorf("%s: chain head %s is %s, want full", sp.Point.ID, head.ID, head.Type)
		}
		if tail := sp.Chain[len(sp.Chain)-1]; tail.ID != sp.Point.ID {
			t.Errorf("%s: chain tail is %s", sp.Point.ID, tail.ID)
		}
	}

	// The most recent day must be retained by the daily rule.
	latest := g.Points()[len(g.Points())-1]
	sp := findSelected(rep, latest.ID)
	if sp == nil {
		t.Fatalf("latest point %s not selected", latest.ID)
	}
	wantReason := "daily:" + latest.Time.Format("2006-01-02")
	found := false
	for _, r := range sp.Reasons {
		if r == wantReason {
			found = true
		}
	}
	if !found {
		t.Errorf("latest point reasons %v, want %q", sp.Reasons, wantReason)
	}
}

func TestDeleteSetPreservesDependencies(t *testing.T) {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, 77)
	g := mustGraph(t, buildCatalog(start, 11))

	rep := BuildReport(g, Policy{Daily: 7, Weekly: 4, Monthly: 2}, now)

	ids := make([]string, 0, len(rep.Selected))
	for _, sp := range rep.Selected {
		ids = append(ids, sp.Point.ID)
	}
	keep := g.Closure(ids)
	for _, p := range rep.DeleteSet {
		if keep[p.ID] {
			t.Errorf("delete set contains %s, a dependency of a selected point", p.ID)
		}
	}
	// Every dependency that is not itself selected must be protected.
	protected := make(map[string]bool)
	for _, p := range rep.Protected {
		protected[p.ID] = true
	}
	for id := range keep {
		if findSelected(rep, id) == nil && !protected[id] {
			t.Errorf("dependency %s neither selected nor protected", id)
		}
	}
	if len(rep.DeleteSet) == 0 {
		t.Error("expected some points to be deletable")
	}
}

func TestCorruptDependencyMarksUnusableButRetained(t *testing.T) {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, 77)
	pts := buildCatalog(start, 11)

	// Corrupt the direct predecessor of the newest incremental.
	victim := incrID(now.AddDate(0, 0, -2))
	dependent := incrID(now.AddDate(0, 0, -1))
	for i := range pts {
		if pts[i].ID == victim {
			pts[i].Corrupt = true
			pts[i].CorruptReason = "checksum mismatch"
		}
	}
	g := mustGraph(t, pts)
	rep := BuildReport(g, Policy{Daily: 7, Weekly: 4, Monthly: 2}, now)

	sp := findSelected(rep, dependent)
	if sp == nil {
		t.Fatalf("dependent %s silently dropped from selection", dependent)
	}
	if sp.Usable {
		t.Errorf("dependent %s reported usable despite corrupt dependency", dependent)
	}
	joined := strings.Join(sp.Problems, "\n")
	if !strings.Contains(joined, victim) || !strings.Contains(joined, "checksum mismatch") {
		t.Errorf("problems should name the corrupt dependency, got:\n%s", joined)
	}

	// The corrupt point and the rest of the chain must not be deleted:
	// the selected point still depends on them.
	for _, id := range []string{victim, fullID(start.AddDate(0, 0, 70))} {
		if inDeleteSet(rep, id) {
			t.Errorf("%s must be retained as a dependency, found in delete set", id)
		}
	}

	// The graph validation must surface the corruption.
	foundCorrupt := false
	for _, iss := range rep.Issues {
		if iss.Kind == "corrupt" && iss.PointID == victim {
			foundCorrupt = true
		}
	}
	if !foundCorrupt {
		t.Error("Validate did not report the corrupt point")
	}
}

func TestValidateDetectsStructuralProblems(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	pts := []Point{
		{ID: "f1", Time: base, Type: Full, DependsOn: []string{"ghost"}},
		{ID: "i1", Time: base.Add(24 * time.Hour), Type: Incremental, DependsOn: []string{"i2"}},
		{ID: "i2", Time: base.Add(48 * time.Hour), Type: Incremental, DependsOn: []string{"i1"}},
		{ID: "i3", Time: base.Add(72 * time.Hour), Type: Incremental},
	}
	g := mustGraph(t, pts)
	kinds := make(map[string]bool)
	for _, iss := range g.Validate() {
		kinds[iss.Kind] = true
	}
	for _, want := range []string{
		"missing-dependency", "full-with-deps", "cycle",
		"incremental-without-deps", "time-order",
	} {
		if !kinds[want] {
			t.Errorf("expected issue kind %q, got %v", want, kinds)
		}
	}

	if ok, problems := g.Usable("i1"); ok {
		t.Error("i1 in a dependency cycle must not be usable")
	} else if len(problems) == 0 {
		t.Error("unusable point must come with problems")
	}
}

func TestNewGraphRejectsDuplicates(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_, err := NewGraph([]Point{
		{ID: "f1", Time: base, Type: Full},
		{ID: "f1", Time: base, Type: Full},
	})
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}
}

func TestReportRenders(t *testing.T) {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, 70)
	g := mustGraph(t, buildCatalog(start, 11))
	rep := BuildReport(g, Policy{Daily: 7, Weekly: 4, Monthly: 2}, now)
	out := rep.String()
	for _, section := range []string{
		"Selected recovery points", "Protected dependencies",
		"Delete set", "Recovery graph issues", "reasons:", "chain:",
	} {
		if !strings.Contains(out, section) {
			t.Errorf("report missing section %q", section)
		}
	}
}
