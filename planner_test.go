package main

import (
	"math/big"
	"reflect"
	"testing"
)

func nat(v int64) Nat {
	return Nat{v: big.NewInt(v)}
}

func natStr(s string) Nat {
	n := Nat{}
	if err := n.UnmarshalJSON([]byte(`"` + s + `"`)); err != nil {
		panic(err)
	}
	return n
}

func budgetOf(v int64) *int64 { return &v }

// mustSolve builds the env and fails the test on hard input errors.
func mustEnv(t *testing.T, in *Input) *env {
	t.Helper()
	env, errs := buildEnv(in)
	if len(errs) > 0 {
		t.Fatalf("unexpected input errors: %+v", errs)
	}
	return env
}

// TestSharedFileBeatsIndependent: one file serving two shards must be
// charged once, so the joint optimum is cheaper than the sum of the two
// per-shard optima.
func TestSharedFileBeatsIndependent(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "common", Bytes: nat(60)},
			{ID: "a-only", Bytes: nat(50)},
			{ID: "b-only", Bytes: nat(50)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 20, Edges: []EdgeInput{{From: 0, To: 20, File: "common"}, {From: 0, To: 20, File: "a-only"}}},
			{Name: "s2", Target: 20, Edges: []EdgeInput{{From: 0, To: 20, File: "common"}, {From: 0, To: 20, File: "b-only"}}},
		},
	}
	out := solve(in, -1)
	if out.Status != StatusOptimal {
		t.Fatalf("status = %s, want optimal", out.Status)
	}
	if out.Solution.TotalBytes != "60" {
		t.Fatalf("total = %s, want 60 (shared file charged once)", out.Solution.TotalBytes)
	}
	if !reflect.DeepEqual(out.Solution.Files, []string{"common"}) {
		t.Fatalf("files = %v, want [common]", out.Solution.Files)
	}
	independentSum := int64(50 + 50) // best per-shard plans added up
	if got := out.Solution.TotalBytes; got != "60" || int64(60) >= independentSum {
		t.Fatalf("joint plan %s must beat independent sum %d", got, independentSum)
	}
	// Cross-check against full subset enumeration.
	env := mustEnv(t, in)
	cost, ids, ok := bruteForce(env)
	if !ok || cost.String() != "60" || !reflect.DeepEqual(ids, []string{"common"}) {
		t.Fatalf("brute force disagrees: %v %v %v", cost, ids, ok)
	}
}

// TestGapUnrecoverable: a missing version range makes the shard unrestorable.
func TestGapUnrecoverable(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "f1", Bytes: nat(10)},
			{ID: "f2", Bytes: nat(10)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 30, Edges: []EdgeInput{
				{From: 0, To: 10, File: "f1"},
				{From: 20, To: 30, File: "f2"}, // gap: nothing covers 10 -> 20
			}},
		},
	}
	out := solve(in, -1)
	if out.Status != StatusInfeasible {
		t.Fatalf("status = %s, want infeasible", out.Status)
	}
	if out.Solution != nil {
		t.Fatalf("expected no solution, got %+v", out.Solution)
	}
}

// TestCorruptedDependencyCascade: a corrupted dependency makes its
// dependents unusable; the planner must diagnose the cascade and either
// route around it or report infeasible.
func TestCorruptedDependencyCascade(t *testing.T) {
	base := []FileInput{
		{ID: "base", Bytes: nat(10)},
		{ID: "mid", Bytes: nat(5), Corrupted: true},
		{ID: "top", Bytes: nat(10), Dependencies: []string{"mid"}},
	}
	edges := []EdgeInput{
		{From: 0, To: 10, File: "base"},
		{From: 10, To: 20, File: "top"},
	}

	// Variant 1: no clean alternative -> infeasible, cascade diagnosed.
	in1 := &Input{Files: base, Shards: []ShardInput{{Name: "s1", Target: 20, Edges: edges}}}
	out1 := solve(in1, -1)
	if out1.Status != StatusInfeasible {
		t.Fatalf("variant1 status = %s, want infeasible", out1.Status)
	}
	if !reflect.DeepEqual(out1.Diagnostics.CorruptedFiles, []string{"mid"}) {
		t.Fatalf("corrupted = %v, want [mid]", out1.Diagnostics.CorruptedFiles)
	}
	if !reflect.DeepEqual(out1.Diagnostics.CascadedUnusableFiles, []string{"top"}) {
		t.Fatalf("cascaded = %v, want [top]", out1.Diagnostics.CascadedUnusableFiles)
	}

	// Variant 2: expensive clean detour -> optimal routes around corruption.
	in2 := &Input{
		Files: append(append([]FileInput{}, base...), FileInput{ID: "direct", Bytes: nat(100)}),
		Shards: []ShardInput{{Name: "s1", Target: 20, Edges: append(append([]EdgeInput{}, edges...),
			EdgeInput{From: 0, To: 20, File: "direct"})}},
	}
	out2 := solve(in2, -1)
	if out2.Status != StatusOptimal || out2.Solution.TotalBytes != "100" {
		t.Fatalf("variant2 = %s %+v, want optimal cost 100", out2.Status, out2.Solution)
	}
	if !reflect.DeepEqual(out2.Solution.Files, []string{"direct"}) {
		t.Fatalf("variant2 files = %v, want [direct]", out2.Solution.Files)
	}
}

// TestZeroCostFiles: zero-byte files are selected when needed and never
// inflate the total; an all-zero restore costs exactly 0.
func TestZeroCostFiles(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "free-base", Bytes: nat(0)},
			{ID: "paid-inc", Bytes: nat(5)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 20, Edges: []EdgeInput{
				{From: 0, To: 10, File: "free-base"},
				{From: 10, To: 20, File: "paid-inc"},
			}},
		},
	}
	out := solve(in, -1)
	if out.Status != StatusOptimal || out.Solution.TotalBytes != "5" {
		t.Fatalf("status=%s total=%v, want optimal 5", out.Status, out.Solution)
	}
	if !reflect.DeepEqual(out.Solution.Files, []string{"free-base", "paid-inc"}) {
		t.Fatalf("files = %v", out.Solution.Files)
	}

	zero := &Input{
		Files:  []FileInput{{ID: "z", Bytes: nat(0)}},
		Shards: []ShardInput{{Name: "s1", Target: 7, Edges: []EdgeInput{{From: 0, To: 7, File: "z"}}}},
	}
	outZ := solve(zero, -1)
	if outZ.Status != StatusOptimal || outZ.Solution.TotalBytes != "0" {
		t.Fatalf("all-zero restore: status=%s total=%v", outZ.Status, outZ.Solution)
	}
}

// TestBigIntCosts: byte counts beyond 64 bits must sum exactly; shared
// files must be charged once even when the naive sum would overflow uint64.
func TestBigIntCosts(t *testing.T) {
	big1 := "18446744073709551617" // 2^64 + 1
	big2 := "36893488147419103233" // 2^65 + 1
	in := &Input{
		Files: []FileInput{
			{ID: "huge-shared", Bytes: natStr(big1)},
			{ID: "huge-tail", Bytes: natStr(big2)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 10, Edges: []EdgeInput{{From: 0, To: 10, File: "huge-shared"}}},
			{Name: "s2", Target: 20, Edges: []EdgeInput{
				{From: 0, To: 10, File: "huge-shared"},
				{From: 10, To: 20, File: "huge-tail"},
			}},
		},
	}
	out := solve(in, -1)
	want := new(big.Int)
	v1, _ := new(big.Int).SetString(big1, 10)
	v2, _ := new(big.Int).SetString(big2, 10)
	want.Add(v1, v2)
	if out.Status != StatusOptimal || out.Solution.TotalBytes != want.String() {
		t.Fatalf("status=%s total=%v, want optimal %s", out.Status, out.Solution, want.String())
	}
	if out.LowerBound != want.String() {
		t.Fatalf("lower bound = %s, want %s", out.LowerBound, want.String())
	}
}

// TestBudgetExactlyExhausted: a budget equal to the exact number of node
// expansions the search needs still proves optimality; one less must stop
// early with a feasible solution, a valid lower bound, and no optimal claim.
func TestBudgetExactlyExhausted(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "a", Bytes: nat(3)},
			{ID: "b", Bytes: nat(4)},
			{ID: "c", Bytes: nat(1)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 2, Edges: []EdgeInput{
				{From: 0, To: 1, File: "a"},
				{From: 1, To: 2, File: "b"},
			}},
		},
	}
	full := solve(in, -1)
	if full.Status != StatusOptimal {
		t.Fatalf("unlimited run status = %s", full.Status)
	}
	need := full.NodesExpanded
	if need < 2 {
		t.Fatalf("expected a non-trivial search, expanded %d nodes", need)
	}

	exact := solve(in, need)
	if exact.Status != StatusOptimal {
		t.Fatalf("budget=%d (exact) status = %s, want optimal", need, exact.Status)
	}
	if exact.NodesExpanded != need {
		t.Fatalf("expanded = %d, want exactly %d", exact.NodesExpanded, need)
	}
	if !reflect.DeepEqual(exact.Solution, full.Solution) {
		t.Fatalf("exact-budget solution %+v != %+v", exact.Solution, full.Solution)
	}

	short := solve(in, need-1)
	if short.Status != StatusBudgetExhausted {
		t.Fatalf("budget=%d status = %s, want budget_exhausted", need-1, short.Status)
	}
	if short.Status == StatusOptimal {
		t.Fatal("must not claim optimality after budget exhaustion")
	}
	optCost, _ := new(big.Int).SetString(full.Solution.TotalBytes, 10)
	lb, ok := new(big.Int).SetString(short.LowerBound, 10)
	if !ok || lb.Sign() < 0 {
		t.Fatalf("invalid lower bound %q", short.LowerBound)
	}
	if lb.Cmp(optCost) > 0 {
		t.Fatalf("lower bound %s exceeds true optimum %s", lb, optCost)
	}
	if short.Solution != nil {
		got, _ := new(big.Int).SetString(short.Solution.TotalBytes, 10)
		if got.Cmp(lb) < 0 {
			t.Fatalf("solution cost %s below its own lower bound %s", got, lb)
		}
		if got.Cmp(optCost) < 0 {
			t.Fatalf("reported solution %s beats the proven optimum %s", got, optCost)
		}
	}
}

// TestDiagnosticsSeparateKinds: dependency cycles, missing IDs and
// corruption are three separately diagnosed categories.
func TestDiagnosticsSeparateKinds(t *testing.T) {
	cycle := &Input{Files: []FileInput{
		{ID: "a", Bytes: nat(1), Dependencies: []string{"b"}},
		{ID: "b", Bytes: nat(1), Dependencies: []string{"a"}},
	}}
	out := solve(cycle, -1)
	if out.Status != StatusError || len(out.Errors) == 0 || out.Errors[0].Kind != "dependency_cycle" {
		t.Fatalf("cycle: %+v", out)
	}

	missing := &Input{Files: []FileInput{
		{ID: "a", Bytes: nat(1), Dependencies: []string{"ghost"}},
	}}
	out = solve(missing, -1)
	if out.Status != StatusError || out.Errors[0].Kind != "missing_id" {
		t.Fatalf("missing dep: %+v", out)
	}

	missingEdge := &Input{
		Files:  []FileInput{{ID: "a", Bytes: nat(1)}},
		Shards: []ShardInput{{Name: "s", Target: 1, Edges: []EdgeInput{{From: 0, To: 1, File: "ghost"}}}},
	}
	out = solve(missingEdge, -1)
	if out.Status != StatusError || out.Errors[0].Kind != "missing_id" {
		t.Fatalf("missing edge file: %+v", out)
	}

	dup := &Input{Files: []FileInput{{ID: "a"}, {ID: "a"}}}
	out = solve(dup, -1)
	if out.Status != StatusError || out.Errors[0].Kind != "duplicate_file_id" {
		t.Fatalf("duplicate: %+v", out)
	}

	// Corruption is a soft diagnostic, not a hard error.
	corrupt := &Input{
		Files:  []FileInput{{ID: "c", Bytes: nat(1), Corrupted: true}},
		Shards: []ShardInput{{Name: "s", Target: 0}},
	}
	out = solve(corrupt, -1)
	if out.Status != StatusOptimal {
		t.Fatalf("corruption-only input status = %s", out.Status)
	}
	if !reflect.DeepEqual(out.Diagnostics.CorruptedFiles, []string{"c"}) {
		t.Fatalf("corrupted diagnostics = %v", out.Diagnostics.CorruptedFiles)
	}
}

// TestReproducible: identical input and budget must produce identical output.
func TestReproducible(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "f1", Bytes: nat(7)}, {ID: "f2", Bytes: nat(3)},
			{ID: "f3", Bytes: nat(9)}, {ID: "f4", Bytes: nat(0)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 3, Edges: []EdgeInput{
				{From: 0, To: 1, File: "f1"}, {From: 1, To: 3, File: "f2"},
				{From: 0, To: 3, File: "f3"},
			}},
		},
	}
	for _, b := range []int64{-1, 0, 1, 2, 5} {
		a := solve(in, b)
		c := solve(in, b)
		if !reflect.DeepEqual(a, c) {
			t.Fatalf("budget %d: non-deterministic output\n%+v\n%+v", b, a, c)
		}
	}
}
