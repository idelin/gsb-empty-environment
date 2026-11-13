package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func fixture(t *testing.T, values ...any) ([]string, []Disclosure) {
	t.Helper()
	commitment := make([]string, len(values))
	disclosures := make([]Disclosure, len(values))
	for index, value := range values {
		salt := "salt-" + string(rune('a'+index))
		encodedJSON, err := json.Marshal([]any{salt, value})
		if err != nil {
			t.Fatal(err)
		}
		encoded := string(encodedJSON)
		commitment[index] = HashDisclosure(encoded)
		disclosures[index] = Disclosure{Index: index, Disclosure: encoded}
	}
	return commitment, disclosures
}

func TestOriginalIndicesAccepted(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	got, err := VerifyArray(commitment, disclosures)
	if err != nil {
		t.Fatalf("VerifyArray: %v", err)
	}
	want := []any{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestMovedDisclosureRejected(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	// Keep the same digest but claim that the first element belongs at index 1.
	disclosures[0].Index = 1
	if _, err := VerifyArray(commitment, disclosures); !errors.Is(err, ErrIndexMismatch) {
		t.Fatalf("want ErrIndexMismatch, got %v", err)
	}
}

func TestSwappedDisclosuresRejected(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	// Swap the positions claimed by two deliberately disclosed elements.
	disclosures[0].Index, disclosures[1].Index = 1, 0
	if _, err := VerifyArray(commitment, disclosures); !errors.Is(err, ErrIndexMismatch) {
		t.Fatalf("want ErrIndexMismatch, got %v", err)
	}
}

func TestNonContiguousIndicesRejected(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	// Disclose positions 0 and 2 but leave position 1 hidden.
	selected := []Disclosure{disclosures[0], disclosures[2]}
	if _, err := VerifyArray(commitment, selected); !errors.Is(err, ErrNonContiguousIndices) {
		t.Fatalf("want ErrNonContiguousIndices, got %v", err)
	}
}

func TestSparseDisclosureCanBeExplicitlyAllowed(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	got, err := VerifyArrayWithOptions(
		commitment,
		[]Disclosure{disclosures[0], disclosures[2]},
		Options{AllowSparse: true},
	)
	if err != nil {
		t.Fatalf("VerifyArrayWithOptions: %v", err)
	}
	if got[0] != "alpha" || got[2] != "gamma" {
		t.Fatalf("unexpected reconstructed array: %#v", got)
	}
}

func TestRawDisclosureOrderDoesNotChangeIndices(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta", "gamma")

	raw := []string{
		disclosures[2].Disclosure,
		disclosures[0].Disclosure,
		disclosures[1].Disclosure,
	}
	got, err := VerifyDisclosures(commitment, raw)
	if err != nil {
		t.Fatalf("VerifyDisclosures: %v", err)
	}
	want := []any{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTamperedDisclosureRejected(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta")

	tampered, _ := json.Marshal([]any{"salt-a", "tampered"})
	disclosures[0].Disclosure = string(tampered)
	if _, err := VerifyArray(commitment, disclosures); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
}

func TestPresentationJSON(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta")
	document, err := json.Marshal(Presentation{
		Commitment:  []any{commitment[0], map[string]any{"...": commitment[1]}},
		Disclosures: disclosures,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := VerifyPresentationJSON(document)
	if err != nil {
		t.Fatalf("VerifyPresentationJSON: %v", err)
	}
	want := []any{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestStructuredDisclosureAccepted(t *testing.T) {
	commitment, _ := fixture(t, "alpha", "beta")
	disclosures := []Disclosure{
		{Index: 0, Salt: "salt-a", Value: "alpha"},
		{Index: 1, Salt: "salt-b", Value: "beta"},
	}

	got, err := VerifyArray(commitment, disclosures)
	if err != nil {
		t.Fatalf("VerifyArray: %v", err)
	}
	want := []any{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestValueOnlyDigestAccepted(t *testing.T) {
	values := []string{"alpha", "beta"}
	commitment := make([]string, len(values))
	disclosures := make([]Disclosure, len(values))
	for index, value := range values {
		sum := sha256.Sum256([]byte(value))
		commitment[index] = base64.RawURLEncoding.EncodeToString(sum[:])
		disclosures[index] = Disclosure{Index: index, Value: value}
	}

	got, err := VerifyArray(commitment, disclosures)
	if err != nil {
		t.Fatalf("VerifyArray: %v", err)
	}
	want := []any{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestIndexMapInput(t *testing.T) {
	commitment, disclosures := fixture(t, "alpha", "beta")
	byIndex := map[int]Disclosure{
		0: disclosures[0],
		1: disclosures[1],
	}
	if _, err := VerifyArrayMap(commitment, byIndex); err != nil {
		t.Fatalf("VerifyArrayMap: %v", err)
	}

	byIndex[1] = disclosures[0]
	if _, err := VerifyArrayMap(commitment, byIndex); !errors.Is(err, ErrIndexMismatch) {
		t.Fatalf("want ErrIndexMismatch, got %v", err)
	}
}
