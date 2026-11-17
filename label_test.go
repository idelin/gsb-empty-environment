package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q-003"
)

func TestLabelHoldsAllSixteenSources(t *testing.T) {
	sources := make([]netsec.Source, netsec.MaxSources)
	for i := range sources {
		sources[i] = netsec.Source(i)
	}
	label := netsec.NewLabel(sources...)
	if got := label.Count(); got != netsec.MaxSources {
		t.Fatalf("Count() = %d, want %d", got, netsec.MaxSources)
	}
	if label.Contains(netsec.Source(netsec.MaxSources)) {
		t.Fatal("out-of-range source unexpectedly present")
	}
	if got := label.Remove(netsec.NewLabel(sources[0], sources[15])); got.Count() != netsec.MaxSources-2 {
		t.Fatalf("Remove count = %d, want %d", got.Count(), netsec.MaxSources-2)
	}
}

func TestOutOfRangeSourcesAreRejected(t *testing.T) {
	bad := netsec.Source(netsec.MaxSources)
	if _, err := netsec.NewRequest(map[netsec.Source]int{bad: 1}); !errors.Is(err, netsec.ErrInvalidSource) {
		t.Fatalf("NewRequest error = %v, want ErrInvalidSource", err)
	}

	request := mustRequest(t, map[netsec.Source]int{sourceAlpha: 1})
	executor := netsec.NewExecutor(netsec.NewRegistrar(), nil)
	result, err := executor.Execute(request, netsec.Output(netsec.Secret(bad)))
	if !errors.Is(err, netsec.ErrInvalidSource) {
		t.Fatalf("Execute error = %v, want ErrInvalidSource", err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("invalid source exposed %d partial outputs", len(result.Outputs))
	}
}
