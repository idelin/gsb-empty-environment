package planner

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRoot(t *testing.T) string {
	root, err := ioutil.TempDir("", "incbuild-test")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, name, content string) {
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, name string) string {
	data, err := ioutil.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// copyTask returns a task that concatenates its content-dep inputs into its output.
func copyTask(name, out string, reads []string, extraDeps []Dep, runs *int) *Task {
	deps := append([]Dep{}, extraDeps...)
	for _, f := range reads {
		deps = append(deps, ContentDep(f))
	}
	return &Task{
		Name:    name,
		Deps:    deps,
		Outputs: []string{out},
		Run: func(ctx *RunContext) error {
			*runs++
			var b strings.Builder
			for _, f := range reads {
				data, err := ctx.ReadFile(f)
				if err != nil {
					return err
				}
				b.Write(data)
			}
			return ctx.WriteFile(out, []byte(name+"("+b.String()+")"))
		},
	}
}

func rebuiltSet(res *Result) map[string]bool {
	set := map[string]bool{}
	for _, n := range res.Rebuilt {
		set[n] = true
	}
	return set
}

// Diamond with one skewed leg:
//
//	in.txt --> B --> b.txt --> E --> e.txt --+
//	 |                                       +--> D --> d.txt
//	 +----------------> C --> c.txt ---------+
//
// The shortest reason chain for D goes through C (depth 3), not through E.
func TestDiamondAndShortestReasonChain(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "in.txt", "v1")

	var runsB, runsC, runsD, runsE int
	p := New(root)
	p.AddTask(copyTask("B", "b.txt", []string{"in.txt"}, nil, &runsB))
	p.AddTask(copyTask("E", "e.txt", []string{"b.txt"}, nil, &runsE))
	p.AddTask(copyTask("C", "c.txt", []string{"in.txt"}, nil, &runsC))
	p.AddTask(copyTask("D", "d.txt", []string{"e.txt", "c.txt"}, nil, &runsD))

	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 4 {
		t.Fatalf("first build should run all 4 tasks, got %v", res.Rebuilt)
	}

	// No changes: nothing rebuilds.
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 0 {
		t.Fatalf("no-op build rebuilt %v", res.Rebuilt)
	}

	// Change the root source: the whole diamond rebuilds, D exactly once.
	write(t, root, "in.txt", "v2")
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsB != 2 || runsC != 2 || runsE != 2 || runsD != 2 {
		t.Fatalf("each task should have run exactly twice, got B=%d C=%d E=%d D=%d", runsB, runsC, runsE, runsD)
	}
	if got := read(t, root, "d.txt"); got != "D(E(B(v2))C(v2))" {
		t.Fatalf("unexpected d.txt: %q", got)
	}

	// D's shortest reason chain must go through C (depth 3), not E (depth 4).
	reason := res.Reasons["D"]
	if reason == nil {
		t.Fatal("no reason recorded for D")
	}
	if reason.Depth() != 3 {
		t.Fatalf("expected shortest chain of depth 3, got %d: %s", reason.Depth(), reason)
	}
	chain := strings.Join(reason.Chain(), " | ")
	if !strings.Contains(chain, "c.txt") || strings.Contains(chain, "e.txt") {
		t.Fatalf("shortest chain should go through c.txt, got: %s", chain)
	}
	if !strings.Contains(chain, "in.txt: source file changed on disk") {
		t.Fatalf("chain should end at the root cause, got: %s", chain)
	}
	t.Logf("D reason chain: %s", reason)
}

// Order-only dependencies must order the schedule but never propagate
// invalidation.
func TestOrderOnlyDependency(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "p-in.txt", "p1")
	write(t, root, "q-in.txt", "q1")

	var runsP, runsQ int
	var execOrder []string
	p := New(root)
	p.AddTask(&Task{
		Name:    "P",
		Outputs: []string{"p.txt"},
		Run: func(ctx *RunContext) error {
			runsP++
			execOrder = append(execOrder, "P")
			data, err := ctx.ReadFile("p-in.txt")
			if err != nil {
				return err
			}
			return ctx.WriteFile("p.txt", data)
		},
	})
	p.AddTask(&Task{
		Name:    "Q",
		Deps:    []Dep{ContentDep("q-in.txt"), OrderDep("p.txt")},
		Outputs: []string{"q.txt"},
		Run: func(ctx *RunContext) error {
			runsQ++
			execOrder = append(execOrder, "Q")
			data, err := ctx.ReadFile("q-in.txt")
			if err != nil {
				return err
			}
			return ctx.WriteFile("q.txt", data)
		},
	})

	if _, err := p.Build(); err != nil {
		t.Fatal(err)
	}

	// Change only P's input: P rebuilds, Q must NOT rebuild (order-only edge).
	write(t, root, "p-in.txt", "p2")
	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsP != 2 {
		t.Fatalf("P should have rebuilt, runs=%d", runsP)
	}
	if runsQ != 1 {
		t.Fatalf("Q must not rebuild on order-only dep change, runs=%d", runsQ)
	}
	if rebuiltSet(res)["Q"] {
		t.Fatalf("Q appears in rebuild set: %v", res.Rebuilt)
	}

	// Change both inputs: both rebuild, and P must run before Q.
	write(t, root, "p-in.txt", "p3")
	write(t, root, "q-in.txt", "q2")
	execOrder = nil
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsP != 3 || runsQ != 2 {
		t.Fatalf("expected both to rebuild, P=%d Q=%d", runsP, runsQ)
	}
	if len(execOrder) != 2 || execOrder[0] != "P" || execOrder[1] != "Q" {
		t.Fatalf("order-only edge not respected, execution order: %v", execOrder)
	}
}

// Generated files are verified: deleting or externally modifying a generated
// file rebuilds its producer. If the regenerated content is identical, the
// change does not propagate further (early cutoff); if it differs, content
// dependents rebuild.
func TestGeneratedFileVerification(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "in.txt", "v1")

	var runsB, runsD int
	p := New(root)
	p.AddTask(copyTask("B", "b.txt", []string{"in.txt"}, nil, &runsB))
	p.AddTask(copyTask("D", "d.txt", []string{"b.txt"}, nil, &runsD))
	if _, err := p.Build(); err != nil {
		t.Fatal(err)
	}

	// Delete the generated file: producer rebuilds with "missing" reason.
	// The regenerated content is identical, so D is spared (early cutoff).
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsB != 2 {
		t.Fatalf("producer should rebuild, B=%d", runsB)
	}
	if runsD != 1 {
		t.Fatalf("identical regenerated content must not propagate, D=%d", runsD)
	}
	if r := res.Reasons["B"]; r == nil || !strings.Contains(r.Cause, "output b.txt is missing") {
		t.Fatalf("expected missing-output reason for B, got %v", r)
	}

	// Externally modify the generated file: detected and regenerated.
	write(t, root, "b.txt", "tampered")
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsB != 3 {
		t.Fatalf("producer should rebuild after tampering, B=%d", runsB)
	}
	if r := res.Reasons["B"]; r == nil || !strings.Contains(r.Cause, "modified outside the build") {
		t.Fatalf("expected tamper reason for B, got %v", r)
	}
	if got := read(t, root, "b.txt"); got != "B(v1)" {
		t.Fatalf("tampered content should be regenerated, got %q", got)
	}

	// A real content change propagates through the generated file to D.
	write(t, root, "in.txt", "v2")
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runsB != 4 || runsD != 2 {
		t.Fatalf("content change should propagate to D, B=%d D=%d", runsB, runsD)
	}
	if got := read(t, root, "d.txt"); got != "D(B(v2))" {
		t.Fatalf("unexpected d.txt: %q", got)
	}
	if r := res.Reasons["D"]; r == nil || r.Depth() != 3 {
		t.Fatalf("expected depth-3 reason chain for D, got %v", r)
	}
}

// A source file that changes while a task is running invalidates that run;
// the stale result is discarded and the task re-executes.
func TestSourceChangesDuringTaskRun(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "src.txt", "old")

	var runs int
	p := New(root)
	p.AddTask(&Task{
		Name:    "S",
		Outputs: []string{"out.txt"},
		Run: func(ctx *RunContext) error {
			runs++
			data, err := ctx.ReadFile("src.txt")
			if err != nil {
				return err
			}
			if runs == 1 {
				// Simulate a concurrent editor: change the source mid-run.
				write(t, root, "src.txt", "new")
			}
			return ctx.WriteFile("out.txt", append([]byte("seen:"), data...))
		},
	})

	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("task should have been re-run after mid-run change, runs=%d", runs)
	}
	// The committed result must reflect the NEW content, never the stale read.
	if got := read(t, root, "out.txt"); got != "seen:new" {
		t.Fatalf("stale result was committed: %q", got)
	}
	notes := res.Notes["S"]
	if len(notes) != 1 || !strings.Contains(notes[0].String(), "changed while the task was running") {
		t.Fatalf("expected mid-run change note, got %v", notes)
	}

	// State was committed from the fresh run: next build is a no-op.
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 0 {
		t.Fatalf("expected no-op build, rebuilt %v", res.Rebuilt)
	}
}

// Dynamically discovered dependencies: a task discovers at run time that it
// needs the output of another task that is itself stale. The planner
// schedules the producer and re-runs the discoverer; the stale result is
// never committed.
func TestDynamicDiscoveryReschedules(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "h-in.txt", "hdr-v1")
	write(t, root, "a.txt", "A")

	var genhdrRuns, compileRuns int
	p := New(root)
	// genhdr produces extra.h from h-in.txt.
	p.AddTask(&Task{
		Name:    "genhdr",
		Outputs: []string{"extra.h"},
		Run: func(ctx *RunContext) error {
			genhdrRuns++
			data, err := ctx.ReadFile("h-in.txt")
			if err != nil {
				return err
			}
			return ctx.WriteFile("extra.h", data)
		},
	})
	// compile discovers its deps at run time: a source listed in a manifest
	// and the generated header extra.h.
	p.AddTask(&Task{
		Name:    "compile",
		Deps:    []Dep{ContentDep("a.txt")},
		Outputs: []string{"app.o"},
		Run: func(ctx *RunContext) error {
			compileRuns++
			ctx.DependContent("extra.h") // discovered dependency
			hdr, err := ctx.ReadFile("extra.h")
			if os.IsNotExist(err) {
				hdr = []byte("PLACEHOLDER") // must never be committed
			} else if err != nil {
				return err
			}
			a, err := ctx.ReadFile("a.txt")
			if err != nil {
				return err
			}
			return ctx.WriteFile("app.o", append(hdr, a...))
		},
	})

	// Build 1: compile sorts before genhdr and runs first; extra.h does not
	// exist yet, so the placeholder attempt is discarded once the dep is
	// discovered, genhdr runs, and compile re-runs against the real header.
	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if genhdrRuns != 1 || compileRuns != 2 {
		t.Fatalf("genhdr=%d compile=%d", genhdrRuns, compileRuns)
	}
	if got := read(t, root, "app.o"); got != "hdr-v1A" {
		t.Fatalf("stale placeholder committed: %q", got)
	}
	if notes := res.Notes["compile"]; len(notes) != 1 || !strings.Contains(notes[0].String(), "extra.h") {
		t.Fatalf("expected discard note about extra.h, got %v", notes)
	}

	// Change the header source. The discovered edge is now known, so genhdr
	// is scheduled before compile; compile's shortest reason chain leads
	// through extra.h to the changed source.
	write(t, root, "h-in.txt", "hdr-v2")
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if genhdrRuns != 2 || compileRuns != 3 {
		t.Fatalf("genhdr=%d compile=%d", genhdrRuns, compileRuns)
	}
	// The final result must embed the NEW header: no stale result was used.
	if got := read(t, root, "app.o"); got != "hdr-v2A" {
		t.Fatalf("stale result committed: %q", got)
	}
	r := res.Reasons["compile"]
	if r == nil || r.Depth() != 3 || !strings.Contains(r.String(), "extra.h") || !strings.Contains(r.String(), "h-in.txt") {
		t.Fatalf("expected depth-3 chain through extra.h to h-in.txt, got %v", r)
	}
	t.Logf("compile reason chain: %s", r)

	// The discovered dep persisted: touching h-in.txt again still rebuilds
	// both, and a no-change build is a no-op.
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rebuilt) != 0 {
		t.Fatalf("expected no-op build, rebuilt %v", res.Rebuilt)
	}
}

// A discovered dep on a not-yet-existing generated file: the discoverer runs
// before the producer, gets rescheduled, and only commits once the file
// exists and is fresh. The placeholder result from the first attempt must
// never be committed.
func TestDynamicDiscoveryOfUnbuiltProducer(t *testing.T) {
	root := newRoot(t)
	defer os.RemoveAll(root)
	write(t, root, "list.txt", "gen.txt\n")
	write(t, root, "gen-in.txt", "generated")

	var genRuns, useRuns int
	p := New(root)
	// "z-gen" sorts after "use", so without the discovered edge the
	// producer would run last.
	p.AddTask(&Task{
		Name:    "z-gen",
		Outputs: []string{"gen.txt"},
		Run: func(ctx *RunContext) error {
			genRuns++
			data, err := ctx.ReadFile("gen-in.txt")
			if err != nil {
				return err
			}
			return ctx.WriteFile("gen.txt", data)
		},
	})
	p.AddTask(&Task{
		Name:    "use",
		Deps:    []Dep{ContentDep("list.txt")},
		Outputs: []string{"use.out"},
		Run: func(ctx *RunContext) error {
			useRuns++
			manifest, err := ctx.ReadFile("list.txt")
			if err != nil {
				return err
			}
			var b strings.Builder
			for _, f := range strings.Fields(string(manifest)) {
				ctx.DependContent(f) // discovered from manifest
				data, err := ctx.ReadFile(f)
				if os.IsNotExist(err) {
					b.WriteString("MISSING") // placeholder; must not be committed
					continue
				}
				if err != nil {
					return err
				}
				b.Write(data)
			}
			return ctx.WriteFile("use.out", []byte(b.String()))
		},
	})

	res, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if genRuns != 1 {
		t.Fatalf("producer should have run once, runs=%d", genRuns)
	}
	if useRuns != 2 {
		t.Fatalf("discoverer should have been re-run after the producer, runs=%d", useRuns)
	}
	// The committed output must contain the real generated content.
	if got := read(t, root, "use.out"); got != "generated" {
		t.Fatalf("stale placeholder result was committed: %q", got)
	}
	// Execution order: use (attempt 1, discarded) -> z-gen -> use (commit).
	if got := strings.Join(res.Rebuilt, ","); got != "z-gen,use" {
		t.Fatalf("unexpected commit order: %s", got)
	}
	if notes := res.Notes["use"]; len(notes) != 1 || !strings.Contains(notes[0].String(), "gen.txt") {
		t.Fatalf("expected discard note mentioning gen.txt, got %v", notes)
	}
	t.Logf("use discard note: %s", res.Notes["use"][0])

	// The discovered dep persisted: changing the producer's source
	// propagates through the discovered edge to "use".
	write(t, root, "gen-in.txt", "regenerated")
	res, err = p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !rebuiltSet(res)["use"] {
		t.Fatalf("use should rebuild when discovered dep changes, rebuilt %v", res.Rebuilt)
	}
	if got := read(t, root, "use.out"); got != "regenerated" {
		t.Fatalf("unexpected use.out: %q", got)
	}
}
