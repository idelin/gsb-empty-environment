package batch_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"batchrun/batch"
)

var errCrash = errors.New("simulated crash")

// upperProc is a deterministic processor: uppercases the input and appends
// the outputs of its dependencies. It logs invocation order into log.
func upperProc(log *[]string) batch.Processor {
	return func(ctx context.Context, item batch.Item, deps []batch.DepResult) ([]byte, error) {
		*log = append(*log, item.Path)
		out := strings.ToUpper(string(item.Content))
		for _, d := range deps {
			out += "|" + d.Path + "=" + string(d.Output)
		}
		return []byte(out), nil
	}
}

func opts(log *[]string, deps map[string][]string) batch.Options {
	return batch.Options{
		SrcDir:   "src",
		OutDir:   "out",
		StateDir: "state",
		Deps:     deps,
		Process:  upperProc(log),
	}
}

func seed(fs *batch.MemFS, files map[string]string) {
	for p, c := range files {
		fs.WriteFile("src/"+p, []byte(c))
	}
}

func snapshot(t *testing.T, fs *batch.MemFS, dir string) map[string]string {
	t.Helper()
	paths, err := fs.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(dir + "/" + p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = string(data)
	}
	return out
}

func runAll(t *testing.T, fs *batch.MemFS, o batch.Options) (*batch.Runner, *batch.Summary) {
	t.Helper()
	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r, sum
}

// A file created after the freeze is ignored; only an explicit new batch
// picks it up, and the new batch recomputes everything from scratch.
func TestFreezeIgnoresNewFilesUntilNewBatch(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "hello", "b": "world"})
	var log []string
	o := opts(&log, nil)

	r, _ := runAll(t, fs, o)
	if got := snapshot(t, fs, "out"); !reflect.DeepEqual(got, map[string]string{"a": "HELLO", "b": "WORLD"}) {
		t.Fatalf("outputs: %v", got)
	}

	// New file appears after the freeze; restart must not auto-include it.
	fs.WriteFile("src/c", []byte("new"))
	log = nil
	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Ignored(), []string{"c"}) {
		t.Fatalf("ignored: %v", r.Ignored())
	}
	sum, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Processed) != 0 || len(log) != 0 {
		t.Fatalf("new file auto-included: processed=%v log=%v", sum.Processed, log)
	}
	if _, err := fs.ReadFile("out/c"); err == nil {
		t.Fatal("out/c must not exist before an explicit new batch")
	}

	// Explicit new batch: c joins the manifest and every item is recomputed
	// (no reuse of batch-1 results).
	if err := r.NewBatch(); err != nil {
		t.Fatal(err)
	}
	if r.BatchID() != 2 {
		t.Fatalf("batch id: %d", r.BatchID())
	}
	log = nil
	sum, err = r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 3 {
		t.Fatalf("new batch must recompute all items, log=%v", log)
	}
	if got := snapshot(t, fs, "out"); got["c"] != "NEW" || got["a"] != "HELLO" {
		t.Fatalf("outputs after new batch: %v", got)
	}
}

// Deleting a source file isolates that item and its transitive dependents;
// independent items keep going.
func TestSourceDeletedIsolatesDependents(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "aa", "b": "bb", "c": "cc"})
	deps := map[string][]string{"c": {"a"}}
	var log []string
	o := opts(&log, deps)

	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	fs.Remove("src/a") // deleted while a and c are still pending
	r, err = batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(log, []string{"b"}) {
		t.Fatalf("only independent item may run, log=%v", log)
	}
	if sum.Isolated["a"] != "source deleted" {
		t.Fatalf("a isolation: %v", sum.Isolated)
	}
	if !strings.Contains(sum.Isolated["c"], "isolated") {
		t.Fatalf("c must be isolated through its dependency: %v", sum.Isolated)
	}
	if rec, _ := r.Record("b"); rec.Status != batch.StatusDone {
		t.Fatalf("b record: %+v", rec)
	}
}

// Replacing a source file's content isolates it and its dependents, but a
// result already confirmed stays bound to the original digest.
func TestSourceModifiedBindingAndIsolation(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "aa", "b": "bb", "c": "cc"})
	deps := map[string][]string{"c": {"a"}}
	var log []string
	o := opts(&log, deps)

	// Process only a (crash right after its checkpoint persist).
	cut := 2 // seq 1 = freeze, seq 2 = item a
	o.AfterPersist = func(seq int) error {
		if seq == cut {
			return errCrash
		}
		return nil
	}
	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != errCrash {
		t.Fatalf("want crash, got %v", err)
	}
	frozenA, _ := r.Record("a")

	// a is done; modify both a (done) and b (pending) sources.
	fs.WriteFile("src/a", []byte("tampered-a"))
	fs.WriteFile("src/b", []byte("tampered-b"))

	o.AfterPersist = nil
	r, err = batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	recA, _ := r.Record("a")
	if recA.Status != batch.StatusDone || recA.InputDigest != frozenA.InputDigest {
		t.Fatalf("done result must stay bound to original digest: %+v", recA)
	}
	recB, _ := r.Record("b")
	if recB.Status != batch.StatusIsolated || recB.Reason != "source modified" {
		t.Fatalf("pending modified item must be isolated: %+v", recB)
	}
	log = nil
	sum, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// c still has its confirmed dependency a and must complete; b isolated.
	if !reflect.DeepEqual(log, []string{"c"}) {
		t.Fatalf("log=%v", log)
	}
	if sum.Isolated["b"] != "source modified" {
		t.Fatalf("isolated: %v", sum.Isolated)
	}
	// c consumed a's original result, not the tampered source.
	if got := snapshot(t, fs, "out"); got["c"] != "CC|a=AA" {
		t.Fatalf("c output: %v", got["c"])
	}
}

// Dependencies run after their same-batch dependencies are confirmed and
// receive their exact results.
func TestDependencyOrderAndResults(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "x", "b": "y", "c": "z"})
	deps := map[string][]string{"c": {"a", "b"}}
	var log []string
	var gotDeps []batch.DepResult
	o := opts(&log, deps)
	base := o.Process
	o.Process = func(ctx context.Context, item batch.Item, ds []batch.DepResult) ([]byte, error) {
		if item.Path == "c" {
			gotDeps = ds
		}
		return base(ctx, item, ds)
	}
	_, sum := runAll(t, fs, o)
	if !reflect.DeepEqual(log, []string{"a", "b", "c"}) {
		t.Fatalf("dependency-first order: %v", log)
	}
	if len(gotDeps) != 2 || string(gotDeps[0].Output) != "X" || string(gotDeps[1].Output) != "Y" {
		t.Fatalf("dep results: %+v", gotDeps)
	}
	if len(sum.Processed) != 3 {
		t.Fatalf("summary: %+v", sum)
	}
}

// The checkpoint binds outputs to input+dependency digests; tampering with
// an output forces recomputation of that item and its dependents only.
func TestCheckpointOutputConfirmation(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "aa", "b": "bb", "c": "cc"})
	deps := map[string][]string{"c": {"a"}}
	var log []string
	o := opts(&log, deps)
	r, _ := runAll(t, fs, o)

	recC, _ := r.Record("c")
	if recC.OutputDigest == "" || recC.DepDigests["a"] == "" || recC.InputDigest == "" {
		t.Fatalf("checkpoint must store output and dependency digests: %+v", recC)
	}

	fs.WriteFile("out/a", []byte("forged"))
	log = nil
	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := r.Record("a"); rec.Status != batch.StatusPending {
		t.Fatalf("tampered output must revert to pending: %+v", rec)
	}
	if rec, _ := r.Record("c"); rec.Status != batch.StatusPending {
		t.Fatalf("dependent of reverted item must be pending too: %+v", rec)
	}
	if rec, _ := r.Record("b"); rec.Status != batch.StatusDone {
		t.Fatalf("independent item stays done: %+v", rec)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(log, []string{"a", "c"}) {
		t.Fatalf("only a and c recomputed: %v", log)
	}
	if got := snapshot(t, fs, "out"); got["a"] != "AA" || got["c"] != "CC|a=AA" {
		t.Fatalf("outputs restored: %v", got)
	}
}

// Wiping the state directory forces a full recompute with identical results.
func TestFullRecompute(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "aa", "b": "bb", "c": "cc"})
	deps := map[string][]string{"c": {"a", "b"}}
	var log []string
	o := opts(&log, deps)
	runAll(t, fs, o)
	want := snapshot(t, fs, "out")

	for _, p := range []string{"a", "b", "c"} {
		fs.Remove("out/" + p)
	}
	fs.Remove("state/checkpoint.json")
	log = nil
	runAll(t, fs, o)
	if got := snapshot(t, fs, "out"); !reflect.DeepEqual(got, want) {
		t.Fatalf("full recompute mismatch:\nwant %v\ngot  %v", want, got)
	}
	if len(log) != 3 {
		t.Fatalf("full recompute must reprocess everything: %v", log)
	}
}

// Crashing at every checkpoint cut point and resuming must always converge
// to the same outputs as a crash-free run.
func TestFaultCutPoints(t *testing.T) {
	deps := map[string][]string{"c": {"a", "b"}}
	files := map[string]string{"a": "aa", "b": "bb", "c": "cc"}

	refFS := batch.NewMemFS()
	seed(refFS, files)
	var refLog []string
	runAll(t, refFS, opts(&refLog, deps))
	want := snapshot(t, refFS, "out")

	for cut := 1; cut <= 5; cut++ {
		t.Run(fmt.Sprintf("cut=%d", cut), func(t *testing.T) {
			fs := batch.NewMemFS()
			seed(fs, files)
			var log []string
			o := opts(&log, deps)
			o.AfterPersist = func(seq int) error {
				if seq == cut {
					return errCrash
				}
				return nil
			}
			// First attempt may crash during Open (freeze) or Run.
			r, err := batch.Open(fs, o)
			if err == nil {
				r.Run(context.Background())
			}
			// Resume without fault injection until completion.
			o.AfterPersist = nil
			r, err = batch.Open(fs, o)
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if _, err := r.Run(context.Background()); err != nil {
				t.Fatalf("resumed run: %v", err)
			}
			if got := snapshot(t, fs, "out"); !reflect.DeepEqual(got, want) {
				t.Fatalf("outputs after crash at cut %d:\nwant %v\ngot  %v", cut, want, got)
			}
			// A second resume is a no-op: everything already confirmed.
			log = nil
			r, err = batch.Open(fs, o)
			if err != nil {
				t.Fatal(err)
			}
			sum, err := r.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(sum.Processed) != 0 || len(log) != 0 {
				t.Fatalf("completed batch must not reprocess: %v %v", sum.Processed, log)
			}
		})
	}
}

// Results are never reused across batches, even for unchanged inputs.
func TestNoReuseAcrossBatches(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "aa"})
	var log []string
	o := opts(&log, nil)
	r, _ := runAll(t, fs, o)
	if err := r.NewBatch(); err != nil {
		t.Fatal(err)
	}
	log = nil
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(log, []string{"a"}) {
		t.Fatalf("unchanged input must still be recomputed in a new batch: %v", log)
	}
}

func TestManifestValidation(t *testing.T) {
	fs := batch.NewMemFS()
	seed(fs, map[string]string{"a": "1", "b": "2"})

	var log []string
	o := opts(&log, map[string][]string{"a": {"b"}, "b": {"a"}})
	if _, err := batch.Open(fs, o); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle must be rejected: %v", err)
	}

	fs2 := batch.NewMemFS()
	seed(fs2, map[string]string{"a": "1"})
	o2 := opts(&log, map[string][]string{"a": {"ghost"}})
	if _, err := batch.Open(fs2, o2); err == nil || !strings.Contains(err.Error(), "not in the manifest") {
		t.Fatalf("dangling dep must be rejected: %v", err)
	}
}
