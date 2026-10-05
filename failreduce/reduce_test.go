package main

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

// fakeOracle judges configurations in memory and records every configuration
// it was asked about, asserting that only dependency-legal configurations
// are ever presented.
type fakeOracle struct {
	judge   func(cfg []string) Verdict
	deps    *Deps
	calls   [][]string
	illegal [][]string
}

func (f *fakeOracle) Test(cfg []string) (Verdict, error) {
	norm := normalize(cfg)
	if f.deps != nil {
		if err := f.deps.CheckLegal(norm); err != nil {
			f.illegal = append(f.illegal, norm)
		}
	}
	f.calls = append(f.calls, norm)
	return f.judge(norm), nil
}

func contains(cfg []string, item string) bool {
	i := sort.SearchStrings(cfg, item)
	return i < len(cfg) && cfg[i] == item
}

func (f *fakeOracle) distinctConfigs() map[string]bool {
	set := make(map[string]bool)
	for _, cfg := range f.calls {
		set[configKey(cfg)] = true
	}
	return set
}

func TestReduceToOneMinimalBasic(t *testing.T) {
	fake := &fakeOracle{judge: func(cfg []string) Verdict {
		if contains(cfg, "c") && contains(cfg, "f") {
			return TestFail
		}
		return Pass
	}}
	oracle := NewCachingOracle(fake)

	got, err := Reduce([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, NewDeps(), oracle)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reduced = %v, want %v", got, want)
	}

	// The result must satisfy the 1-minimality guarantee.
	ok, err := verifyOneMinimal(got, NewDeps(), oracle)
	if err != nil || !ok {
		t.Fatalf("result not 1-minimal: ok=%v err=%v", ok, err)
	}

	// Invocations must equal the number of distinct normalized configs, and
	// every oracle query must be accounted for as invocation or cache hit.
	stats := oracle.Stats
	if stats.Invocations != len(fake.distinctConfigs()) {
		t.Fatalf("invocations = %d, distinct configs = %d", stats.Invocations, len(fake.distinctConfigs()))
	}
	if stats.Invocations != len(fake.calls) {
		t.Fatalf("inner oracle consulted %d times, invocations reported %d", len(fake.calls), stats.Invocations)
	}
	if stats.TestFailures < 2 {
		t.Fatalf("expected at least the initial and final failing judgements, got %+v", stats)
	}
}

func TestReduceKeepsDependenciesLegal(t *testing.T) {
	deps := NewDeps()
	deps.Add("app", "lib")
	deps.Add("lib", "core")

	fake := &fakeOracle{
		deps: deps,
		judge: func(cfg []string) Verdict {
			if contains(cfg, "app") {
				return TestFail
			}
			return Pass
		},
	}
	oracle := NewCachingOracle(fake)

	got, err := Reduce([]string{"app", "lib", "core", "noise1", "noise2"}, deps, oracle)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app", "core", "lib"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reduced = %v, want %v (dependency chain must survive)", got, want)
	}
	if len(fake.illegal) > 0 {
		t.Fatalf("oracle was queried with illegal configurations: %v", fake.illegal)
	}
	ok, err := verifyOneMinimal(got, deps, oracle)
	if err != nil || !ok {
		t.Fatalf("result not 1-minimal: ok=%v err=%v", ok, err)
	}
}

func TestReduceDistinguishesCompileFailures(t *testing.T) {
	// b only compiles alongside x; the target test failure needs a, x and
	// y together. Reducing away x while keeping b yields a compile failure,
	// which must not be mistaken for the target failure.
	judge := func(cfg []string) Verdict {
		if contains(cfg, "b") && !contains(cfg, "x") {
			return CompileFail
		}
		if contains(cfg, "a") && contains(cfg, "x") && contains(cfg, "y") {
			return TestFail
		}
		return Pass
	}
	fake := &fakeOracle{judge: judge}
	oracle := NewCachingOracle(fake)

	got, err := Reduce([]string{"a", "b", "x", "y"}, NewDeps(), oracle)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "x", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reduced = %v, want %v", got, want)
	}
	stats := oracle.Stats
	if stats.CompileFailures == 0 {
		t.Fatalf("expected the reduction to encounter compile failures, got %+v", stats)
	}
	// Compile failures must never be accepted: the final config itself must
	// be a genuine target test failure.
	if judge(got) != TestFail {
		t.Fatalf("final config verdict = %s, want %s", judge(got), TestFail)
	}
}

func TestReduceRejectsNonFailingInitialConfig(t *testing.T) {
	fake := &fakeOracle{judge: func(cfg []string) Verdict { return Pass }}
	_, err := Reduce([]string{"a", "b"}, NewDeps(), NewCachingOracle(fake))
	if !errors.Is(err, ErrNotFailing) {
		t.Fatalf("err = %v, want ErrNotFailing", err)
	}
}

func TestReduceRejectsCompileFailingInitialConfig(t *testing.T) {
	fake := &fakeOracle{judge: func(cfg []string) Verdict { return CompileFail }}
	_, err := Reduce([]string{"a", "b"}, NewDeps(), NewCachingOracle(fake))
	if !errors.Is(err, ErrNotFailing) {
		t.Fatalf("err = %v, want ErrNotFailing (compile failure is not the target failure)", err)
	}
}

func TestReduceRejectsIllegalInitialConfig(t *testing.T) {
	deps := NewDeps()
	deps.Add("a", "b")
	fake := &fakeOracle{judge: func(cfg []string) Verdict { return TestFail }}
	_, err := Reduce([]string{"a"}, deps, NewCachingOracle(fake))
	if !errors.Is(err, ErrIllegalConfig) {
		t.Fatalf("err = %v, want ErrIllegalConfig", err)
	}
}

func TestReduceRejectsEmptyConfig(t *testing.T) {
	fake := &fakeOracle{judge: func(cfg []string) Verdict { return TestFail }}
	_, err := Reduce(nil, NewDeps(), NewCachingOracle(fake))
	if !errors.Is(err, ErrIllegalConfig) {
		t.Fatalf("err = %v, want ErrIllegalConfig", err)
	}
}

func TestReduceEmptyResultWhenEmptyConfigFails(t *testing.T) {
	// If even the empty configuration fails, everything can be removed.
	fake := &fakeOracle{judge: func(cfg []string) Verdict { return TestFail }}
	got, err := Reduce([]string{"a", "b", "c"}, NewDeps(), NewCachingOracle(fake))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reduced = %v, want empty", got)
	}
}

func TestReduceDeterministicInvocationCount(t *testing.T) {
	judge := func(cfg []string) Verdict {
		if contains(cfg, "k3") && contains(cfg, "k7") {
			return TestFail
		}
		return Pass
	}
	items := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}

	run := func() ([]string, Stats) {
		oracle := NewCachingOracle(&fakeOracle{judge: judge})
		got, err := Reduce(items, NewDeps(), oracle)
		if err != nil {
			t.Fatal(err)
		}
		return got, oracle.Stats
	}
	got1, stats1 := run()
	got2, stats2 := run()
	if !reflect.DeepEqual(got1, got2) || stats1 != stats2 {
		t.Fatalf("reduction must be deterministic: (%v,%+v) vs (%v,%+v)", got1, stats1, got2, stats2)
	}
	if want := []string{"k3", "k7"}; !reflect.DeepEqual(got1, want) {
		t.Fatalf("reduced = %v, want %v", got1, want)
	}
}
