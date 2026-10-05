package main

import (
	"reflect"
	"testing"
)

func TestNormalizeDedupesAndSorts(t *testing.T) {
	got := normalize([]string{"b", "a", "b", "a", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize = %v, want %v", got, want)
	}
}

func TestConfigKeyIsOrderIndependent(t *testing.T) {
	if configKey([]string{"b", "a"}) != configKey([]string{"a", "b", "a"}) {
		t.Fatal("configKey must be keyed by the normalized configuration")
	}
	if configKey([]string{"a", "b"}) == configKey([]string{"a", "c"}) {
		t.Fatal("configKey must distinguish different configurations")
	}
}

func TestCheckLegal(t *testing.T) {
	deps := NewDeps()
	deps.Add("a", "b")
	if err := deps.CheckLegal([]string{"a"}); err == nil {
		t.Fatal("expected illegality error for missing dependency")
	}
	if err := deps.CheckLegal([]string{"a", "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := deps.CheckLegal([]string{"b"}); err != nil {
		t.Fatalf("dependency direction wrong: %v", err)
	}
}

func TestRemovalClosureIsTransitive(t *testing.T) {
	deps := NewDeps()
	deps.Add("app", "lib")
	deps.Add("lib", "core")
	cfg := []string{"app", "core", "extra", "lib"}

	if got := deps.RemovalClosure(cfg, []string{"core"}); !reflect.DeepEqual(got, []string{"extra"}) {
		t.Fatalf("removing core must cascade to lib and app, got %v", got)
	}
	if got := deps.RemovalClosure(cfg, []string{"lib"}); !reflect.DeepEqual(got, []string{"core", "extra"}) {
		t.Fatalf("removing lib must cascade to app only, got %v", got)
	}
	if got := deps.RemovalClosure(cfg, []string{"app"}); !reflect.DeepEqual(got, []string{"core", "extra", "lib"}) {
		t.Fatalf("removing app must not remove its dependencies, got %v", got)
	}
}

func TestRemovalClosureHandlesCycles(t *testing.T) {
	deps := NewDeps()
	deps.Add("a", "b")
	deps.Add("b", "a")
	got := deps.RemovalClosure([]string{"a", "b", "c"}, []string{"a"})
	if !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("cyclic dependents must both be removed, got %v", got)
	}
}

func TestPartition(t *testing.T) {
	chunks := partition([]string{"a", "b", "c", "d", "e"}, 2)
	if len(chunks) != 2 || len(chunks[0]) != 3 || len(chunks[1]) != 2 {
		t.Fatalf("unexpected partition: %v", chunks)
	}
	chunks = partition([]string{"a", "b"}, 5)
	if len(chunks) != 2 {
		t.Fatalf("partition must not exceed item count: %v", chunks)
	}
}
