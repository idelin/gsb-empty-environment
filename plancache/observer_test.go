package plancache_test

import (
	"strings"
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

// TestObservableTrace: the trace must expose the complete lifecycle and an
// immediately invalidated plan must show an immediate-failure event with no
// operator event.
func TestObservableTrace(t *testing.T) {
	f := newFixture(t)
	var calls int
	tx, _ := f.exec.Begin(alice)
	plan, h, err := f.exec.Prepare(tx,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		countingFactory(&calls))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.exec.Enqueue(tx, h, plan)
	if err != nil {
		t.Fatal(err)
	}
	f.cat.Revoke(alice, ordersO, "SELECT", true)
	if err := f.exec.Dispatch(pending, []any{int64(1)}); err == nil {
		t.Fatal("dispatch must fail")
	}

	joined := strings.Join(f.obs.Events(), "|")
	for _, want := range []string{"compiled:", "revalidation-failed:", "immediate-failure:"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("trace missing %q, got %v", want, f.obs.Events())
		}
	}
	if strings.Contains(joined, "operator:") {
		t.Fatalf("invalidated plan must never emit an operator event, got %v", f.obs.Events())
	}
}
