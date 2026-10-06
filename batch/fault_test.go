package batch

import (
	"errors"
	"reflect"
	"testing"
)

// flakyStore fails the Save calls whose 1-based indices are in failAt.
type flakyStore struct {
	inner  *MemStore
	failAt map[int]bool
	calls  int
}

func (f *flakyStore) Save(cp *Checkpoint) error {
	f.calls++
	if f.failAt[f.calls] {
		return errors.New("injected save failure")
	}
	return f.inner.Save(cp)
}

func (f *flakyStore) Load(batchID string) (*Checkpoint, error) {
	return f.inner.Load(batchID)
}

// runToCompletion drives a batch to completion across crashes, retrying
// Start when the freeze checkpoint was lost and Resume otherwise.
func runToCompletion(t *testing.T, runner *Runner, batchID string, manifest []Item) *Report {
	t.Helper()
	rep, err := runner.Start(ctx, batchID, manifest)
	for i := 0; err != nil && i < 100; i++ {
		if errors.Is(err, ErrBatchExists) {
			rep, err = runner.Resume(ctx, batchID)
			continue
		}
		// Freeze checkpoint lost (or other transient failure): retry.
		rep, err = runner.Start(ctx, batchID, manifest)
		if err == nil || !errors.Is(err, ErrBatchExists) {
			continue
		}
		rep, err = runner.Resume(ctx, batchID)
	}
	if err != nil {
		t.Fatalf("batch never completed: %v", err)
	}
	return rep
}

func chainManifest() []Item {
	return []Item{
		{Path: "a"},
		{Path: "b", DependsOn: []string{"a"}},
		{Path: "c", DependsOn: []string{"b"}},
		{Path: "d"},
	}
}

func chainFiles() map[string]string {
	return map[string]string{"a": "A", "b": "B", "c": "C", "d": "D"}
}

// TestFaultInjectionAtEverySavePoint kills the batch at each checkpoint
// save cut point in turn, then verifies the resumed run matches a clean
// full recompute.
func TestFaultInjectionAtEverySavePoint(t *testing.T) {
	manifest := chainManifest()

	// Reference: clean full recompute.
	_, _, refRunner := newFixture(chainFiles())
	ref := runToCompletion(t, refRunner, "ref", manifest)

	// Count the save points of a clean run: 1 freeze + 1 per done item.
	probe := &flakyStore{inner: NewMemStore()}
	probeRunner := NewRunner(mustFS(chainFiles()), probe, echoProcess)
	runToCompletion(t, probeRunner, "probe", manifest)
	savePoints := probe.calls
	if savePoints != 5 {
		t.Fatalf("expected 5 save points, got %d", savePoints)
	}

	for cut := 1; cut <= savePoints; cut++ {
		store := &flakyStore{inner: NewMemStore(), failAt: map[int]bool{cut: true}}
		runner := NewRunner(mustFS(chainFiles()), store, echoProcess)
		rep := runToCompletion(t, runner, "b1", manifest)
		if !reflect.DeepEqual(resultMap(rep), resultMap(ref)) {
			t.Fatalf("cut at save %d: results differ from full recompute\n got: %v\nwant: %v",
				cut, resultMap(rep), resultMap(ref))
		}
		for _, it := range rep.Items {
			if it.Status != StatusDone {
				t.Fatalf("cut at save %d: item %s not done (%s)", cut, it.Path, it.Reason)
			}
		}
	}
}

// TestFaultInjectionInProcessor crashes inside the ProcessFunc at every
// position, then verifies the resumed run matches a full recompute.
func TestFaultInjectionInProcessor(t *testing.T) {
	manifest := chainManifest()

	_, _, refRunner := newFixture(chainFiles())
	ref := runToCompletion(t, refRunner, "ref", manifest)

	for crashAt := 0; crashAt < len(manifest); crashAt++ {
		_, _, runner := newFixture(chainFiles())
		runner.process = failAfter(echoProcess, crashAt)
		if _, err := runner.Start(ctx, "b1", manifest); err == nil {
			t.Fatalf("crashAt=%d: expected abort", crashAt)
		}
		runner.process = echoProcess
		rep, err := runner.Resume(ctx, "b1")
		if err != nil {
			t.Fatalf("crashAt=%d: resume: %v", crashAt, err)
		}
		if !reflect.DeepEqual(resultMap(rep), resultMap(ref)) {
			t.Fatalf("crashAt=%d: results differ from full recompute\n got: %v\nwant: %v",
				crashAt, resultMap(rep), resultMap(ref))
		}
	}
}

// TestCheckpointRecordsDependencyDigests verifies the checkpoint pins both
// input digests and per-dependency digests at freeze time.
func TestCheckpointRecordsDependencyDigests(t *testing.T) {
	_, store, runner := newFixture(chainFiles())
	if _, err := runner.Start(ctx, "b1", chainManifest()); err != nil {
		t.Fatal(err)
	}
	cp, err := store.Load("b1")
	if err != nil {
		t.Fatal(err)
	}
	c := cp.Items["c"]
	if c.DepDigests["b"] != cp.Items["b"].InputDigest {
		t.Fatalf("dep digest not pinned: %q vs %q", c.DepDigests["b"], cp.Items["b"].InputDigest)
	}
	if c.OutputDigest != digestOf(c.Output) {
		t.Fatal("output confirmation digest inconsistent")
	}
	if cp.Items["a"].InputDigest != digestOf([]byte("A")) {
		t.Fatal("input digest not frozen")
	}
}

func mustFS(files map[string]string) *MemFS {
	fs := NewMemFS()
	for path, content := range files {
		fs.Set(path, []byte(content))
	}
	return fs
}
