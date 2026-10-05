package incbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func identity(s string) string { return s }

// concatTask builds a task that writes transform(concat(inputs)) to outputs[0].
func concatTask(name string, inputs, orderOnly, outputs []string, transform func(string) string) *Task {
	return &Task{
		Name:      name,
		Cmd:       "concat:" + name,
		Inputs:    inputs,
		OrderOnly: orderOnly,
		Outputs:   outputs,
		Run: func(rc *RunContext) error {
			var data string
			for _, f := range inputs {
				b, err := os.ReadFile(filepath.Join(rc.Dir, f))
				if err != nil {
					return err
				}
				data += string(b)
			}
			return os.WriteFile(filepath.Join(rc.Dir, outputs[0]), []byte(transform(data)), 0o644)
		},
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// Diamond: a.txt -> {gen-b, gen-c} -> gen-d. Changing a.txt must rebuild
// all three, each with a shortest reason chain rooted at a.txt.
func TestDiamondDependency(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a1")
	eng := &Engine{Dir: dir, Store: NewStore(), Tasks: []*Task{
		concatTask("gen-b", []string{"a.txt"}, nil, []string{"b.txt"}, func(s string) string { return s + "B" }),
		concatTask("gen-c", []string{"a.txt"}, nil, []string{"c.txt"}, func(s string) string { return s + "C" }),
		concatTask("gen-d", []string{"b.txt", "c.txt"}, nil, []string{"d.txt"}, identity),
	}}

	r1, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r1.Rebuilt, []string{"gen-b", "gen-c", "gen-d"}) {
		t.Fatalf("cold build rebuilt %v", r1.Rebuilt)
	}

	r2, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Rebuilt) != 0 {
		t.Fatalf("no-op build rebuilt %v", r2.Rebuilt)
	}

	writeFile(t, dir, "a.txt", "a2")
	r3, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r3.Rebuilt, []string{"gen-b", "gen-c", "gen-d"}) {
		t.Fatalf("after source change rebuilt %v", r3.Rebuilt)
	}
	if got := readFile(t, dir, "d.txt"); got != "a2Ba2C" {
		t.Fatalf("d.txt = %q, want %q", got, "a2Ba2C")
	}

	// Shortest chain for gen-d: a.txt -> gen-b -> b.txt -> gen-d (4 steps).
	d := r3.Reasons["gen-d"]
	if len(d.Steps) != 4 {
		t.Fatalf("gen-d chain has %d steps, want 4:\n%s", len(d.Steps), d)
	}
	if !contains(d.Steps[0], `"a.txt"`) || !contains(d.Steps[1], `"gen-b"`) ||
		!contains(d.Steps[2], `"b.txt"`) || !contains(d.Steps[3], `"gen-d"`) {
		t.Fatalf("unexpected gen-d chain:\n%s", d)
	}
	if got := len(r3.Reasons["gen-b"].Steps); got != 2 {
		t.Fatalf("gen-b chain has %d steps, want 2", got)
	}

	r4, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(r4.Rebuilt) != 0 {
		t.Fatalf("post-rebuild no-op build rebuilt %v", r4.Rebuilt)
	}
}

// Order-only dependencies constrain scheduling but never propagate
// invalidation.
func TestOrderOnlyDoesNotPropagate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "s1")
	writeFile(t, dir, "marker.txt", "m1")
	eng := &Engine{Dir: dir, Store: NewStore(), Tasks: []*Task{
		concatTask("gen", []string{"src.txt"}, []string{"marker.txt"}, []string{"out.txt"}, identity),
	}}

	if r, err := eng.Build(); err != nil || len(r.Rebuilt) != 1 {
		t.Fatalf("cold build: %v %v", r, err)
	}

	writeFile(t, dir, "marker.txt", "m2")
	r, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rebuilt) != 0 {
		t.Fatalf("order-only change rebuilt %v, want nothing", r.Rebuilt)
	}

	writeFile(t, dir, "src.txt", "s2")
	r, err = eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r.Rebuilt, []string{"gen"}) {
		t.Fatalf("content change rebuilt %v", r.Rebuilt)
	}
}

// Generated files: if a re-run task produces identical output content,
// downstream tasks are not invalidated (restat semantics).
func TestGeneratedFileRestat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a1")
	genA := &Task{
		Name: "gen-a", Cmd: "const", Inputs: []string{"a.txt"}, Outputs: []string{"b.txt"},
		Run: func(rc *RunContext) error {
			return os.WriteFile(filepath.Join(rc.Dir, "b.txt"), []byte("CONST"), 0o644)
		},
	}
	genC := concatTask("gen-c", []string{"b.txt"}, nil, []string{"c.txt"}, identity)
	eng := &Engine{Dir: dir, Store: NewStore(), Tasks: []*Task{genA, genC}}

	if r, err := eng.Build(); err != nil || len(r.Rebuilt) != 2 {
		t.Fatalf("cold build: %v %v", r, err)
	}

	writeFile(t, dir, "a.txt", "a2")
	r, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r.Rebuilt, []string{"gen-a"}) {
		t.Fatalf("rebuilt %v, want only [gen-a] (b.txt content unchanged)", r.Rebuilt)
	}

	// Deleting a generated file rebuilds exactly its producer.
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	r, err = eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r.Rebuilt, []string{"gen-a"}) {
		t.Fatalf("after deleting b.txt rebuilt %v, want [gen-a]", r.Rebuilt)
	}
}

// A source file modified while a task is running must not be used: the
// stale result is discarded and the task re-runs.
func TestSourceChangesDuringTaskRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "v1")
	runs := 0
	armMutation := false
	slow := &Task{
		Name: "slow", Cmd: "slow", Inputs: []string{"src.txt"}, Outputs: []string{"out.txt"},
		Run: func(rc *RunContext) error {
			runs++
			data, err := os.ReadFile(filepath.Join(rc.Dir, "src.txt"))
			if err != nil {
				return err
			}
			if armMutation {
				armMutation = false
				// Simulate a concurrent external edit mid-run.
				if err := os.WriteFile(filepath.Join(rc.Dir, "src.txt"), []byte("v2"), 0o644); err != nil {
					return err
				}
			}
			return os.WriteFile(filepath.Join(rc.Dir, "out.txt"), data, 0o644)
		},
	}
	eng := &Engine{Dir: dir, Store: NewStore(), Tasks: []*Task{slow}}

	if _, err := eng.Build(); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("cold build ran task %d times", runs)
	}

	// Force a re-run (output lost), then mutate src.txt during the run.
	if err := os.Remove(filepath.Join(dir, "out.txt")); err != nil {
		t.Fatal(err)
	}
	armMutation = true
	r, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if r.Runs["slow"] != 2 {
		t.Fatalf("slow ran %d times, want 2 (stale result discarded)", r.Runs["slow"])
	}
	if !equalStrings(r.StaleDiscarded, []string{"slow"}) {
		t.Fatalf("StaleDiscarded = %v, want [slow]", r.StaleDiscarded)
	}
	if got := readFile(t, dir, "out.txt"); got != "v2" {
		t.Fatalf("out.txt = %q, want %q: stale result was used", got, "v2")
	}

	// State must be consistent afterwards: nothing left to rebuild.
	r, err = eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rebuilt) != 0 {
		t.Fatalf("post-recovery build rebuilt %v", r.Rebuilt)
	}
}

// Dynamic discovery: a task discovers a content dependency at run time.
// The discovered edge re-schedules the build (the producer is ordered
// before the consumer), and a result computed before the discovered
// dependency was available is discarded rather than used.
func TestDynamicDiscoveryReschedules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.txt", "base")
	writeFile(t, dir, "manifest.txt", "mod.txt")
	writeFile(t, dir, "mod-src.txt", "v1")

	genmod := concatTask("genmod", []string{"mod-src.txt"}, nil, []string{"mod.txt"}, identity)
	compile := &Task{
		Name: "compile", Cmd: "compile",
		Inputs:  []string{"manifest.txt", "base.txt"},
		Outputs: []string{"out.txt"},
		Run: func(rc *RunContext) error {
			data := readFile(t, rc.Dir, "base.txt")
			for _, dep := range strings.Fields(readFile(t, rc.Dir, "manifest.txt")) {
				rc.Discover(dep)
				b, err := os.ReadFile(filepath.Join(rc.Dir, dep))
				if os.IsNotExist(err) {
					b = nil // dep not generated yet on the first pass
				} else if err != nil {
					return err
				}
				data += string(b)
			}
			return os.WriteFile(filepath.Join(rc.Dir, "out.txt"), []byte(data), 0o644)
		},
	}
	eng := &Engine{Dir: dir, Store: NewStore(), Tasks: []*Task{compile, genmod}}

	// Cold build: compile runs before genmod is scheduled (no edge known
	// yet), discovers mod.txt, and its first (incomplete) result must be
	// discarded and re-computed after genmod runs.
	r1, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if r1.Runs["compile"] != 2 {
		t.Fatalf("compile ran %d times, want 2 (re-scheduled after discovery)", r1.Runs["compile"])
	}
	if r1.Runs["genmod"] != 1 {
		t.Fatalf("genmod ran %d times, want 1", r1.Runs["genmod"])
	}
	if !equalStrings(r1.StaleDiscarded, []string{"compile"}) {
		t.Fatalf("StaleDiscarded = %v, want [compile]", r1.StaleDiscarded)
	}
	if got := readFile(t, dir, "out.txt"); got != "basev1" {
		t.Fatalf("out.txt = %q, want %q: stale result was used", got, "basev1")
	}

	// Changing the discovered dependency's source propagates through the
	// discovered edge: genmod first, then compile.
	writeFile(t, dir, "mod-src.txt", "v2")
	r2, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(r2.Rebuilt, []string{"genmod", "compile"}) {
		t.Fatalf("rebuilt %v, want [genmod compile]", r2.Rebuilt)
	}
	if got := readFile(t, dir, "out.txt"); got != "basev2" {
		t.Fatalf("out.txt = %q, want %q", got, "basev2")
	}
	c := r2.Reasons["compile"]
	if len(c.Steps) != 4 || !contains(c.Steps[0], `"mod-src.txt"`) ||
		!contains(c.Steps[1], `"genmod"`) || !contains(c.Steps[2], `"mod.txt"`) ||
		!contains(c.Steps[3], `"compile"`) {
		t.Fatalf("unexpected compile chain:\n%s", c)
	}

	r3, err := eng.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(r3.Rebuilt) != 0 {
		t.Fatalf("no-op build rebuilt %v", r3.Rebuilt)
	}
}
