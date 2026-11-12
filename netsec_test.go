package netsec_test

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	netsec "example.org/netsec/q0414"
)

// handshakeProblem models a small client/server handshake on an integer
// millisecond clock. The server may answer immediately (weak order), while
// every strict step needs at least one clock tick.
func handshakeProblem(t *testing.T) *netsec.Problem {
	t.Helper()
	names := []string{"client_hello", "server_recv", "server_hello", "client_done", "server_fin", "client_ack"}
	lo := []int64{0, 0, 0, 0, 0, 0}
	hi := []int64{4, 4, 4, 4, 4, 4}
	orders := []netsec.Order{
		{Before: "client_hello", After: "server_recv", Kind: netsec.StrictOrder},
		{Before: "server_recv", After: "server_hello", Kind: netsec.WeakOrder},
		{Before: "server_hello", After: "client_done", Kind: netsec.StrictOrder},
		{Before: "client_done", After: "server_fin", Kind: netsec.StrictOrder},
		{Before: "server_fin", After: "client_ack", Kind: netsec.WeakOrder},
	}
	p, err := netsec.NewProblem(names, lo, hi, orders)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	return p
}

func TestSolveAndTightWindows(t *testing.T) {
	p := handshakeProblem(t)
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	earliest := sol.Times()
	if !p.Legal(earliest) {
		t.Fatalf("earliest assignment illegal: %v", earliest)
	}

	wantEarliest := []int64{0, 1, 1, 2, 3, 3}
	if !equal(earliest, wantEarliest) {
		t.Fatalf("earliest = %v, want %v", earliest, wantEarliest)
	}
	latest := sol.Latest()
	if !p.Legal(latest) {
		t.Fatalf("latest assignment illegal: %v", latest)
	}

	// client_hello needs three strict successors before the deadline at 4.
	lo, hi := sol.Window(0)
	if lo != 0 || hi != 1 {
		t.Fatalf("client_hello window = [%d,%d], want [0,1]", lo, hi)
	}
}

func TestRandomReproducibleAndLegal(t *testing.T) {
	p := handshakeProblem(t)
	first, err := p.Random(42)
	if err != nil {
		t.Fatalf("Random: %v", err)
	}
	second, err := p.Random(42)
	if err != nil {
		t.Fatalf("Random repeat: %v", err)
	}
	if !equal(first.Times, second.Times) {
		t.Fatalf("seed not reproducible: %v != %v", first.Times, second.Times)
	}
	if !p.Legal(first.Times) {
		t.Fatalf("random assignment illegal: %v", first.Times)
	}

	// On a tiny domain some seeds may collide, but a batch of seeds must
	// explore more than one assignment.
	distinct := map[string]bool{fmt.Sprint(first.Times): true}
	for seed := uint64(100); seed < 130; seed++ {
		other, err := p.Random(seed)
		if err != nil {
			t.Fatalf("Random(%d): %v", seed, err)
		}
		if !p.Legal(other.Times) {
			t.Fatalf("random assignment illegal: %v", other.Times)
		}
		distinct[fmt.Sprint(other.Times)] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("sampler produced no diversity: %d distinct assignment(s)", len(distinct))
	}

	// The handshake domain has only 7 legal assignments; request all of them.
	muts, err := p.Generate(1, 7)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := map[string]bool{}
	for i, m := range muts {
		if !p.Legal(m.Times) {
			t.Fatalf("mutation %d illegal: %v", i, m.Times)
		}
		key := fmt.Sprint(m.Times)
		if seen[key] {
			t.Fatalf("Generate returned duplicate assignment: %v", m.Times)
		}
		seen[key] = true
	}
}

func TestBoundarySamples(t *testing.T) {
	p := handshakeProblem(t)
	b, err := p.BoundarySamples()
	if err != nil {
		t.Fatalf("BoundarySamples: %v", err)
	}
	if b.Simultaneous == nil || b.BeforeDeadline == nil || b.AtDeadline == nil {
		t.Fatalf("missing boundary sample: %+v", b)
	}
	if !p.Legal(b.Simultaneous.Times) || !p.Legal(b.BeforeDeadline.Times) || !p.Legal(b.AtDeadline.Times) {
		t.Fatalf("boundary assignment illegal: %+v", b)
	}

	if !hasCoincident(b.Simultaneous.Times) {
		t.Fatalf("simultaneous sample has no equal timestamps: %v", b.Simultaneous.Times)
	}
	deadline := p.Deadline()
	if !contains(b.BeforeDeadline.Times, deadline-1) {
		t.Fatalf("before-deadline sample missing t=%d: %v", deadline-1, b.BeforeDeadline.Times)
	}
	if !contains(b.AtDeadline.Times, deadline) {
		t.Fatalf("at-deadline sample missing t=%d: %v", deadline, b.AtDeadline.Times)
	}

	again, err := p.BoundarySamples()
	if err != nil {
		t.Fatalf("BoundarySamples repeat: %v", err)
	}
	if !equal(again.Simultaneous.Times, b.Simultaneous.Times) ||
		!equal(again.BeforeDeadline.Times, b.BeforeDeadline.Times) ||
		!equal(again.AtDeadline.Times, b.AtDeadline.Times) {
		t.Fatalf("boundary samples are not deterministic")
	}
}

func TestEnumerationCrossCheck(t *testing.T) {
	p := handshakeProblem(t)
	all, err := p.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("enumeration found no legal assignments")
	}
	for i, ts := range all {
		if !p.Legal(ts) {
			t.Fatalf("enumerated assignment %d illegal: %v", i, ts)
		}
	}

	seen := map[string]bool{}
	for i, ts := range all {
		key := fmt.Sprint(ts)
		if seen[key] {
			t.Fatalf("duplicate enumeration entry: %v", ts)
		}
		seen[key] = true
		if i > 0 && leq(ts, all[i-1]) {
			t.Fatalf("enumeration not strictly lexicographic at %d", i)
		}
	}

	var samples []netsec.Mutation
	for seed := uint64(1); seed <= 40; seed++ {
		m, err := p.Random(seed)
		if err != nil {
			t.Fatalf("Random(%d): %v", seed, err)
		}
		samples = append(samples, *m)
	}
	b, err := p.BoundarySamples()
	if err != nil {
		t.Fatalf("BoundarySamples: %v", err)
	}
	samples = append(samples, *b.Simultaneous, *b.BeforeDeadline, *b.AtDeadline)

	cov, err := p.CheckCoverage(samples, 5)
	if err != nil {
		t.Fatalf("CheckCoverage: %v", err)
	}
	if cov.Hit == 0 || cov.Total < cov.Hit {
		t.Fatalf("bad coverage report: %+v", cov)
	}
	t.Logf("coverage: %d unique samples over %d legal assignments", cov.Hit, cov.Total)
}

func TestSamplerCanCoverWholeDomain(t *testing.T) {
	p := handshakeProblem(t)
	all, err := p.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	want := map[string]bool{}
	for _, ts := range all {
		want[fmt.Sprint(ts)] = true
	}
	got := map[string]bool{}
	for seed := uint64(1); seed <= 500 && len(got) < len(want); seed++ {
		m, err := p.Random(seed)
		if err != nil {
			t.Fatalf("Random(%d): %v", seed, err)
		}
		if !p.Legal(m.Times) {
			t.Fatalf("illegal sample for seed %d: %v", seed, m.Times)
		}
		got[fmt.Sprint(m.Times)] = true
	}
	if len(got) != len(want) {
		missing := []string{}
		for k := range want {
			if !got[k] {
				missing = append(missing, k)
			}
		}
		t.Fatalf("sampler covered %d/%d legal assignments; missing %v", len(got), len(want), missing)
	}
}

func TestEnumerationGuardsHugeDomain(t *testing.T) {
	p, err := netsec.NewProblem([]string{"a", "b"}, []int64{0, 0}, []int64{2000, 2000}, nil)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	if _, err := p.Enumerate(); err == nil {
		t.Fatal("expected enumeration guard error for large domain")
	}
}

func TestNegativeCycleEvidence(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"x", "y"},
		[]int64{0, 0}, []int64{3, 3},
		[]netsec.Order{
			{Before: "x", After: "y", Kind: netsec.StrictOrder},
			{Before: "y", After: "x", Kind: netsec.StrictOrder},
		},
	)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	_, err = p.Solve()
	if err == nil {
		t.Fatal("expected infeasibility error")
	}
	var cyc *netsec.CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("error is not *CycleError: %T", err)
	}
	if cyc.Sum() >= 0 {
		t.Fatalf("cycle sum must be negative, got %d", cyc.Sum())
	}
	if len(cyc.Edges()) < 2 {
		t.Fatalf("cycle too short: %d edges", len(cyc.Edges()))
	}
	evidence := cyc.Explain(p)
	if !strings.Contains(evidence, "t[x]") || !strings.Contains(evidence, "t[y]") {
		t.Fatalf("evidence does not name both events:\n%s", evidence)
	}
	if _, err := p.Random(1); err == nil {
		t.Fatal("Random must reject an infeasible problem")
	}
	if _, err := p.BoundarySamples(); err == nil {
		t.Fatal("BoundarySamples must reject an infeasible problem")
	}
}

func TestEmptyWindowIsNegativeCycle(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"a", "b"},
		[]int64{5, 0}, []int64{5, 2},
		[]netsec.Order{{Before: "a", After: "b", Kind: netsec.WeakOrder}},
	)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	_, err = p.Solve()
	var cyc *netsec.CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected CycleError for contradictory bounds/order, got %v", err)
	}
	if cyc.Sum() >= 0 {
		t.Fatalf("negative cycle expected, sum=%d", cyc.Sum())
	}
}

func TestValidation(t *testing.T) {
	if _, err := netsec.NewProblem(nil, nil, nil, nil); err == nil {
		t.Fatal("expected error for empty problem")
	}
	if _, err := netsec.NewProblem(
		[]string{"a", "a"}, []int64{0, 0}, []int64{1, 1}, nil,
	); err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if _, err := netsec.NewProblem(
		[]string{"a", "b"}, []int64{2}, []int64{1, 1}, nil,
	); err == nil {
		t.Fatal("expected length-mismatch error")
	}
	if _, err := netsec.NewProblem(
		[]string{"a"}, []int64{2}, []int64{1}, nil,
	); err == nil {
		t.Fatal("expected empty-window error")
	}
	if _, err := netsec.NewProblem(
		[]string{"a"}, []int64{0}, []int64{1},
		[]netsec.Order{{Before: "a", After: "ghost"}},
	); err == nil {
		t.Fatal("expected unknown-event error")
	}
}

func TestAllStrictChainHasNoSimultaneous(t *testing.T) {
	// A fully strict chain can never schedule two events at the same time.
	p, err := netsec.NewProblem(
		[]string{"e0", "e1", "e2"},
		[]int64{0, 0, 0}, []int64{5, 5, 5},
		[]netsec.Order{
			{Before: "e0", After: "e1", Kind: netsec.StrictOrder},
			{Before: "e1", After: "e2", Kind: netsec.StrictOrder},
		},
	)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	b, err := p.BoundarySamples()
	if err != nil {
		t.Fatalf("BoundarySamples: %v", err)
	}
	if b.Simultaneous != nil {
		t.Fatalf("strict chain must not admit simultaneous sample, got %v", b.Simultaneous.Times)
	}
	if b.BeforeDeadline == nil || b.AtDeadline == nil {
		t.Fatalf("deadline boundary samples should still exist: %+v", b)
	}
	if !p.Legal(b.BeforeDeadline.Times) || !p.Legal(b.AtDeadline.Times) {
		t.Fatalf("deadline samples illegal: %+v", b)
	}
}

func TestNonZeroLowerBoundsAndSingleEvent(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"a", "b"},
		[]int64{10, 10}, []int64{13, 13},
		[]netsec.Order{{Before: "a", After: "b", Kind: netsec.StrictOrder}},
	)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	lo, hi := sol.Window(1)
	if lo != 11 || hi != 13 {
		t.Fatalf("b tight window = [%d,%d], want [11,13]", lo, hi)
	}

	one, err := netsec.NewProblem([]string{"solo"}, []int64{3}, []int64{3}, nil)
	if err != nil {
		t.Fatalf("NewProblem single: %v", err)
	}
	m, err := one.Random(7)
	if err != nil {
		t.Fatalf("single Random: %v", err)
	}
	if len(m.Times) != 1 || m.Times[0] != 3 {
		t.Fatalf("single event mutation wrong: %v", m.Times)
	}
	all, err := one.Enumerate()
	if err != nil || len(all) != 1 || all[0][0] != 3 {
		t.Fatalf("single event enumeration wrong: %v %v", all, err)
	}
}

func TestGenerateRejectsOversizedRequest(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"a", "b"}, []int64{0, 0}, []int64{1, 1},
		[]netsec.Order{{Before: "a", After: "b", Kind: netsec.WeakOrder}},
	)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	all, err := p.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if _, err := p.Generate(1, len(all)+1); err == nil {
		t.Fatal("expected error when requesting more mutations than exist")
	}
	if _, err := p.Generate(1, 0); err == nil {
		t.Fatal("expected error for non-positive count")
	}
}

func TestRandomFallbackAndAccessors(t *testing.T) {
	// Tightly coupled chain: each event's tight window is a single value once
	// predecessors are fixed, so rejection sampling rarely accepts and the
	// topological fallback must still return a legal, reproducible result.
	names := []string{"e0", "e1", "e2", "e3"}
	lo := []int64{0, 0, 0, 0}
	hi := []int64{3, 3, 3, 3}
	orders := []netsec.Order{
		{Before: "e0", After: "e1", Kind: netsec.StrictOrder},
		{Before: "e1", After: "e2", Kind: netsec.StrictOrder},
		{Before: "e2", After: "e3", Kind: netsec.StrictOrder},
	}
	p, err := netsec.NewProblem(names, lo, hi, orders)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	for seed := uint64(0); seed < 50; seed++ {
		m, err := p.Random(seed)
		if err != nil {
			t.Fatalf("Random(%d): %v", seed, err)
		}
		if !p.Legal(m.Times) {
			t.Fatalf("fallback produced illegal assignment: %v", m.Times)
		}
	}
	// Accessor coverage.
	if p.EventCount() != 4 {
		t.Fatalf("EventCount = %d", p.EventCount())
	}
	if n, l, h := p.Event(0); n != "e0" || l != 0 || h != 3 {
		t.Fatalf("Event(0) = %q,%d,%d", n, l, h)
	}
	if idx, ok := p.EventIndex("e2"); !ok || idx != 2 {
		t.Fatalf("EventIndex = %d,%v", idx, ok)
	}
	if _, ok := p.EventIndex("ghost"); ok {
		t.Fatal("ghost event should not resolve")
	}
	if len(p.Orders()) != len(orders) {
		t.Fatal("Orders() length mismatch")
	}
	if p.Deadline() != 3 {
		t.Fatalf("Deadline = %d", p.Deadline())
	}
	if p.Legal([]int64{0, 1, 2}) {
		t.Fatal("Legal must reject wrong-length assignment")
	}
	if s := p.String(); !strings.Contains(s, "e0") {
		t.Fatalf("String() missing event: %s", s)
	}
}

func TestCycleErrorString(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"x", "y"}, []int64{0, 0}, []int64{3, 3},
		[]netsec.Order{
			{Before: "x", After: "y", Kind: netsec.StrictOrder},
			{Before: "y", After: "x", Kind: netsec.StrictOrder},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Solve()
	var cyc *netsec.CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("want CycleError, got %T", err)
	}
	if s := err.Error(); !strings.Contains(s, "negative cycle") {
		t.Fatalf("Error() missing marker: %s", s)
	}
	if len(cyc.Edges()) == 0 {
		t.Fatal("cycle has no edges")
	}
	// Explain must render bound and ordering constraints, not raw node ids.
	explained := cyc.Explain(p)
	if strings.Contains(explained, "edge ") {
		t.Fatalf("Explain should use constraint prose:\n%s", explained)
	}
}

func TestCheckCoverageRejectsIllegalSample(t *testing.T) {
	p, err := netsec.NewProblem(
		[]string{"a", "b"}, []int64{0, 0}, []int64{2, 2},
		[]netsec.Order{{Before: "a", After: "b", Kind: netsec.WeakOrder}},
	)
	if err != nil {
		t.Fatal(err)
	}
	bad := netsec.Mutation{Times: []int64{2, 0}, Seed: 1, Tag: "bad"}
	if _, err := p.CheckCoverage([]netsec.Mutation{bad}, 3); err == nil {
		t.Fatal("expected CheckCoverage to reject illegal sample")
	}
	good, err := p.Random(1)
	if err != nil {
		t.Fatal(err)
	}
	cov, err := p.CheckCoverage([]netsec.Mutation{*good}, 100)
	if err != nil {
		t.Fatalf("CheckCoverage legal sample: %v", err)
	}
	if cov.Total <= 0 || cov.Hit != 1 {
		t.Fatalf("unexpected coverage: %+v", cov)
	}
}

// TestNoNetworkImports statically guarantees the package never imports the
// networking standard library; the mutator is strictly offline.
func TestNoNetworkImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, pkg := range pkgs {
		for file, astFile := range pkg.Files {
			for _, imp := range astFile.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if path == "net" || strings.HasPrefix(path, "net/") ||
					strings.HasPrefix(path, "golang.org/x/net/") {
					t.Fatalf("%s imports networking package %s", file, path)
				}
			}
		}
	}
}

func equal(a, b []int64) bool {
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

// leq reports whether a <= b in lexicographic order.
func leq(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return true
}

func contains(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func hasCoincident(xs []int64) bool {
	seen := map[int64]bool{}
	for _, x := range xs {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}
