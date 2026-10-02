package logtmpl

import (
	"math/rand"
	"reflect"
	"testing"
)

func runCluster(t *testing.T, lines []string, cfg Config) *Clusterer {
	t.Helper()
	c := New(cfg, nil)
	for _, l := range lines {
		c.Add(l)
	}
	c.Finalize()
	return c
}

func TestNumberAndIDAbstraction(t *testing.T) {
	c := runCluster(t, []string{
		"request id=a1b2c3d4e5f6 took 120 ms",
		"request id=ff00aa11bb22 took 98 ms",
	}, Config{Deterministic: true})
	ts := c.Templates()
	if len(ts) != 1 {
		t.Fatalf("got %d templates, want 1", len(ts))
	}
	if got, want := ts[0].Signature(), "request id = <ID> took <NUM> ms"; got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}
	if ts[0].Count != 2 {
		t.Fatalf("count %d, want 2", ts[0].Count)
	}
	// Example mapping is preserved.
	if len(ts[0].Examples) != 2 {
		t.Fatalf("examples %v, want 2 entries", ts[0].Examples)
	}
}

func TestErrorCodesKeepDistinction(t *testing.T) {
	c := runCluster(t, []string{
		"ERR-100 disk full",
		"ERR-200 disk full",
		"E900 disk full",
		"500 disk full",
	}, Config{Deterministic: true})
	sigs := Signatures(c.Templates())
	want := []string{"500 disk full", "<ERR:ERR> disk full", "E900 disk full"}
	if !reflect.DeepEqual(sigs, want) {
		t.Fatalf("signatures %v, want %v", sigs, want)
	}
}

func TestTokenCountMismatchNeverMerges(t *testing.T) {
	c := runCluster(t, []string{
		"a b c",
		"a b",
		"a b c d",
	}, Config{Deterministic: true})
	if got := len(c.Templates()); got != 3 {
		t.Fatalf("got %d templates, want 3 (token counts differ)", got)
	}
}

func TestBudgetExhaustionRetainsUnclassified(t *testing.T) {
	lines := []string{
		"alpha one two",
		"bravo three four",
		"charlie five six",
		"delta seven eight",
	}
	c := runCluster(t, lines, Config{Deterministic: true, MaxTemplates: 2})
	if got := len(c.Templates()); got != 2 {
		t.Fatalf("live templates %d, want 2", got)
	}
	un := c.Unclassified()
	if len(un) != 2 {
		t.Fatalf("unclassified %d, want 2: budget must not swallow records", len(un))
	}
	for _, u := range un {
		if u.Reason == "" || u.Line == "" {
			t.Fatalf("unclassified record missing line/reason: %+v", u)
		}
	}
	// Every input record is accounted for: classified count + unclassified.
	total := 0
	for _, tp := range c.Templates() {
		total += tp.Count
	}
	if total+len(un) != len(lines) {
		t.Fatalf("accounting: %d classified + %d unclassified != %d input",
			total, len(un), len(lines))
	}
}

func TestOverGeneralizationLimit(t *testing.T) {
	lines := []string{"req 1 north", "req 2 south"}
	// ratio cap 0.5: generalizing 2 of 3 positions (0.67) is rejected.
	strict := runCluster(t, lines, Config{Deterministic: true, MaxWildcardRatio: 0.5})
	if got := len(strict.Templates()); got != 2 {
		t.Fatalf("strict: got %d templates, want 2 (over-generalization blocked)", got)
	}
	// ratio cap 0.7: the same merge is allowed.
	loose := runCluster(t, lines, Config{Deterministic: true, MaxWildcardRatio: 0.7})
	if got := len(loose.Templates()); got != 1 {
		t.Fatalf("loose: got %d templates, want 1", got)
	}
}

func TestMergeEvidenceAndForbiddenFields(t *testing.T) {
	c := runCluster(t, []string{
		"user alice logged in",
		"user bob logged in",
	}, Config{Deterministic: true})
	var ev *Evidence
	for _, e := range c.Journal() {
		if e.Op == "add" && e.Evidence != nil {
			ev = e.Evidence
		}
	}
	if ev == nil {
		t.Fatal("no merge evidence recorded in journal")
	}
	if ev.Kind != "generalize" {
		t.Fatalf("evidence kind %q, want generalize", ev.Kind)
	}
	if len(ev.Sources) != 1 {
		t.Fatalf("evidence sources %v, want 1 source template", ev.Sources)
	}
	if !reflect.DeepEqual(ev.Positions, []int{1}) {
		t.Fatalf("generalized positions %v, want [1]", ev.Positions)
	}
	if !reflect.DeepEqual(ev.Forbidden, []int{0}) {
		t.Fatalf("forbidden positions %v, want [0]", ev.Forbidden)
	}
	if len(ev.Samples) == 0 {
		t.Fatal("evidence must carry sample lines from its sources")
	}
}

func TestSplitIsNotMonotonic(t *testing.T) {
	lines := []string{
		"user u1 logged in",
		"user u2 logged in",
		"user u3 logged in",
		"user u4 logged in",
	}
	c := runCluster(t, lines, Config{Deterministic: true, MaxSlotValues: 2})
	// The <WORD> template must have been split into literal templates and
	// the barrier must prevent re-creating it.
	for _, tp := range c.Templates() {
		if tp.Signature() == "user <WORD> logged in" {
			t.Fatal("split template re-appeared: generalization must not be monotonic")
		}
	}
	if got := len(c.Templates()); got != 4 {
		t.Fatalf("got %d templates, want 4 literal ones after split", got)
	}
	// The journal must show retract entries (split + generalize history).
	retracts, splits := 0, 0
	for _, e := range c.Journal() {
		if e.Op == "retract" {
			retracts++
			if e.Reason == "split" {
				splits++
			}
		}
	}
	if retracts == 0 || splits == 0 {
		t.Fatalf("journal: retracts=%d splits=%d, want both > 0", retracts, splits)
	}
	// Total count is conserved across the split.
	total := 0
	for _, tp := range c.Templates() {
		total += tp.Count
	}
	if total != 4 {
		t.Fatalf("total count %d, want 4", total)
	}
}

func shuffles(lines []string, n int) [][]string {
	out := [][]string{append([]string(nil), lines...)}
	r := rand.New(rand.NewSource(42))
	for i := 0; i < n; i++ {
		p := append([]string(nil), lines...)
		r.Shuffle(len(p), func(a, b int) { p[a], p[b] = p[b], p[a] })
		out = append(out, p)
	}
	return out
}

var determinismLines = []string{
	"user alice logged in",
	"user bob logged in",
	"request id=a1b2c3d4e5f6 took 120 ms",
	"request id=ff00aa11bb22 took 98 ms",
	"ERR-100 disk full",
	"ERR-200 disk full",
	"E900 disk full",
	"status = 200 health ok",
	"status = 404 health ok",
	"job worker-1 failed",
	"job worker-2 failed",
}

func TestDeterministicModeOrderIndependent(t *testing.T) {
	var reference []string
	for i, lines := range shuffles(determinismLines, 5) {
		c := runCluster(t, lines, Config{Deterministic: true})
		sigs := Signatures(c.Templates())
		if i == 0 {
			reference = sigs
			continue
		}
		if !reflect.DeepEqual(sigs, reference) {
			t.Fatalf("shuffle %d: signatures %v, want %v", i, sigs, reference)
		}
	}
}

func TestOfflineReferenceOrderIndependent(t *testing.T) {
	var reference []string
	for i, lines := range shuffles(determinismLines, 5) {
		sigs := Signatures(Recluster(lines, Config{}))
		if i == 0 {
			reference = sigs
			continue
		}
		if !reflect.DeepEqual(sigs, reference) {
			t.Fatalf("shuffle %d: offline signatures %v, want %v", i, sigs, reference)
		}
	}
}

func TestOnlineOfflineAgreement(t *testing.T) {
	cfg := Config{Deterministic: true}
	online := runCluster(t, determinismLines, cfg)
	offline := Recluster(determinismLines, cfg)
	onP, ok1 := PartitionOf(online.Templates(), determinismLines)
	offP, ok2 := PartitionOf(offline, determinismLines)
	if !ok1 || !ok2 {
		t.Fatalf("ambiguous partition: online=%v offline=%v", ok1, ok2)
	}
	if !reflect.DeepEqual(onP, offP) {
		t.Fatalf("partitions diverged:\nonline:  %v\noffline: %v", onP, offP)
	}
}
