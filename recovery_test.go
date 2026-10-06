package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// counter records how many times each task body has executed.
type counter map[string]int

func (c counter) wrap(name string, fn func(Input) ([]byte, error)) func(Input) ([]byte, error) {
	return func(in Input) ([]byte, error) {
		c[name]++
		return fn(in)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// diamond builds the graph used by most tests:
//
//	A(a.txt) --\
//	             C -- D
//	B(b.txt) --/
//	E(e.txt)   (independent branch)
func diamond(dir string, c counter) []Task {
	file := func(name string) string { return filepath.Join(dir, name) }
	return []Task{
		{Name: "A", Files: []string{file("a.txt")}, Run: c.wrap("A", func(in Input) ([]byte, error) {
			b, _ := in.File(file("a.txt"))
			return []byte("A:" + string(b)), nil
		})},
		{Name: "B", Files: []string{file("b.txt")}, Run: c.wrap("B", func(in Input) ([]byte, error) {
			b, _ := in.File(file("b.txt"))
			return []byte("B:" + string(b)), nil
		})},
		{Name: "C", Deps: []string{"A", "B"}, Run: c.wrap("C", func(in Input) ([]byte, error) {
			a, _ := in.Output("A")
			b, _ := in.Output("B")
			return []byte(fmt.Sprintf("C(%s,%s)", a, b)), nil
		})},
		{Name: "D", Deps: []string{"C"}, Run: c.wrap("D", func(in Input) ([]byte, error) {
			b, _ := in.Output("C")
			return []byte("D(" + string(b) + ")"), nil
		})},
		{Name: "E", Files: []string{file("e.txt")}, Run: c.wrap("E", func(in Input) ([]byte, error) {
			b, _ := in.File(file("e.txt"))
			return []byte("E:" + string(b)), nil
		})},
	}
}

func seedFiles(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "a.txt"), "a1")
	writeFile(t, filepath.Join(dir, "b.txt"), "b1")
	writeFile(t, filepath.Join(dir, "e.txt"), "e1")
}

func TestFullRebuildThenReuse(t *testing.T) {
	dir := t.TempDir()
	seedFiles(t, dir)
	c := counter{}
	p := &Planner{Store: NewMemStore()}

	rep, err := p.Run(diamond(dir, c))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A", "B", "C", "D", "E"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("full rebuild ran %v, want %v", rep.Ran, want)
	}
	for name, n := range c {
		if n != 1 {
			t.Fatalf("task %s ran %d times, want 1", name, n)
		}
	}

	rep, err = p.Run(diamond(dir, c))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Ran) != 0 || len(rep.Reused) != 5 {
		t.Fatalf("second run: ran %v reused %v, want nothing ran and 5 reused", rep.Ran, rep.Reused)
	}
	for name, n := range c {
		if n != 1 {
			t.Fatalf("task %s ran %d times after no-op run, want 1", name, n)
		}
	}
}

func TestCorruptFileRerunsOnlyAffectedBranch(t *testing.T) {
	dir := t.TempDir()
	seedFiles(t, dir)
	c := counter{}
	p := &Planner{Store: NewMemStore()}
	if _, err := p.Run(diamond(dir, c)); err != nil {
		t.Fatal(err)
	}

	// Corrupt a.txt: only A and its transitive dependents C, D re-run.
	writeFile(t, filepath.Join(dir, "a.txt"), "a2-corrupted")
	rep, err := p.Run(diamond(dir, c))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A", "C", "D"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("ran %v, want %v", rep.Ran, want)
	}
	if want := []string{"B", "E"}; !reflect.DeepEqual(rep.Reused, want) {
		t.Fatalf("reused %v, want %v", rep.Reused, want)
	}
	want := counter{"A": 2, "B": 1, "C": 2, "D": 2, "E": 1}
	if !reflect.DeepEqual(map[string]int(c), map[string]int(want)) {
		t.Fatalf("execution counts %v, want %v", c, want)
	}
}

func TestIdenticalRerunOutputStopsInvalidation(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	writeFile(t, a, "  Hello World ")
	c := counter{}

	// Task A normalizes its input, so cosmetically different file
	// content can still yield byte-identical output.
	normalize := func(in Input) ([]byte, error) {
		b, _ := in.File(a)
		return []byte(strings.ToUpper(strings.TrimSpace(string(b)))), nil
	}
	downstream := func(in Input) ([]byte, error) {
		b, _ := in.Output("A")
		return []byte("[" + string(b) + "]"), nil
	}
	tasks := func() []Task {
		return []Task{
			{Name: "A", Files: []string{a}, Run: c.wrap("A", normalize)},
			{Name: "B", Deps: []string{"A"}, Run: c.wrap("B", downstream)},
		}
	}

	p := &Planner{Store: NewMemStore()}
	if _, err := p.Run(tasks()); err != nil {
		t.Fatal(err)
	}

	// Touch the file: new bytes, same normalized content.
	writeFile(t, a, "HELLO WORLD")
	rep, err := p.Run(tasks())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("ran %v, want %v", rep.Ran, want)
	}
	if want := []string{"B"}; !reflect.DeepEqual(rep.Reused, want) {
		t.Fatalf("reused %v, want %v: identical output must block invalidation", rep.Reused, want)
	}
	if c["A"] != 2 || c["B"] != 1 {
		t.Fatalf("counts %v, want A=2 B=1", c)
	}
}

func TestFailureBlocksDependentsAndIsRetried(t *testing.T) {
	dir := t.TempDir()
	seedFiles(t, dir)
	c := counter{}
	p := &Planner{Store: NewMemStore()}

	// Poison a.txt so A fails.
	writeFile(t, filepath.Join(dir, "a.txt"), "bad")
	tasks := diamond(dir, c)
	tasks[0].Run = c.wrap("A", func(in Input) ([]byte, error) {
		b, _ := in.File(filepath.Join(dir, "a.txt"))
		if string(b) == "bad" {
			return nil, fmt.Errorf("corrupt input")
		}
		return []byte("A:" + string(b)), nil
	})

	rep, err := p.Run(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A"}; !reflect.DeepEqual(rep.Failed, want) {
		t.Fatalf("failed %v, want %v", rep.Failed, want)
	}
	if want := []string{"C", "D"}; !reflect.DeepEqual(rep.Blocked, want) {
		t.Fatalf("blocked %v, want %v", rep.Blocked, want)
	}
	if want := []string{"B", "E"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("ran %v, want %v", rep.Ran, want)
	}

	// Inputs unchanged: the failed task must be retried, not cached.
	rep, err = p.Run(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if c["A"] != 2 {
		t.Fatalf("A attempted %d times, want 2 (failed tasks are retried)", c["A"])
	}
	if want := []string{"B", "E"}; !reflect.DeepEqual(rep.Reused, want) {
		t.Fatalf("reused %v, want %v", rep.Reused, want)
	}

	// Repair the file: A succeeds, blocked dependents catch up.
	writeFile(t, filepath.Join(dir, "a.txt"), "a-good")
	rep, err = p.Run(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A", "C", "D"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("ran %v, want %v", rep.Ran, want)
	}
	want := counter{"A": 3, "B": 1, "C": 1, "D": 1, "E": 1}
	if !reflect.DeepEqual(map[string]int(c), map[string]int(want)) {
		t.Fatalf("execution counts %v, want %v", c, want)
	}
}

func TestWithinRunRetries(t *testing.T) {
	c := counter{}
	flaky := c.wrap("flaky", func(Input) ([]byte, error) {
		if c["flaky"] == 1 {
			return nil, fmt.Errorf("transient failure")
		}
		return []byte("ok"), nil
	})
	tasks := []Task{{Name: "flaky", Run: flaky}}

	// No retries: single attempt, task fails.
	rep, err := (&Planner{Store: NewMemStore()}).Run(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 1 || c["flaky"] != 1 {
		t.Fatalf("no-retry run: failed %v attempts %d", rep.Failed, c["flaky"])
	}

	// One retry: second attempt succeeds within the same run.
	c["flaky"] = 0
	rep, err = (&Planner{Store: NewMemStore(), Retries: 1}).Run(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"flaky"}; !reflect.DeepEqual(rep.Ran, want) {
		t.Fatalf("ran %v, want %v", rep.Ran, want)
	}
	if c["flaky"] != 2 {
		t.Fatalf("flaky attempted %d times, want 2", c["flaky"])
	}
}

func TestIncrementalDigestsMatchFullRebuild(t *testing.T) {
	dir := t.TempDir()
	seedFiles(t, dir)
	c := counter{}
	store := NewMemStore()
	p := &Planner{Store: store}

	if _, err := p.Run(diamond(dir, c)); err != nil {
		t.Fatal(err)
	}
	// Corrupt files and re-run incrementally.
	writeFile(t, filepath.Join(dir, "a.txt"), "a2")
	writeFile(t, filepath.Join(dir, "e.txt"), "e2")
	if _, err := p.Run(diamond(dir, c)); err != nil {
		t.Fatal(err)
	}

	incremental := map[string]Record{}
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		rec, ok := store.Get(name)
		if !ok || !rec.OK {
			t.Fatalf("missing/failed record for %s", name)
		}
		incremental[name] = rec
	}

	// Full rebuild from an empty store must reproduce identical digests.
	rebuilt := NewMemStore()
	rep, err := (&Planner{Store: rebuilt}).Run(diamond(dir, counter{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Ran) != 5 {
		t.Fatalf("full rebuild ran %v, want all 5 tasks", rep.Ran)
	}
	for name, rec := range incremental {
		got, _ := rebuilt.Get(name)
		if got.OutputDigest != rec.OutputDigest || got.InputDigest != rec.InputDigest {
			t.Fatalf("task %s digest mismatch: incremental %+v vs rebuild %+v", name, rec, got)
		}
	}
}

func TestFileStoreRecoveryAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	seedFiles(t, dir)
	path := filepath.Join(dir, "store.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := counter{}
	if _, err := (&Planner{Store: store}).Run(diamond(dir, c)); err != nil {
		t.Fatal(err)
	}

	// Simulate a process restart: fresh planner, same backing file.
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := (&Planner{Store: reopened}).Run(diamond(dir, c))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Ran) != 0 || len(rep.Reused) != 5 {
		t.Fatalf("after restart: ran %v reused %v, want full reuse", rep.Ran, rep.Reused)
	}
	for name, n := range c {
		if n != 1 {
			t.Fatalf("task %s ran %d times, want 1", name, n)
		}
	}
}

func TestGraphValidation(t *testing.T) {
	run := func(in Input) ([]byte, error) { return nil, nil }
	p := &Planner{Store: NewMemStore()}

	if _, err := p.Run([]Task{
		{Name: "A", Deps: []string{"B"}, Run: run},
		{Name: "B", Deps: []string{"A"}, Run: run},
	}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: got %v", err)
	}
	if _, err := p.Run([]Task{
		{Name: "A", Deps: []string{"nope"}, Run: run},
	}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown dep: got %v", err)
	}
	if _, err := p.Run([]Task{
		{Name: "A", Run: run}, {Name: "A", Run: run},
	}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate: got %v", err)
	}
}
