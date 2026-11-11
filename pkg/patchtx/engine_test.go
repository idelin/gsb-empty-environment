package patchtx

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"example.org/offlinecli/pkg/jsonpatch"
)

func decode(t *testing.T, input string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(input), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

func encode(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(raw)
}

func op(kind, path string, value any, from string) jsonpatch.Op {
	o := jsonpatch.Op{Op: kind, Path: path, From: from}
	if value != nil {
		raw, _ := json.Marshal(value)
		msg := json.RawMessage(raw)
		o.Value = &msg
	}
	return o
}

// counterPlugin increments /spec/count and depends on it.
type counterPlugin struct {
	calls int
	limit float64
}

func (p *counterPlugin) Name() string   { return "counter" }
func (p *counterPlugin) Deps() []string { return []string{"/spec/count"} }
func (p *counterPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	count := view.(map[string]any)["spec"].(map[string]any)["count"].(float64)
	if count >= p.limit {
		return nil, nil
	}
	return []jsonpatch.Op{op("replace", "/spec/count", count+1, "")}, nil
}

// labelPlugin stamps a label derived from /spec/count, so it re-runs whenever
// the count changes.
type labelPlugin struct{ calls int }

func (p *labelPlugin) Name() string   { return "labeler" }
func (p *labelPlugin) Deps() []string { return []string{"/spec/count"} }
func (p *labelPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	count := view.(map[string]any)["spec"].(map[string]any)["count"].(float64)
	meta := view.(map[string]any)["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)
	if labels == nil {
		labels = map[string]any{}
	}
	labels["count"] = count
	return []jsonpatch.Op{op("add", "/metadata/labels", labels, "")}, nil
}

// staticPlugin has no dependencies: it runs once and stays cached across runs.
type staticPlugin struct{ calls int }

func (p *staticPlugin) Name() string   { return "static" }
func (p *staticPlugin) Deps() []string { return nil }
func (p *staticPlugin) Patch(any) ([]jsonpatch.Op, error) {
	p.calls++
	return []jsonpatch.Op{op("add", "/metadata/labels/fixed", "v1", "")}, nil
}

// namerPlugin depends only on /metadata/name, so /spec changes must not
// re-invoke it and vice versa.
type namerPlugin struct{ calls int }

func (p *namerPlugin) Name() string   { return "namer" }
func (p *namerPlugin) Deps() []string { return []string{"/metadata/name"} }
func (p *namerPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	name := view.(map[string]any)["metadata"].(map[string]any)["name"].(string)
	return []jsonpatch.Op{op("add", "/metadata/labels/from-name", name, "")}, nil
}

func TestConvergesAndRunsOnlyDirtyPlugins(t *testing.T) {
	counter := &counterPlugin{limit: 3}
	labeler := &labelPlugin{}
	static := &staticPlugin{}
	engine := NewEngine([]Plugin{counter, labeler, static}, 0)

	object := decode(t, `{"metadata":{"labels":{}},"spec":{"count":0}}`)
	result, err := engine.Run(object)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	want := decode(t, `{"metadata":{"labels":{"count":3,"fixed":"v1"}},"spec":{"count":3}}`)
	if !reflect.DeepEqual(result.Object, want) {
		t.Fatalf("got %s, want %s", encode(t, result.Object), encode(t, want))
	}
	if counter.calls != 4 { // counts 0,1,2,3; the last emits an empty patch
		t.Fatalf("counter invoked %d times, want 4", counter.calls)
	}
	if labeler.calls != 4 {
		t.Fatalf("labeler invoked %d times, want 4", labeler.calls)
	}
	if static.calls != 1 {
		t.Fatalf("static invoked %d times, want 1", static.calls)
	}
	if len(result.Rounds) != 4 {
		t.Fatalf("expected 4 rounds (3 changes + fixed point), got %d", len(result.Rounds))
	}
	if !result.Rounds[2].Changed {
		t.Fatal("round 3 must still change the document")
	}
	if result.Rounds[3].Changed {
		t.Fatal("last round must be a fixed point (unchanged)")
	}
	// The caller's object must be untouched.
	if got := encode(t, object); got != `{"metadata":{"labels":{}},"spec":{"count":0}}` {
		t.Fatalf("original object mutated: %s", got)
	}
}

func TestSecondRunSkipsPluginsWithStableDependencies(t *testing.T) {
	counter := &counterPlugin{limit: 3}
	labeler := &labelPlugin{}
	static := &staticPlugin{}
	namer := &namerPlugin{}
	engine := NewEngine([]Plugin{counter, labeler, static, namer}, 0)

	original := decode(t, `{"metadata":{"name":"pod","labels":{}},"spec":{"count":0}}`)
	first, err := engine.Run(original)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Re-running on the already-converged object: every dependency fingerprint
	// matches the cached one, so no plugin is invoked and cached patches make
	// the first round a fixed point.
	result, err := engine.Run(first.Object)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Invocations != 0 {
		t.Fatalf("expected zero invocations on stable rerun, got %d", result.Invocations)
	}
	if len(result.Rounds) != 1 || result.Rounds[0].Changed {
		t.Fatalf("expected single converged round, got %+v", result.Rounds)
	}
	if counter.calls != 4 || labeler.calls != 4 || static.calls != 1 || namer.calls != 1 {
		t.Fatalf("plugins were re-invoked: counter=%d labeler=%d static=%d namer=%d",
			counter.calls, labeler.calls, static.calls, namer.calls)
	}

	// Only /metadata/name changed: namer must re-run, while counter/labeler
	// keep their cached patches and the dependency-free plugin stays untouched.
	renamed := decode(t, `{"metadata":{"name":"pod-renamed","labels":{"count":3,"fixed":"v1","from-name":"pod"}},"spec":{"count":3}}`)
	result, err = engine.Run(renamed)
	if err != nil {
		t.Fatalf("rerun after rename: %v", err)
	}
	if result.Invocations != 1 {
		t.Fatalf("expected only namer to re-run, got %d invocations", result.Invocations)
	}
	if namer.calls != 2 || counter.calls != 4 || labeler.calls != 4 || static.calls != 1 {
		t.Fatalf("unexpected call counts: counter=%d labeler=%d static=%d namer=%d",
			counter.calls, labeler.calls, static.calls, namer.calls)
	}
	labels := result.Object.(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["from-name"] != "pod-renamed" {
		t.Fatalf("namer did not recompute label, labels=%v", labels)
	}
}

// oscillatorPlugin flips /spec/phase between "A" and "B" on every invocation,
// guaranteeing the document never reaches a fixed point.
type oscillatorPlugin struct{ calls int }

func (p *oscillatorPlugin) Name() string   { return "oscillator" }
func (p *oscillatorPlugin) Deps() []string { return []string{"/spec/phase"} }
func (p *oscillatorPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	p.calls++
	phase := view.(map[string]any)["spec"].(map[string]any)["phase"].(string)
	next := "B"
	if phase == "B" {
		next = "A"
	}
	return []jsonpatch.Op{op("replace", "/spec/phase", next, "")}, nil
}

func TestOscillationProducesCycleWitnessAndKeepsOriginal(t *testing.T) {
	oscillator := &oscillatorPlugin{}
	engine := NewEngine([]Plugin{oscillator}, 0)

	original := decode(t, `{"spec":{"phase":"A"},"metadata":{"name":"pod"}}`)
	before := encode(t, original)

	_, err := engine.Run(original)
	var ncErr *NonConvergenceError
	if !errors.As(err, &ncErr) {
		t.Fatalf("expected NonConvergenceError, got %T: %v", err, err)
	}
	if len(ncErr.Cycle) < 2 {
		t.Fatalf("witness must contain at least two repeated states, got %d", len(ncErr.Cycle))
	}
	first, last := ncErr.Cycle[0], ncErr.Cycle[len(ncErr.Cycle)-1]
	if first.Hash != last.Hash {
		t.Fatalf("cycle must close on the same hash: %s != %s", first.Hash, last.Hash)
	}
	if !reflect.DeepEqual(first.Object, last.Object) {
		t.Fatal("cycle endpoints must be identical objects")
	}
	if oscillator.calls < 2 {
		t.Fatalf("oscillator should run at least twice, ran %d", oscillator.calls)
	}
	// The whole point: a failed transaction must leave the input exactly as it was.
	if got := encode(t, original); got != before {
		t.Fatalf("original object not preserved through cycle: before %s, after %s", before, got)
	}
}

// testGuardPlugin emits a test that fails once another plugin changes a value,
// proving that a failed test rolls back the entire combined round.
type guardPlugin struct {
	failOn float64
}

func (p *guardPlugin) Name() string   { return "guard" }
func (p *guardPlugin) Deps() []string { return []string{"/spec/count"} }
func (p *guardPlugin) Patch(view any) ([]jsonpatch.Op, error) {
	// Asserts the count still equals the guarded value in the post-patch view.
	return []jsonpatch.Op{op("test", "/spec/count", p.failOn, "")}, nil
}

func TestFailingTestRollsBackRoundAndOriginalSurvives(t *testing.T) {
	counter := &counterPlugin{limit: 99}
	guard := &guardPlugin{failOn: 0}
	engine := NewEngine([]Plugin{counter, guard}, 0)

	original := decode(t, `{"spec":{"count":0}}`)
	before := encode(t, original)

	_, err := engine.Run(original)
	var roundErr *RoundFailureError
	if !errors.As(err, &roundErr) {
		t.Fatalf("expected RoundFailureError, got %T: %v", err, err)
	}
	if !errors.Is(err, jsonpatch.ErrTestFailed) {
		t.Fatalf("round error must wrap ErrTestFailed, got %v", err)
	}
	if roundErr.Round != 1 {
		t.Fatalf("failure should happen in round 1, got %d", roundErr.Round)
	}
	if got := encode(t, original); got != before {
		t.Fatalf("original object changed after failed test: %s", got)
	}
}

func TestCombinedPatchesAreAtomicAcrossPlugins(t *testing.T) {
	// Two plugins touch the same array; indices must chain against the view left
	// by the earlier plugin in plugin order within one transaction.
	first := pluginFunc{"first", []string{"/items"}, func(view any) ([]jsonpatch.Op, error) {
		items := view.(map[string]any)["items"].([]any)
		if len(items) > 0 && items[0] == "x" {
			return nil, nil // idempotent once its insertion is present
		}
		return []jsonpatch.Op{op("add", "/items/0", "x", "")}, nil
	}}
	second := pluginFunc{"second", []string{"/items"}, func(any) ([]jsonpatch.Op, error) {
		// After "first", the original element shifted to index 1.
		return []jsonpatch.Op{op("test", "/items/1", "keep", "")}, nil
	}}
	engine := NewEngine([]Plugin{first, second}, 0)
	result, err := engine.Run(decode(t, `{"items":["keep"]}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := decode(t, `{"items":["x","keep"]}`)
	if !reflect.DeepEqual(result.Object, want) {
		t.Fatalf("got %s, want %s", encode(t, result.Object), encode(t, want))
	}
	if result.Invocations != 4 { // both plugins re-run in round 2, then converge in round 3
		t.Fatalf("expected 4 plugin invocations across rounds, got %d", result.Invocations)
	}
}

type pluginFunc struct {
	name string
	deps []string
	fn   func(any) ([]jsonpatch.Op, error)
}

func (p pluginFunc) Name() string                           { return p.name }
func (p pluginFunc) Deps() []string                         { return p.deps }
func (p pluginFunc) Patch(view any) ([]jsonpatch.Op, error) { return p.fn(view) }

func TestDuplicatePluginNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate plugin name")
		}
	}()
	NewEngine([]Plugin{
		pluginFunc{"dup", nil, func(any) ([]jsonpatch.Op, error) { return nil, nil }},
		pluginFunc{"dup", nil, func(any) ([]jsonpatch.Op, error) { return nil, nil }},
	}, 0)
}
