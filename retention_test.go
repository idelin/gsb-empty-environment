package retention

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// buildCatalog creates one backup per day: a full every 7 days and
// incrementals chained to the previous day otherwise.
func buildCatalog(days int, corrupted map[string]bool) []*Backup {
	var out []*Backup
	for i := 0; i < days; i++ {
		id := fmt.Sprintf("d%03d", i)
		b := &Backup{ID: id, Time: base.AddDate(0, 0, i), Corrupted: corrupted[id]}
		if i%7 == 0 {
			b.Type = Full
		} else {
			b.Type = Incremental
			b.ParentID = fmt.Sprintf("d%03d", i-1)
		}
		out = append(out, b)
	}
	return out
}

func decisionOf(rep *Report, id string) *Decision {
	for _, d := range rep.Decisions {
		if d.Backup.ID == id {
			return d
		}
	}
	return nil
}

func TestSelectKeepsDependenciesAndValidates(t *testing.T) {
	g, err := NewGraph(buildCatalog(60, nil))
	if err != nil {
		t.Fatal(err)
	}
	now := base.AddDate(0, 0, 60)
	rep := g.Select(Policy{Daily: 7, Weekly: 4, Monthly: 3}, now)

	if errs := g.Validate(rep); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}
	if len(rep.DeleteSet) == 0 {
		t.Fatal("expected a non-empty delete set")
	}
	// Every kept incremental must keep its parent too.
	for _, d := range rep.Decisions {
		if !d.Kept {
			continue
		}
		if d.Unavailable != "" {
			t.Errorf("kept backup %s is unavailable: %s", d.Backup.ID, d.Unavailable)
		}
		if d.Backup.Type == Incremental {
			if pd := decisionOf(rep, d.Backup.ParentID); pd == nil || !pd.Kept {
				t.Errorf("kept %s but parent %s not kept", d.Backup.ID, d.Backup.ParentID)
			}
		}
	}
	// Newest backup is selected by the daily rule with rationale and chain.
	newest := decisionOf(rep, "d059")
	if newest == nil || !newest.Selected {
		t.Fatal("newest backup not selected")
	}
	want := "daily " + base.AddDate(0, 0, 59).Format("2006-01-02")
	found := false
	for _, r := range newest.Reasons {
		if r == want {
			found = true
		}
	}
	if !found {
		t.Errorf("newest backup missing rationale %q, got %v", want, newest.Reasons)
	}
	if len(newest.Chain) == 0 || newest.Chain[len(newest.Chain)-1] != "d059" {
		t.Errorf("bad chain for d059: %v", newest.Chain)
	}
	if fb, _ := g.Get(newest.Chain[0]); fb.Type != Full {
		t.Errorf("chain root %s is not a full backup", newest.Chain[0])
	}
}

func TestCorruptedChainReportedNotDropped(t *testing.T) {
	g, err := NewGraph(buildCatalog(60, map[string]bool{"d015": true}))
	if err != nil {
		t.Fatal(err)
	}
	rep := g.Select(Policy{Daily: 7, Weekly: 4, Monthly: 3}, base.AddDate(0, 0, 60))

	// d015..d020 depend on the corrupted d015 and must be reported
	// unavailable, present in the decisions, and not selected.
	for i := 15; i <= 20; i++ {
		id := fmt.Sprintf("d%03d", i)
		d := decisionOf(rep, id)
		if d == nil {
			t.Fatalf("%s missing from report (silently dropped)", id)
		}
		if d.Unavailable == "" {
			t.Errorf("%s should be unavailable", id)
		}
		if !strings.Contains(d.Unavailable, "corrupted") {
			t.Errorf("%s unavailability should mention corruption: %q", id, d.Unavailable)
		}
		if d.Selected {
			t.Errorf("unavailable %s must not be selected", id)
		}
	}
	// Points after the next full (d021) remain restorable.
	if !g.Restorable("d021") {
		t.Error("d021 is a full backup and must stay restorable")
	}
	if errs := g.Validate(rep); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}
}

func TestGapReportedWhenBucketHasNoRestorablePoint(t *testing.T) {
	// Corrupt the only full backup: every point becomes unavailable.
	g, err := NewGraph(buildCatalog(7, map[string]bool{"d000": true}))
	if err != nil {
		t.Fatal(err)
	}
	rep := g.Select(Policy{Daily: 3}, base.AddDate(0, 0, 7))
	if len(rep.Gaps) != 3 {
		t.Fatalf("expected 3 daily gaps, got %v", rep.Gaps)
	}
	if len(rep.DeleteSet) != 7 {
		t.Fatalf("all 7 backups should be deleted, got %v", rep.DeleteSet)
	}
	if errs := g.Validate(rep); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}
}

func TestBrokenGraphs(t *testing.T) {
	if _, err := NewGraph(buildCatalog(3, nil)); err != nil {
		t.Fatal(err)
	}
	// Missing parent.
	g, _ := NewGraph([]*Backup{
		{ID: "a", Type: Incremental, ParentID: "ghost", Time: base},
	})
	if _, err := g.Chain("a"); err == nil || !strings.Contains(err.Error(), "missing parent") {
		t.Errorf("expected missing parent error, got %v", err)
	}
	// Cycle.
	g, _ = NewGraph([]*Backup{
		{ID: "a", Type: Incremental, ParentID: "b", Time: base},
		{ID: "b", Type: Incremental, ParentID: "a", Time: base},
	})
	if _, err := g.Chain("a"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got %v", err)
	}
	// Duplicate IDs rejected.
	if _, err := NewGraph([]*Backup{
		{ID: "a", Type: Full, Time: base},
		{ID: "a", Type: Full, Time: base},
	}); err == nil {
		t.Error("expected duplicate ID error")
	}
}

func TestValidateDetectsTamperedDeleteSet(t *testing.T) {
	g, _ := NewGraph(buildCatalog(60, nil))
	rep := g.Select(Policy{Daily: 7, Weekly: 4, Monthly: 3}, base.AddDate(0, 0, 60))

	// Find a kept incremental and move its parent into the delete set.
	var victim *Decision
	for _, d := range rep.Decisions {
		if d.Kept && d.Backup.Type == Incremental {
			victim = d
			break
		}
	}
	if victim == nil {
		t.Fatal("no kept incremental found")
	}
	parent := victim.Backup.ParentID
	for _, d := range rep.Decisions {
		if d.Backup.ID == parent {
			d.Kept = false
		}
	}
	rep.DeleteSet = append(rep.DeleteSet, parent)

	errs := g.Validate(rep)
	if len(errs) == 0 {
		t.Fatal("validate must reject a delete set that drops a dependency")
	}
	joined := fmt.Sprint(errs)
	if !strings.Contains(joined, "loses dependency") {
		t.Errorf("expected dependency-loss error, got %v", errs)
	}
}
