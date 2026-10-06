package recovery

import (
	"errors"
	"strings"
	"testing"
)

// transform builds a deterministic Run function: output content is derived
// purely from the current input contents, so re-running an unaffected task
// always reproduces byte-identical outputs.
func transform(prefix string, inputs []string, output string) func(*MemFS) error {
	return func(fs *MemFS) error {
		var parts []string
		for _, in := range inputs {
			data, ok := fs.Read(in)
			if !ok {
				return errors.New("missing input " + in)
			}
			parts = append(parts, string(data))
		}
		fs.Write(output, []byte(prefix+"["+strings.Join(parts, "|")+"]"))
		return nil
	}
}

// stdGraph builds the test pipeline:
//
//	a.txt -> tA -> a.out -> tB -> b.out --+
//	                                      +-> tD -> d.out
//	b.txt -> tC -> c.out -----------------+
func stdGraph() []*Task {
	return []*Task{
		{Name: "tA", Inputs: []string{"a.txt"}, Outputs: []string{"a.out"}, Run: transform("A", []string{"a.txt"}, "a.out")},
		{Name: "tB", Inputs: []string{"a.out"}, Outputs: []string{"b.out"}, Run: transform("B", []string{"a.out"}, "b.out")},
		{Name: "tC", Inputs: []string{"b.txt"}, Outputs: []string{"c.out"}, Run: transform("C", []string{"b.txt"}, "c.out")},
		{Name: "tD", Inputs: []string{"b.out", "c.out"}, Outputs: []string{"d.out"}, Run: transform("D", []string{"b.out", "c.out"}, "d.out")},
	}
}

func stdSources() *MemFS {
	fs := NewMemFS()
	fs.Write("a.txt", []byte("a1"))
	fs.Write("b.txt", []byte("b1"))
	return fs
}

func mustEngine(t *testing.T, fs *MemFS, tasks []*Task, maxRetries int) *Engine {
	e, err := NewEngine(fs, tasks, maxRetries)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func TestFullBuildExecutesEachTaskOnce(t *testing.T) {
	e := mustEngine(t, stdSources(), stdGraph(), 0)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, name := range []string{"tA", "tB", "tC", "tD"} {
		if got := e.ExecCount(name); got != 1 {
			t.Errorf("ExecCount(%s) = %d, want 1", name, got)
		}
	}
	want := "D[B[A[a1]]|C[b1]]"
	if data, _ := e.fs.Read("d.out"); string(data) != want {
		t.Errorf("d.out = %q, want %q", data, want)
	}
}

func TestRecoverCorruptedOutputEarlyCutoff(t *testing.T) {
	e := mustEngine(t, stdSources(), stdGraph(), 0)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.ResetExecCounts()

	e.fs.Write("b.out", []byte("garbage")) // corrupt tB's output
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Only the corrupted task re-runs; its regenerated content is identical,
	// so the invalidation must not propagate to tD.
	want := map[string]int{"tA": 0, "tB": 1, "tC": 0, "tD": 0}
	for name, n := range want {
		if got := e.ExecCount(name); got != n {
			t.Errorf("ExecCount(%s) = %d, want %d", name, got, n)
		}
	}
	if data, _ := e.fs.Read("b.out"); string(data) != "B[A[a1]]" {
		t.Errorf("b.out = %q, want restored content", data)
	}
	if e.FileDigest("b.out") != e.RecordedOutputDigest("tB", "b.out") {
		t.Error("b.out digest not restored to recorded value")
	}
}

func TestRecoverCorruptedIntermediateStopsAtProducer(t *testing.T) {
	e := mustEngine(t, stdSources(), stdGraph(), 0)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.ResetExecCounts()

	e.fs.Write("a.out", []byte("corrupted")) // corrupt tA's output
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// tA restores a.out byte-identically, so tB and everything downstream
	// stays untouched.
	want := map[string]int{"tA": 1, "tB": 0, "tC": 0, "tD": 0}
	for name, n := range want {
		if got := e.ExecCount(name); got != n {
			t.Errorf("ExecCount(%s) = %d, want %d", name, got, n)
		}
	}
}

func TestRecoverDeletedOutputRerunsProducerOnly(t *testing.T) {
	e := mustEngine(t, stdSources(), stdGraph(), 0)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.ResetExecCounts()

	e.fs.Delete("c.out")
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	want := map[string]int{"tA": 0, "tB": 0, "tC": 1, "tD": 0}
	for name, n := range want {
		if got := e.ExecCount(name); got != n {
			t.Errorf("ExecCount(%s) = %d, want %d", name, got, n)
		}
	}
}

func TestRecoverSourceEditPropagatesAndMatchesFullRebuild(t *testing.T) {
	fs := stdSources()
	e := mustEngine(t, fs, stdGraph(), 0)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.ResetExecCounts()

	fs.Write("a.txt", []byte("a2")) // edit a source file
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// The change flows a.txt -> tA -> tB -> tD; the independent tC branch is
	// reused and never re-executed.
	want := map[string]int{"tA": 1, "tB": 1, "tC": 0, "tD": 1}
	for name, n := range want {
		if got := e.ExecCount(name); got != n {
			t.Errorf("ExecCount(%s) = %d, want %d", name, got, n)
		}
	}

	// Digest propagation check: the recorded digest of every output must
	// match the content now on disk.
	for _, out := range e.SortedOutputs() {
		if e.FileDigest(out) == "" {
			t.Errorf("output %s missing after recovery", out)
		}
	}
	for _, task := range stdGraph() {
		for _, out := range task.Outputs {
			if e.FileDigest(out) != e.RecordedOutputDigest(task.Name, out) {
				t.Errorf("digest mismatch for %s (task %s)", out, task.Name)
			}
		}
	}

	// Cross-check against a full rebuild from the same sources: incremental
	// recovery must converge to exactly the same content.
	fs2 := NewMemFS()
	fs2.Write("a.txt", []byte("a2"))
	fs2.Write("b.txt", []byte("b1"))
	full := mustEngine(t, fs2, stdGraph(), 0)
	if err := full.Build(); err != nil {
		t.Fatalf("full Build: %v", err)
	}
	for _, out := range e.SortedOutputs() {
		got, _ := fs.Read(out)
		want2, _ := fs2.Read(out)
		if string(got) != string(want2) {
			t.Errorf("output %s: recovery produced %q, full rebuild produced %q", out, got, want2)
		}
	}
	if data, _ := fs.Read("d.out"); string(data) != "D[B[A[a2]]|C[b1]]" {
		t.Errorf("d.out = %q after recovery", data)
	}
}

func TestFailureRetrySucceedsWithinBudget(t *testing.T) {
	fs := NewMemFS()
	fs.Write("in.txt", []byte("x"))
	attempts := 0
	flaky := &Task{
		Name:    "flaky",
		Inputs:  []string{"in.txt"},
		Outputs: []string{"out.txt"},
		Run: func(fs *MemFS) error {
			attempts++
			if attempts < 3 {
				return errors.New("transient failure")
			}
			fs.Write("out.txt", []byte("ok"))
			return nil
		},
	}
	e := mustEngine(t, fs, []*Task{flaky}, 2) // 1 initial + 2 retries
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := e.ExecCount("flaky"); got != 3 {
		t.Errorf("ExecCount(flaky) = %d, want 3 (2 failures + 1 success)", got)
	}
	if data, _ := fs.Read("out.txt"); string(data) != "ok" {
		t.Errorf("out.txt = %q, want %q", data, "ok")
	}
}

func TestFailureRetryExhaustedBlocksDownstream(t *testing.T) {
	fs := NewMemFS()
	fs.Write("in.txt", []byte("x"))
	broken := &Task{
		Name:    "broken",
		Inputs:  []string{"in.txt"},
		Outputs: []string{"mid.txt"},
		Run:     func(fs *MemFS) error { return errors.New("permanent failure") },
	}
	downstream := &Task{
		Name:    "downstream",
		Inputs:  []string{"mid.txt"},
		Outputs: []string{"final.txt"},
		Run:     transform("F", []string{"mid.txt"}, "final.txt"),
	}
	e := mustEngine(t, fs, []*Task{broken, downstream}, 2)
	if err := e.Build(); err == nil {
		t.Fatal("Build should fail when retries are exhausted")
	}
	if got := e.ExecCount("broken"); got != 3 {
		t.Errorf("ExecCount(broken) = %d, want 3 attempts", got)
	}
	if got := e.ExecCount("downstream"); got != 0 {
		t.Errorf("ExecCount(downstream) = %d, want 0 (must not run after upstream failure)", got)
	}
}

func TestRecoverRetriesFlakyTaskAndReusesIndependentBranch(t *testing.T) {
	fs := stdSources()
	tasks := stdGraph()
	e := mustEngine(t, fs, tasks, 1)
	if err := e.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.ResetExecCounts()

	// Make tB fail once before succeeding on the retry.
	tB := tasks[1]
	original := tB.Run
	failed := false
	tB.Run = func(fs *MemFS) error {
		if !failed {
			failed = true
			return errors.New("transient failure")
		}
		return original(fs)
	}

	fs.Write("a.txt", []byte("a2"))
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	want := map[string]int{"tA": 1, "tB": 2, "tC": 0, "tD": 1}
	for name, n := range want {
		if got := e.ExecCount(name); got != n {
			t.Errorf("ExecCount(%s) = %d, want %d", name, got, n)
		}
	}
	if data, _ := fs.Read("d.out"); string(data) != "D[B[A[a2]]|C[b1]]" {
		t.Errorf("d.out = %q after flaky recovery", data)
	}
}

func TestRecoverOnFreshStateEqualsFullBuild(t *testing.T) {
	e := mustEngine(t, stdSources(), stdGraph(), 0)
	if err := e.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, name := range []string{"tA", "tB", "tC", "tD"} {
		if got := e.ExecCount(name); got != 1 {
			t.Errorf("ExecCount(%s) = %d, want 1", name, got)
		}
	}
}

func TestValidationRejectsCyclesAndDuplicateOutputs(t *testing.T) {
	fs := NewMemFS()
	noop := func(fs *MemFS) error { return nil }
	cyclic := []*Task{
		{Name: "x", Inputs: []string{"y.out"}, Outputs: []string{"x.out"}, Run: noop},
		{Name: "y", Inputs: []string{"x.out"}, Outputs: []string{"y.out"}, Run: noop},
	}
	if _, err := NewEngine(fs, cyclic, 0); err == nil {
		t.Error("expected cycle detection error")
	}
	dup := []*Task{
		{Name: "p", Outputs: []string{"same.out"}, Run: noop},
		{Name: "q", Outputs: []string{"same.out"}, Run: noop},
	}
	if _, err := NewEngine(fs, dup, 0); err == nil {
		t.Error("expected duplicate output error")
	}
}
