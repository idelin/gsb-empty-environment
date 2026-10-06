package batch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var ctx = context.Background()

// echoProcess derives output deterministically from the item's own content
// and the confirmed outputs of its same-batch dependencies.
func echoProcess(_ context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error) {
	var b strings.Builder
	b.Write(content)
	if len(deps) > 0 {
		names := make([]string, 0, len(deps))
		for name := range deps {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "<%s:%s>", name, deps[name].Output)
		}
	}
	return []byte(b.String()), nil
}

// failAfter wraps a ProcessFunc and returns an injected error once the
// wrapped function has succeeded n times, simulating a crash mid-batch.
func failAfter(p ProcessFunc, n int) ProcessFunc {
	calls := 0
	return func(ctx context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error) {
		if calls >= n {
			return nil, errors.New("injected crash")
		}
		calls++
		return p(ctx, item, content, deps)
	}
}

func newFixture(files map[string]string) (*MemFS, *MemStore, *Runner) {
	fs := NewMemFS()
	for path, content := range files {
		fs.Set(path, []byte(content))
	}
	store := NewMemStore()
	return fs, store, NewRunner(fs, store, echoProcess)
}

// resultMap flattens a report into path -> output for comparison.
func resultMap(rep *Report) map[string]string {
	out := make(map[string]string)
	for _, it := range rep.Items {
		if it.Result != nil {
			out[it.Path] = string(it.Result.Output)
		}
	}
	return out
}

func TestFreezeIgnoresNewFiles(t *testing.T) {
	fs, _, runner := newFixture(map[string]string{"a.txt": "A", "b.txt": "B"})
	manifest := []Item{{Path: "a.txt"}, {Path: "b.txt"}}

	// A new file appears in the directory mid-run; it must not be picked up.
	wrapped := runner.process
	runner.process = func(ctx context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error) {
		fs.Set("c.txt", []byte("C"))
		return wrapped(ctx, item, content, deps)
	}

	rep, err := runner.Start(ctx, "b1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 2 {
		t.Fatalf("batch picked up files outside the frozen manifest: %+v", rep.Items)
	}
	for _, it := range rep.Items {
		if it.Status != StatusDone {
			t.Fatalf("item %s not done: %s", it.Path, it.Reason)
		}
	}
}

func TestDeleteBeforeRestartIsolatesItem(t *testing.T) {
	fs, _, runner := newFixture(map[string]string{"a.txt": "A", "b.txt": "B"})
	manifest := []Item{{Path: "a.txt"}, {Path: "b.txt"}}

	// Process only a.txt, then crash leaving b.txt pending.
	runner.process = failAfter(echoProcess, 1)
	if _, err := runner.Start(ctx, "b1", manifest); err == nil {
		t.Fatal("expected abort after first item")
	}

	fs.Delete("b.txt")
	runner.process = echoProcess
	rep, err := runner.Resume(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.StatusOf("b.txt"); got != StatusIsolated {
		t.Fatalf("deleted item status = %s, want isolated", got)
	}
	res := rep.ResultOf("a.txt")
	if res == nil || string(res.Output) != "A" {
		t.Fatalf("done item lost across restart: %+v", res)
	}
	if res.InputDigest != digestOf([]byte("A")) {
		t.Fatalf("done result not bound to original digest: %s", res.InputDigest)
	}
}

func TestReplaceBeforeRestartIsolatesItemAndSuccessors(t *testing.T) {
	// Chain a -> b -> c plus independent d.
	files := map[string]string{"a": "A", "b": "B", "c": "C", "d": "D"}
	fs, _, runner := newFixture(files)
	manifest := []Item{
		{Path: "a"},
		{Path: "b", DependsOn: []string{"a"}},
		{Path: "c", DependsOn: []string{"b"}},
		{Path: "d"},
	}

	// Crash after processing a and d... order is a,b,c,d; process a only.
	runner.process = failAfter(echoProcess, 1)
	if _, err := runner.Start(ctx, "b1", manifest); err == nil {
		t.Fatal("expected abort")
	}

	// Replace b's source while the batch is down.
	fs.Set("b", []byte("B2"))
	runner.process = echoProcess
	rep, err := runner.Resume(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.StatusOf("b"); got != StatusIsolated {
		t.Fatalf("replaced item status = %s, want isolated", got)
	}
	if got := rep.StatusOf("c"); got != StatusIsolated {
		t.Fatalf("successor status = %s, want isolated", got)
	}
	if got := rep.StatusOf("d"); got != StatusDone {
		t.Fatalf("independent item status = %s, want done", got)
	}
	if got := rep.StatusOf("a"); got != StatusDone {
		t.Fatalf("already-processed item status = %s, want done", got)
	}
}

func TestReplaceDuringRunIsolatesBeforeProcessing(t *testing.T) {
	fs, _, runner := newFixture(map[string]string{"a": "A", "b": "B"})
	manifest := []Item{{Path: "a"}, {Path: "b"}}

	// Replace b's source from inside a's processing call.
	wrapped := runner.process
	runner.process = func(ctx context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error) {
		if item.Path == "a" {
			fs.Set("b", []byte("B2"))
		}
		return wrapped(ctx, item, content, deps)
	}

	rep, err := runner.Start(ctx, "b1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.StatusOf("b"); got != StatusIsolated {
		t.Fatalf("replaced mid-run item status = %s, want isolated", got)
	}
	if got := rep.StatusOf("a"); got != StatusDone {
		t.Fatalf("untouched item status = %s, want done", got)
	}
}

func TestProcessedResultStaysBoundToOriginalDigest(t *testing.T) {
	// b depends on a. a is processed, then a's source is replaced while the
	// batch is down. On resume a's confirmed result must be kept (bound to
	// the original digest) and b must consume that same-batch result.
	fs, _, runner := newFixture(map[string]string{"a": "A", "b": "B"})
	manifest := []Item{{Path: "a"}, {Path: "b", DependsOn: []string{"a"}}}

	runner.process = failAfter(echoProcess, 1) // process a, crash on b
	if _, err := runner.Start(ctx, "b1", manifest); err == nil {
		t.Fatal("expected abort")
	}
	fs.Set("a", []byte("A2")) // replace a's source after it was processed

	runner.process = echoProcess
	rep, err := runner.Resume(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	resA := rep.ResultOf("a")
	if resA == nil || string(resA.Output) != "A" {
		t.Fatalf("processed result not retained: %+v", resA)
	}
	if resA.InputDigest != digestOf([]byte("A")) {
		t.Fatalf("result rebound to new digest: %s", resA.InputDigest)
	}
	resB := rep.ResultOf("b")
	if resB == nil || string(resB.Output) != "B<a:A>" {
		t.Fatalf("dependent did not consume original same-batch result: %+v", resB)
	}
}

func TestExplicitNewBatchRecomputes(t *testing.T) {
	fs, _, runner := newFixture(map[string]string{"a": "A"})
	manifest := []Item{{Path: "a"}}

	rep1, err := runner.Start(ctx, "batch-1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Start(ctx, "batch-1", manifest); !errors.Is(err, ErrBatchExists) {
		t.Fatalf("reusing batch ID: err = %v, want ErrBatchExists", err)
	}

	// Replace the source and start an explicit new batch: everything is
	// re-frozen and recomputed; batch-1 results are not reused.
	fs.Set("a", []byte("A2"))
	rep2, err := runner.Start(ctx, "batch-2", manifest)
	if err != nil {
		t.Fatal(err)
	}
	res1, res2 := rep1.ResultOf("a"), rep2.ResultOf("a")
	if string(res2.Output) != "A2" {
		t.Fatalf("new batch reused old result: %q", res2.Output)
	}
	if res1.InputDigest == res2.InputDigest {
		t.Fatal("new batch did not re-freeze the input digest")
	}
}

func TestDependenciesRequireSameBatchConfirmation(t *testing.T) {
	_, _, runner := newFixture(map[string]string{"a": "A", "b": "B", "c": "C"})
	manifest := []Item{
		{Path: "c", DependsOn: []string{"b"}},
		{Path: "a"},
		{Path: "b", DependsOn: []string{"a"}},
	}

	var order []string
	depSeen := map[string]map[string]string{}
	wrapped := runner.process
	runner.process = func(ctx context.Context, item Item, content []byte, deps map[string]Result) ([]byte, error) {
		order = append(order, item.Path)
		seen := map[string]string{}
		for name, res := range deps {
			seen[name] = string(res.Output)
		}
		depSeen[item.Path] = seen
		return wrapped(ctx, item, content, deps)
	}

	rep, err := runner.Start(ctx, "b1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b", "c"}) {
		t.Fatalf("processing order = %v, want [a b c]", order)
	}
	if got := depSeen["b"]; !reflect.DeepEqual(got, map[string]string{"a": "A"}) {
		t.Fatalf("b saw deps %v", got)
	}
	if got := depSeen["c"]; !reflect.DeepEqual(got, map[string]string{"b": "B<a:A>"}) {
		t.Fatalf("c saw deps %v", got)
	}
	if got := rep.StatusOf("c"); got != StatusDone {
		t.Fatalf("c status = %s", got)
	}
}

func TestMissingAtFreezeIsolatesSuccessorsOnly(t *testing.T) {
	_, _, runner := newFixture(map[string]string{"a": "A", "d": "D"})
	manifest := []Item{
		{Path: "a"},
		{Path: "gone", DependsOn: []string{"a"}},
		{Path: "c", DependsOn: []string{"gone"}},
		{Path: "d"},
	}
	rep, err := runner.Start(ctx, "b1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{"a": StatusDone, "gone": StatusIsolated, "c": StatusIsolated, "d": StatusDone}
	for path, status := range want {
		if got := rep.StatusOf(path); got != status {
			t.Fatalf("%s status = %s, want %s", path, got, status)
		}
	}
}

func TestManifestValidation(t *testing.T) {
	_, _, runner := newFixture(map[string]string{"a": "A", "b": "B"})
	cases := map[string][]Item{
		"cycle":       {{Path: "a", DependsOn: []string{"b"}}, {Path: "b", DependsOn: []string{"a"}}},
		"unknown dep": {{Path: "a", DependsOn: []string{"ghost"}}},
		"duplicate":   {{Path: "a"}, {Path: "a"}},
		"self depend": {{Path: "a", DependsOn: []string{"a"}}},
	}
	for name, manifest := range cases {
		if _, err := runner.Start(ctx, "bad-"+name, manifest); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestResumeUnknownBatch(t *testing.T) {
	_, _, runner := newFixture(nil)
	if _, err := runner.Resume(ctx, "nope"); !errors.Is(err, ErrBatchNotFound) {
		t.Fatalf("err = %v, want ErrBatchNotFound", err)
	}
}

func TestOutputConfirmationDetectsCorruption(t *testing.T) {
	_, store, runner := newFixture(map[string]string{"a": "A", "b": "B"})
	manifest := []Item{{Path: "a"}, {Path: "b"}}

	runner.process = failAfter(echoProcess, 1)
	if _, err := runner.Start(ctx, "b1", manifest); err == nil {
		t.Fatal("expected abort")
	}

	// Corrupt the stored output without updating its digest.
	cp, err := store.Load("b1")
	if err != nil {
		t.Fatal(err)
	}
	cp.Items["a"].Output = []byte("tampered")
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}

	runner.process = echoProcess
	if _, err := runner.Resume(ctx, "b1"); !errors.Is(err, ErrCheckpointCorrupt) {
		t.Fatalf("err = %v, want ErrCheckpointCorrupt", err)
	}
}

func TestDependencyVersionMismatchIsolates(t *testing.T) {
	// If the checkpoint's confirmed dependency result is somehow bound to a
	// different digest than the one frozen for the dependent, the dependent
	// must be isolated rather than mix input versions.
	_, store, runner := newFixture(map[string]string{"a": "A", "b": "B"})
	manifest := []Item{{Path: "a"}, {Path: "b", DependsOn: []string{"a"}}}

	runner.process = failAfter(echoProcess, 1) // process a, crash on b
	if _, err := runner.Start(ctx, "b1", manifest); err == nil {
		t.Fatal("expected abort")
	}

	cp, err := store.Load("b1")
	if err != nil {
		t.Fatal(err)
	}
	cp.Items["a"].InputDigest = digestOf([]byte("other-version"))
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}

	runner.process = echoProcess
	rep, err := runner.Resume(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.StatusOf("b"); got != StatusIsolated {
		t.Fatalf("b status = %s, want isolated on dep version mismatch", got)
	}
}
