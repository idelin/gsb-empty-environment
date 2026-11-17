package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMergeLabels(t *testing.T) {
	inherited := Labels{"tenant": "acme", "env": "dev"}
	requested := Labels{"env": "prod", "owner": "alice"}
	mandatory := Labels{"classification": "secret"}

	final, err := MergeLabels(inherited, requested, mandatory)
	if err != nil {
		t.Fatalf("MergeLabels: %v", err)
	}
	want := Labels{
		"tenant":         "acme",
		"env":            "prod",
		"owner":          "alice",
		"classification": "secret",
	}
	if !final.Equal(want) {
		t.Fatalf("final labels = %#v, want %#v", final, want)
	}
	if !inherited.Equal(Labels{"tenant": "acme", "env": "dev"}) {
		t.Fatalf("MergeLabels mutated inherited labels: %#v", inherited)
	}
	if !requested.Equal(Labels{"env": "prod", "owner": "alice"}) {
		t.Fatalf("MergeLabels mutated requested labels: %#v", requested)
	}
	if !mandatory.Equal(Labels{"classification": "secret"}) {
		t.Fatalf("MergeLabels mutated mandatory labels: %#v", mandatory)
	}
}

func TestMergeLabelsRejectsMandatoryConflicts(t *testing.T) {
	tests := []struct {
		name      string
		inherited Labels
		requested Labels
		source    string
	}{
		{
			name:      "requested",
			inherited: nil,
			requested: Labels{"classification": "public"},
			source:    "requested",
		},
		{
			name:      "inherited",
			inherited: Labels{"classification": "public"},
			requested: nil,
			source:    "inherited",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MergeLabels(tc.inherited, tc.requested, Labels{"classification": "secret"})
			if !errors.Is(err, ErrImmutableLabel) {
				t.Fatalf("err = %v, want ErrImmutableLabel", err)
			}
			var conflict *ImmutableLabelError
			if !errors.As(err, &conflict) {
				t.Fatalf("err = %T, want *ImmutableLabelError", err)
			}
			if conflict.Key != "classification" || conflict.Source != tc.source {
				t.Fatalf("conflict = %#v", conflict)
			}
		})
	}
}

func TestMergeLabelsAllowsMatchingMandatoryValue(t *testing.T) {
	final, err := MergeLabels(
		Labels{"classification": "secret"},
		Labels{"classification": "secret"},
		Labels{"classification": "secret"},
	)
	if err != nil {
		t.Fatalf("MergeLabels: %v", err)
	}
	if final["classification"] != "secret" {
		t.Fatalf("classification = %q", final["classification"])
	}
}

func TestMergeLabelsRejectsInvalidSources(t *testing.T) {
	_, err := MergeLabels(nil, Labels{"bad key": "v"}, nil)
	if !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("err = %v, want ErrInvalidLabel", err)
	}

	tooMany := make(Labels, MaxLabels+1)
	for i := 0; i <= MaxLabels; i++ {
		tooMany[fmt.Sprintf("key-%d", i)] = "v"
	}
	_, err = MergeLabels(nil, tooMany, nil)
	if !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("err = %v, want ErrInvalidLabel", err)
	}
}

func TestMergeLabelsRejectsOversizedMergedSet(t *testing.T) {
	inherited := make(Labels, MaxLabels)
	requested := make(Labels, 1)
	for i := 0; i < MaxLabels; i++ {
		inherited[fmt.Sprintf("inherited-%d", i)] = "v"
	}
	requested["requested"] = "v"
	_, err := MergeLabels(inherited, requested, nil)
	if !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("err = %v, want ErrInvalidLabel", err)
	}
}

func TestLabelValidation(t *testing.T) {
	valid := []Labels{
		nil,
		{},
		{"owner": "alice"},
		{"team.example.com/role": "reader"},
		{"a_b:c-d/e.f": "value with spaces = and punctuation"},
		{"key": ""},
	}
	for _, labels := range valid {
		if err := labels.Validate(); err != nil {
			t.Errorf("%#v.Validate() = %v", labels, err)
		}
	}

	invalid := []Labels{
		{"": "v"},
		{"-leading": "v"},
		{"bad key": "v"},
		{"key": "a\x00b"},
		{"key": "line\nbreak"},
		{strings.Repeat("k", MaxLabelKeyBytes+1): "v"},
		{"key": strings.Repeat("v", MaxLabelValueBytes+1)},
	}
	for _, labels := range invalid {
		if err := labels.Validate(); !errors.Is(err, ErrInvalidLabel) {
			t.Errorf("%#v.Validate() = %v, want ErrInvalidLabel", labels, err)
		}
	}
}

func TestDigestIsOrderIndependentAndCanonical(t *testing.T) {
	a := Labels{"b": "2", "a": "1", "c": "3"}
	b := Labels{"c": "3", "a": "1", "b": "2"}
	if a.Digest() != b.Digest() {
		t.Fatal("digest depends on map iteration order")
	}
	if a.Digest() == (Labels{"a": "1", "b": "2", "c": "4"}).Digest() {
		t.Fatal("different labels produced the same digest")
	}

	// Length prefixes must prevent ambiguous concatenations.
	if (Labels{"ab": "c"}).Digest() == (Labels{"a": "bc"}).Digest() {
		t.Fatal("ambiguous canonical encoding")
	}
	if got := len(a.Digest().Hex()); got != 64 {
		t.Fatalf("hex digest length = %d, want 64", got)
	}
	if a.Digest().IsZero() {
		t.Fatal("non-zero labels produced zero digest")
	}
	var zero LabelDigest
	if !zero.IsZero() {
		t.Fatal("zero digest reported non-zero")
	}
}

func TestParseLabelPair(t *testing.T) {
	labels, err := ParseLabelPair("team=reader")
	if err != nil {
		t.Fatalf("ParseLabelPair: %v", err)
	}
	if labels["team"] != "reader" {
		t.Fatalf("labels = %#v", labels)
	}
	for _, input := range []string{"missing", "=value", "bad key=value"} {
		if _, err := ParseLabelPair(input); !errors.Is(err, ErrInvalidLabel) {
			t.Errorf("ParseLabelPair(%q) = %v, want ErrInvalidLabel", input, err)
		}
	}
}
