package main

import (
	"math/big"
	"reflect"
	"testing"
)

func file(id, bytes string, intact bool, deps ...string) FileSpec {
	v, _ := new(big.Int).SetString(bytes, 10)
	return FileSpec{ID: id, Bytes: Amount{V: v}, Intact: &intact, Deps: deps}
}

func shard(name string, target int64, edges ...Edge) ShardSpec {
	return ShardSpec{Name: name, Target: target, Edges: edges}
}

func ed(from, to int64, f string) Edge { return Edge{From: from, To: to, File: f} }

func solve(t *testing.T, in *Input, budget int64) *Solution {
	t.Helper()
	m, diags, fatal := buildModel(in)
	if fatal {
		return &Solution{Status: "error", Diagnostics: diags}
	}
	return Solve(m, diags, budget)
}

func hasDiag(sol *Solution, kind string) bool {
	for _, d := range sol.Diagnostics {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

// A file shared by two shards is charged once, so the shared plan beats
// summing the per-shard optima.
func TestSharedFileBeatsIndependent(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("shared", "10", true),
			file("alt1", "6", true),
			file("alt2", "6", true),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "shared"), ed(0, 1, "alt1")),
			shard("s2", 1, ed(0, 1, "shared"), ed(0, 1, "alt2")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Status != StatusOptimal {
		t.Fatalf("status = %s, want optimal", sol.Status)
	}
	// Independent optima would sum to 6+6=12; sharing costs 10 once.
	if sol.Cost != "10" {
		t.Fatalf("cost = %s, want 10", sol.Cost)
	}
	if !reflect.DeepEqual(sol.Files, []string{"shared"}) {
		t.Fatalf("files = %v, want [shared]", sol.Files)
	}
	if v := Verify(in, sol.Files); !v.OK || v.Cost != sol.Cost {
		t.Fatalf("verifier rejected solver output: %+v", v)
	}
}

// A missing version link makes the shard unrecoverable.
func TestGapUnrecoverable(t *testing.T) {
	in := &Input{
		Files:  []FileSpec{file("a", "3", true)},
		Shards: []ShardSpec{shard("s1", 2, ed(0, 1, "a"))},
	}
	sol := solve(t, in, -1)
	if sol.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", sol.Status)
	}
	if !hasDiag(sol, "gap") {
		t.Fatalf("expected gap diagnostic, got %+v", sol.Diagnostics)
	}
}

// An intact file depending on a corrupted file is unusable; the planner
// must fall back to an alternative, or report the shard unrecoverable.
func TestCorruptedDependencyCascade(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("alt", "5", true),
			file("main", "1", true, "lib"),
			file("lib", "1", false),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "main"), ed(0, 1, "alt")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Status != StatusOptimal || sol.Cost != "5" {
		t.Fatalf("status=%s cost=%s, want optimal/5", sol.Status, sol.Cost)
	}
	if !reflect.DeepEqual(sol.Files, []string{"alt"}) {
		t.Fatalf("files = %v, want [alt]", sol.Files)
	}
	if !hasDiag(sol, "corrupted") || !hasDiag(sol, "corrupted_cascade") {
		t.Fatalf("expected corruption diagnostics, got %+v", sol.Diagnostics)
	}

	// Without the alternative the shard cannot be restored.
	in.Shards[0].Edges = in.Shards[0].Edges[:1]
	sol = solve(t, in, -1)
	if sol.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", sol.Status)
	}
	if !hasDiag(sol, "unrecoverable") {
		t.Fatalf("expected unrecoverable diagnostic, got %+v", sol.Diagnostics)
	}
}

// Zero-byte files are allowed and free; ties are broken by the
// lexicographically smallest sorted ID list.
func TestZeroCostFiles(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("z1", "0", true),
			file("z2", "0", true),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "z2"), ed(0, 1, "z1")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Status != StatusOptimal || sol.Cost != "0" {
		t.Fatalf("status=%s cost=%s, want optimal/0", sol.Status, sol.Cost)
	}
	if !reflect.DeepEqual(sol.Files, []string{"z1"}) {
		t.Fatalf("files = %v, want [z1]", sol.Files)
	}
}

// Equal-cost plans are decided by the sorted file-ID list.
func TestTieBreakLexicographic(t *testing.T) {
	in := &Input{
		Files: []FileSpec{file("b", "5", true), file("c", "5", true)},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "c"), ed(0, 1, "b")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Cost != "5" || !reflect.DeepEqual(sol.Files, []string{"b"}) {
		t.Fatalf("cost=%s files=%v, want 5/[b]", sol.Cost, sol.Files)
	}
}

// A budget exactly equal to the required expansion count still proves
// optimality; one less must not claim it.
func TestExactBudgetExhaustion(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("p1a", "0", true), file("p1b", "0", true),
			file("p2a", "0", true), file("p2b", "0", true),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "p1a"), ed(0, 1, "p1b")),
			shard("s2", 1, ed(0, 1, "p2a"), ed(0, 1, "p2b")),
		},
	}
	full := solve(t, in, -1)
	if full.Status != StatusOptimal {
		t.Fatalf("unlimited run: status = %s", full.Status)
	}
	need := full.Expansions
	if need < 2 {
		t.Fatalf("degenerate search tree, expansions = %d", need)
	}

	exact := solve(t, in, need)
	if exact.Status != StatusOptimal {
		t.Fatalf("budget=%d: status = %s, want optimal", need, exact.Status)
	}
	if exact.Expansions != need || !reflect.DeepEqual(exact.Files, full.Files) {
		t.Fatalf("budget=%d: expansions=%d files=%v, want %d/%v",
			need, exact.Expansions, exact.Files, need, full.Files)
	}

	short := solve(t, in, need-1)
	if short.Status == StatusOptimal {
		t.Fatalf("budget=%d: must not claim optimal", need-1)
	}
	if short.Expansions != need-1 {
		t.Fatalf("budget=%d: expansions = %d", need-1, short.Expansions)
	}
	if short.LowerBound == "" {
		t.Fatalf("budget=%d: missing lower bound", need-1)
	}
	lb, _ := new(big.Int).SetString(short.LowerBound, 10)
	opt, _ := new(big.Int).SetString(full.Cost, 10)
	if lb.Cmp(opt) > 0 {
		t.Fatalf("lower bound %s exceeds true optimum %s", lb, opt)
	}
	if short.Status == StatusFeasible {
		got, _ := new(big.Int).SetString(short.Cost, 10)
		if got.Cmp(opt) < 0 {
			t.Fatalf("feasible cost %s below optimum %s", got, opt)
		}
	}
}

// Zero budget: no node may be expanded, but a valid lower bound is returned.
func TestZeroBudget(t *testing.T) {
	in := &Input{
		Files:  []FileSpec{file("a", "7", true)},
		Shards: []ShardSpec{shard("s1", 1, ed(0, 1, "a"))},
	}
	sol := solve(t, in, 0)
	if sol.Status != StatusUnknown || sol.Expansions != 0 {
		t.Fatalf("status=%s expansions=%d, want unknown/0", sol.Status, sol.Expansions)
	}
	// The root lower bound already reflects the only shard's minimum
	// marginal cost, which is a valid (and here tight) bound.
	if sol.LowerBound != "7" {
		t.Fatalf("lower bound = %s, want 7", sol.LowerBound)
	}
}

// Costs are arbitrary precision; a shared 2^63-byte file is charged once.
func TestBigIntNoOverflow(t *testing.T) {
	big63 := "9223372036854775808" // 2^63
	in := &Input{
		Files: []FileSpec{file("big", big63, true)},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "big")),
			shard("s2", 1, ed(0, 1, "big")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Cost != big63 {
		t.Fatalf("shared cost = %s, want %s (charged once)", sol.Cost, big63)
	}

	in2 := &Input{
		Files: []FileSpec{file("b1", big63, true), file("b2", big63, true)},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "b1")),
			shard("s2", 1, ed(0, 1, "b2")),
		},
	}
	sol2 := solve(t, in2, -1)
	if sol2.Cost != "18446744073709551616" { // 2^64, overflows uint64
		t.Fatalf("sum cost = %s, want 18446744073709551616", sol2.Cost)
	}
}

func TestDepCycleDiagnosed(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("a", "1", true, "b"),
			file("b", "1", true, "a"),
		},
		Shards: []ShardSpec{shard("s1", 1, ed(0, 1, "a"))},
	}
	sol := solve(t, in, -1)
	if sol.Status != "error" || !hasDiag(sol, "dep_cycle") {
		t.Fatalf("status=%s diags=%+v, want error/dep_cycle", sol.Status, sol.Diagnostics)
	}
}

func TestMissingIDDiagnosed(t *testing.T) {
	in := &Input{
		Files:  []FileSpec{file("a", "1", true, "ghost")},
		Shards: []ShardSpec{shard("s1", 1, ed(0, 1, "a"))},
	}
	sol := solve(t, in, -1)
	if sol.Status != "error" || !hasDiag(sol, "missing_id") {
		t.Fatalf("status=%s diags=%+v, want error/missing_id", sol.Status, sol.Diagnostics)
	}

	in2 := &Input{
		Files:  []FileSpec{file("a", "1", true)},
		Shards: []ShardSpec{shard("s1", 1, ed(0, 1, "ghost"))},
	}
	sol2 := solve(t, in2, -1)
	if sol2.Status != "error" || !hasDiag(sol2, "missing_id") {
		t.Fatalf("status=%s diags=%+v, want error/missing_id", sol2.Status, sol2.Diagnostics)
	}
}

// Dependencies of path files are pulled in automatically and charged once.
func TestDependencyClosureSelected(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("app", "4", true, "lib"),
			file("lib", "2", true),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 1, "app")),
			shard("s2", 1, ed(0, 1, "lib")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Cost != "6" || !reflect.DeepEqual(sol.Files, []string{"app", "lib"}) {
		t.Fatalf("cost=%s files=%v, want 6/[app lib]", sol.Cost, sol.Files)
	}
}

// Same input and budget must always produce the same result.
func TestDeterministic(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("a", "3", true), file("b", "4", true),
			file("c", "1", true), file("d", "2", true),
		},
		Shards: []ShardSpec{
			shard("s1", 2, ed(0, 1, "a"), ed(0, 2, "b"), ed(1, 2, "c")),
			shard("s2", 1, ed(0, 1, "a"), ed(0, 1, "d")),
		},
	}
	first := solve(t, in, 5)
	for i := 0; i < 3; i++ {
		got := solve(t, in, 5)
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs:\n%+v\n%+v", i, first, got)
		}
	}
}

// A path may not overshoot the target and come back; versions strictly
// increase, so only paths ending exactly at the target count.
func TestNoOvershoot(t *testing.T) {
	in := &Input{
		Files: []FileSpec{
			file("a", "1", true), file("b", "1", true),
		},
		Shards: []ShardSpec{
			shard("s1", 1, ed(0, 2, "a"), ed(0, 1, "b")),
		},
	}
	sol := solve(t, in, -1)
	if sol.Status != StatusOptimal || !reflect.DeepEqual(sol.Files, []string{"b"}) {
		t.Fatalf("status=%s files=%v, want optimal/[b]", sol.Status, sol.Files)
	}
}
