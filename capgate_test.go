package capgate

import (
	"strings"
	"testing"
)

func baseStages() []string { return []string{"fetch", "build", "publish"} }

func halfEdge(from, to, stage, txn, digest string, idx int, sels ...Selector) Edge {
	return Edge{
		From:            from,
		To:              to,
		Stage:           stage,
		Platform:        sels,
		TransportDigest: "td:" + from + "-" + to,
		Half: &Half{
			TxnID:     txn,
			Digest:    digest,
			HalfIndex: idx,
			Stage:     stage,
			Transport: "attested",
		},
	}
}

func plainCycleEdge(from, to, stage string) Edge {
	return Edge{From: from, To: to, Stage: stage, TransportDigest: "td:" + from + "-" + to}
}

func linuxOn() Selector  { return Selector{Platform: "linux", On: true} }
func darwinOn() Selector { return Selector{Platform: "darwin", On: true} }

func denialReasons(r Report) map[string]bool {
	out := map[string]bool{}
	for _, d := range r.Denials {
		out[d.Reason] = true
	}
	return out
}

func checkByID(r Report) map[string]CheckResult {
	m := map[string]CheckResult{}
	for _, c := range r.Checks {
		m[c.ID] = c
	}
	return m
}

func assertAllChecksPass(t *testing.T, r Report) {
	t.Helper()
	for _, c := range r.Checks {
		if !c.Pass {
			t.Fatalf("semantic check %s failed: %s", c.ID, c.Detail)
		}
	}
}

func hasAdded(reg Region, key string) bool {
	for _, a := range reg.Added {
		if a == key {
			return true
		}
	}
	return false
}

// TestExhaustiveAndMutex covers small-bit enumeration and mutually-exclusive
// platform regions: a linux-only new grant never appears in a darwin combo.
func TestExhaustiveAndMutex(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux", "darwin"},
		Flags:          []string{"exp"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old: Graph{Label: "o", Nodes: []Node{
			{ID: "r", Name: "app", Digest: "sha:r"},
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "p", Name: "p", Digest: "sha:p1"},
		}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "r", Name: "app", Digest: "sha:r"},
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "b", Name: "b", Digest: "sha:b"},
			{ID: "p", Name: "p", Digest: "sha:p2"},
		}},
	}
	job.New.Edges = []Edge{
		halfEdge("a", "p", "publish", "T", "sha:p2", 0, linuxOn()),
		halfEdge("b", "p", "publish", "T", "sha:p2", 1, linuxOn()),
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if r.ComboCount != 4 {
		t.Fatalf("expected 4 legal combos, got %d", r.ComboCount)
	}
	if !r.Coverage.Complete || !r.Coverage.RegionsDisjoint {
		t.Fatalf("coverage not a complete disjoint partition: %+v", r.Coverage)
	}
	for _, reg := range r.Regions {
		for _, cube := range reg.Cubes {
			linux := cube.Platforms["linux"]
			if hasAdded(reg, "p@publish") && (linux == nil || !*linux) {
				t.Fatalf("new publish capability leaked into a non-linux cube: %+v", cube.Platforms)
			}
		}
	}
	assertAllChecksPass(t, r)
}

// TestCrossStageLeakDenied ensures env/cache smuggled credentials are rejected
// and cannot complete a sensitive operation in a later stage.
func TestCrossStageLeakDenied(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{
			Label: "n",
			Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}},
			Edges: []Edge{halfEdge("p", "p", "build", "T", "sha:p", 0)},
			Leaks: []LeakAttempt{
				{FromStage: "build", ToStage: "publish", Node: "p", Digest: "sha:p", TxnID: "T", Channel: "env"},
				{FromStage: "build", ToStage: "publish", Node: "p", Digest: "sha:p", TxnID: "T", Channel: "writable_cache"},
			},
		},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !denialReasons(r)["cross_stage_leak_blocked"] {
		t.Fatalf("expected cross_stage_leak_blocked denial, got %v", denialReasons(r))
	}
	for _, reg := range r.Regions {
		if len(reg.Added) != 0 {
			t.Fatalf("leaked credential must not authorise a publish op, added=%v", reg.Added)
		}
	}
	if !checkByID(r)["isolation.cross_stage_leak"].Pass {
		t.Fatal("cross-stage leak check failed")
	}
}

// TestHalfStageMismatchDenied issues a half for an earlier stage but places it
// on a publish edge; the half must be refused rather than promoted.
func TestHalfStageMismatchDenied(t *testing.T) {
	e := halfEdge("a", "p", "publish", "T", "sha:p", 0)
	e.Half.Stage = "build" // credential belongs to an earlier stage
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "p", Name: "p", Digest: "sha:p"},
		}, Edges: []Edge{
			e,
			halfEdge("a", "p", "publish", "T", "sha:p", 1),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !denialReasons(r)["half_stage_mismatch"] {
		t.Fatalf("expected half_stage_mismatch denial, got %v", denialReasons(r))
	}
	for _, reg := range r.Regions {
		if len(reg.Chains) != 0 {
			t.Fatalf("stage-mismatched half must not complete a grant: %+v", reg.Chains)
		}
	}
}

// TestSameNameHostAndTarget verifies a host tool and target package sharing a
// name stay isolated because authorisation binds id+digest, not the name.
func TestSameNameHostAndTarget(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old: Graph{Label: "o", Nodes: []Node{
			{ID: "tool", Name: "zip", Digest: "sha:host"},
			{ID: "tgt", Name: "zip", Digest: "sha:tgt"},
		}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "tool", Name: "zip", Host: true, Digest: "sha:host"},
			{ID: "tgt", Name: "zip", Digest: "sha:tgt"},
		}, Edges: []Edge{
			halfEdge("tool", "tgt", "publish", "T", "sha:tgt", 0),
			halfEdge("tool", "tgt", "publish", "T", "sha:tgt", 1),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !checkByID(r)["isolation.same_name"].Pass {
		t.Fatal("same-name isolation check failed")
	}
	found := false
	for _, reg := range r.Regions {
		for _, ch := range reg.Chains {
			if ch.Instance.Node == "tgt" && ch.Digest == "sha:tgt" {
				found = true
			}
			if ch.Digest == "sha:host" {
				t.Fatal("host tool digest must never receive a target grant")
			}
		}
	}
	if !found {
		t.Fatal("expected a digest-bound grant on the target instance")
	}
}

// TestDependencyDowngradeRejected feeds a half whose digest does not match the
// receiver's locked content and expects hard rejection at validation time.
func TestDependencyDowngradeRejected(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "p", Name: "p", Digest: "sha:p-new"},
		}, Edges: []Edge{
			halfEdge("a", "p", "publish", "T", "sha:p-old", 0),
		}},
	}
	if _, err := Analyze(job); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected digest/downgrade validation error, got %v", err)
	}
}

// TestCrossTransactionMergeInvalid confirms halves from different issuing
// transactions never merge, even when digest and receiver match.
func TestCrossTransactionMergeInvalid(t *testing.T) {
	nodes := []Node{
		{ID: "a", Name: "a", Digest: "sha:a"},
		{ID: "b", Name: "b", Digest: "sha:b"},
		{ID: "p", Name: "p", Digest: "sha:p"},
	}
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: nodes},
		New: Graph{Label: "n", Nodes: nodes, Edges: []Edge{
			halfEdge("a", "p", "publish", "TX1", "sha:p", 0),
			halfEdge("b", "p", "publish", "TX2", "sha:p", 1),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !denialReasons(r)["cross_transaction_merge"] {
		t.Fatalf("expected cross_transaction_merge denial: %v", denialReasons(r))
	}
	for _, reg := range r.Regions {
		if len(reg.Chains) != 0 {
			t.Fatalf("cross-txn halves must not produce a chain: %+v", reg.Chains)
		}
	}
}

// TestNoDeltaCoverageProof uses identical old/new graphs and expects an empty
// delta plus a complete coverage proof rather than an absent report.
func TestNoDeltaCoverageProof(t *testing.T) {
	nodes := []Node{
		{ID: "a", Name: "a", Digest: "sha:a"},
		{ID: "p", Name: "p", Digest: "sha:p"},
	}
	edges := []Edge{
		halfEdge("a", "p", "publish", "T", "sha:p", 0),
		halfEdge("a", "p", "publish", "T", "sha:p", 1),
	}
	job := &Job{
		Platforms:      []string{"linux", "darwin"},
		Flags:          []string{"exp"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: nodes, Edges: edges},
		New:            Graph{Label: "n", Nodes: nodes, Edges: edges},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !r.NoDelta || !r.Coverage.NoDelta {
		t.Fatal("expected no-delta result")
	}
	if !r.Coverage.Complete || r.Coverage.CombosInRegions != r.ComboCount {
		t.Fatal("no-delta run still needs full coverage proof")
	}
	if len(r.Regions) != 1 || len(r.Regions[0].Added) != 0 {
		t.Fatalf("expected single empty-delta region, got %+v", r.Regions)
	}
	assertAllChecksPass(t, r)
}

// TestOldPathHalvesNotSpliced builds two half paths in the OLD graph and a
// single half in the NEW graph. The new evaluation must not borrow the old
// half to complete a grant.
func TestOldPathHalvesNotSpliced(t *testing.T) {
	nodes := []Node{
		{ID: "a", Name: "a", Digest: "sha:a"},
		{ID: "b", Name: "b", Digest: "sha:b"},
		{ID: "p", Name: "p", Digest: "sha:p"},
	}
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old: Graph{Label: "o", Nodes: nodes, Edges: []Edge{
			halfEdge("a", "p", "publish", "T", "sha:p", 0),
			halfEdge("b", "p", "publish", "T", "sha:p", 1),
		}},
		New: Graph{Label: "n", Nodes: nodes, Edges: []Edge{
			halfEdge("a", "p", "publish", "T", "sha:p", 0),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range r.Regions {
		if len(reg.Chains) != 0 {
			t.Fatalf("new graph must not splice an old half into a chain: %+v", reg.Chains)
		}
	}
	if !denialReasons(r)["incomplete_half_set"] {
		t.Fatalf("expected incomplete_half_set denial, got %v", denialReasons(r))
	}
	if !checkByID(r)["isolation.old_splicing"].Pass {
		t.Fatal("old-splicing check failed")
	}
}

// TestConditionalCycleConverges creates an active cycle and requires the
// fixpoint walk to terminate, recording a cycle-revisit skip rather than
// looping, while still delivering the half to the intended receiver.
func TestConditionalCycleConverges(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "s", Name: "s", Digest: "sha:s"},
			{ID: "a", Name: "a", Digest: "sha:ab"},
			{ID: "b", Name: "b", Digest: "sha:ab"},
			{ID: "p", Name: "p", Digest: "sha:p"},
		}, Edges: []Edge{
			// Active conditional cycle a<->b carrying an sh:ab half.
			plainCycleEdge("a", "b", "publish"),
			plainCycleEdge("b", "a", "publish"),
			halfEdge("s", "a", "publish", "T", "sha:ab", 0),
			halfEdge("s", "b", "publish", "T", "sha:ab", 1),
			// Independent, cycle-free pair that still completes a grant on p.
			halfEdge("s", "p", "publish", "TP", "sha:p", 0),
			halfEdge("a", "p", "publish", "TP", "sha:p", 1),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	if !denialReasons(r)["cycle_revisit_skipped"] {
		t.Fatalf("expected cycle_revisit_skipped denial, got %v", denialReasons(r))
	}
	granted := false
	for _, reg := range r.Regions {
		for _, ch := range reg.Chains {
			if ch.Instance.Node == "p" {
				granted = true
			}
		}
	}
	if !granted {
		t.Fatal("half should still reach p despite the conditional cycle")
	}
	assertAllChecksPass(t, r)
}

// TestOptionalGateFlag ensures an optional dependency only grants when its
// feature switch is on, and that off/on combos land in disjoint regions.
func TestOptionalGateFlag(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Flags:          []string{"exp"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "b", Name: "b", Digest: "sha:b"},
			{ID: "p", Name: "p", Digest: "sha:p"},
		}},
	}
	job.New.Edges = []Edge{
		gateHalf("a", "p", "exp", true, 0),
		gateHalf("b", "p", "exp", true, 1),
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	onRegion, offRegion := false, false
	for _, reg := range r.Regions {
		has := hasAdded(reg, "p@publish")
		for _, cube := range reg.Cubes {
			exp := cube.Flags["exp"]
			if has && exp != nil && *exp {
				onRegion = true
			}
			if !has {
				offRegion = true
			}
		}
	}
	if !onRegion || !offRegion {
		t.Fatalf("optional gate must split flag-on (granted) from flag-off (denied): %+v", r.Regions)
	}
	assertAllChecksPass(t, r)
}

func gateHalf(from, to, flag string, on bool, idx int) Edge {
	e := halfEdge(from, to, "publish", "T", "sha:p", idx)
	e.Optional = true
	e.GateFlag = flag
	e.GateOn = on
	return e
}

// TestContradictoryConditionIsUnsatisfiable proves contradictory selectors
// make an edge inactive everywhere rather than silently permissive.
func TestContradictoryConditionIsUnsatisfiable(t *testing.T) {
	job := &Job{
		Platforms:      []string{"linux"},
		Stages:         baseStages(),
		SensitiveStage: "publish",
		Old:            Graph{Label: "o", Nodes: []Node{{ID: "p", Name: "p", Digest: "sha:p"}}},
		New: Graph{Label: "n", Nodes: []Node{
			{ID: "a", Name: "a", Digest: "sha:a"},
			{ID: "p", Name: "p", Digest: "sha:p"},
		}, Edges: []Edge{
			halfEdge("a", "p", "publish", "T", "sha:p", 0,
				Selector{Platform: "linux", On: true},
				Selector{Platform: "linux", On: false}),
		}},
	}
	r, err := Analyze(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range r.Regions {
		if len(reg.Chains) != 0 || hasAdded(reg, "p@publish") {
			t.Fatalf("contradictory condition must never activate an edge: %+v", reg)
		}
	}
}
