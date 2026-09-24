package canonicalkey

import (
	"errors"
	"fmt"
	"testing"
)

func TestGeneratedPairsHaveNoFalseEquivalence(t *testing.T) {
	checks, err := CheckPairs(GeneratePairs(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if counterexamples := Counterexamples(checks); len(counterexamples) != 0 {
		t.Fatalf("unexpected counterexamples: %#v", counterexamples)
	}
}

func TestPairProbeFindsInequivalentSameKeyCounterexample(t *testing.T) {
	badKey := func(request Request, limits Limits) (string, []byte, error) {
		if !request.ConfigVersion.Present {
			return "", nil, ErrMissingConfig
		}
		return fmt.Sprintf("tenant=%s/type=%s", request.Tenant, request.ParameterType), nil, nil
	}
	checks, err := CheckPairsWithKeyFunc(StandardPairs(), DefaultLimits(), badKey)
	if err != nil {
		t.Fatal(err)
	}
	counterexamples := Counterexamples(checks)
	if len(counterexamples) == 0 {
		t.Fatal("probe failed to find an inequivalent same-key counterexample")
	}
	found := false
	for _, counterexample := range counterexamples {
		if counterexample.Pair.Name == "numeric one versus string one" {
			found = true
		}
	}
	if !found {
		t.Fatalf("numeric/string counterexample was not found: %#v", counterexamples)
	}
}

func TestConfigVersionIsolationAndOmittedVersion(t *testing.T) {
	request := Request{"tenant-a", "search", PresentString("cfg-2"), PresentParams(Object{"mode": "strict"})}
	upgraded := request
	upgraded.ConfigVersion = PresentString("cfg-4")
	oldCanonical, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	newCanonical, err := CanonicalRequest(upgraded, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if DigestKey(oldCanonical, DefaultHash) == DigestKey(newCanonical, DefaultHash) {
		t.Fatal("old and upgraded configurations were not isolated")
	}

	omitted := request
	omitted.ConfigVersion = StringField{}
	_, err = CanonicalRequest(omitted, DefaultLimits())
	if !errors.Is(err, ErrMissingConfig) {
		t.Fatalf("omitted config version error = %v, want %v", err, ErrMissingConfig)
	}
}
