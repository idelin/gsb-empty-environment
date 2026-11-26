package krb5policy

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustExpand(t *testing.T, encoded, client, server string, opts TransitedOptions) *ExpandedTransited {
	t.Helper()
	exp, err := ExpandTransited([]byte(encoded), client, server, opts)
	if err != nil {
		t.Fatalf("ExpandTransited(%q): %v", encoded, err)
	}
	return exp
}

func TestDNSCompressionRoundTrip(t *testing.T) {
	realms := []string{"A.B.C", "D.B.C", "D.E.C", "D.E.F", "E.F.G"}
	encoded, err := EncodeTransited(realms, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(encoded)
	want := "A.B.C,.D,.D.E,D.E.F,E.F.G"
	if got != want {
		t.Fatalf("encoded = %q; want %q", got, want)
	}
	exp := mustExpand(t, got, "CLIENT.ORG", "SERVER.ORG", TransitedOptions{})
	if !reflect.DeepEqual(exp.Realms, realms) {
		t.Fatalf("Realms = %v; want %v", exp.Realms, realms)
	}
	if !exp.Complete || !exp.ImplicitFinalAdded {
		t.Fatalf("path not completed: %+v", exp)
	}
	if got := exp.FullPath; len(got) != 7 || got[0] != "CLIENT.ORG" || got[6] != "SERVER.ORG" {
		t.Fatalf("FullPath = %v", got)
	}
}

func TestFirstRelativeDNSCompressionUsesClientRealm(t *testing.T) {
	exp := mustExpand(t, ".D", "A.B.C", "S.ORG", TransitedOptions{})
	if len(exp.Realms) != 1 || exp.Realms[0] != "D.B.C" {
		t.Fatalf("Realms = %v", exp.Realms)
	}
	if len(exp.FullPath) != 3 || exp.FullPath[0] != "A.B.C" || exp.FullPath[2] != "S.ORG" {
		t.Fatalf("FullPath = %v", exp.FullPath)
	}
}

func TestX500CompressionRoundTrip(t *testing.T) {
	realms := []string{
		"/C=US/O=MIT/OU=ATHENA",
		"/C=US/O=MIT/OU=CSAIL",
	}
	encoded, err := EncodeTransited(realms, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), "/C=US/O=MIT/OU=ATHENA,/OU=CSAIL"; got != want {
		t.Fatalf("encoded = %q; want %q", got, want)
	}
	exp := mustExpand(t, string(encoded), "/C=US/O=CLIENT", "/C=US/O=SERVER", TransitedOptions{})
	if !reflect.DeepEqual(exp.Realms, realms) {
		t.Fatalf("Realms = %v; want %v", exp.Realms, realms)
	}
}

func TestX500DeeperMoveIsNotMiscompressed(t *testing.T) {
	realms := []string{"/C=US/O=MIT", "/C=US/O=MIT/OU=CSAIL"}
	encoded, err := EncodeTransited(realms, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	exp := mustExpand(t, string(encoded), "/C=US/O=CLIENT", "/C=US/O=SERVER", TransitedOptions{})
	if !reflect.DeepEqual(exp.Realms, realms) {
		t.Fatalf("Realms = %v; want %v (encoded %q)", exp.Realms, realms, encoded)
	}
}

func TestTrailingCommaDisablesImplicitCompletion(t *testing.T) {
	empty := mustExpand(t, "", "CLIENT.ORG", "CORE.ORG", TransitedOptions{})
	if !reflect.DeepEqual(empty.FullPath, []string{"CLIENT.ORG", "CORE.ORG"}) || !empty.Complete || !empty.ImplicitFinalAdded {
		t.Fatalf("empty field = %+v", empty)
	}
	emptyDisabled := mustExpand(t, "", "CLIENT.ORG", "CORE.ORG", TransitedOptions{DisableImplicitFinal: true})
	if !reflect.DeepEqual(emptyDisabled.FullPath, []string{"CLIENT.ORG"}) || emptyDisabled.Complete {
		t.Fatalf("empty field with disabled completion = %+v", emptyDisabled)
	}

	noComma := mustExpand(t, "EDGE.ORG", "CLIENT.ORG", "CORE.ORG", TransitedOptions{})
	if !reflect.DeepEqual(noComma.FullPath, []string{"CLIENT.ORG", "EDGE.ORG", "CORE.ORG"}) {
		t.Fatalf("no-comma FullPath = %v", noComma.FullPath)
	}
	if !noComma.ImplicitFinalAdded || !noComma.Complete {
		t.Fatalf("no-comma flags = %+v", noComma)
	}

	withComma := mustExpand(t, "EDGE.ORG,", "CLIENT.ORG", "CORE.ORG", TransitedOptions{})
	if !withComma.TrailingComma || withComma.ImplicitFinalAdded || withComma.Complete {
		t.Fatalf("trailing-comma flags = %+v", withComma)
	}
	if !reflect.DeepEqual(withComma.FullPath, []string{"CLIENT.ORG", "EDGE.ORG"}) {
		t.Fatalf("trailing-comma FullPath = %v", withComma.FullPath)
	}

	emptyComma := mustExpand(t, ",", "CLIENT.ORG", "CORE.ORG", TransitedOptions{})
	if len(emptyComma.Realms) != 0 || emptyComma.ImplicitFinalAdded || emptyComma.Complete {
		t.Fatalf("lone comma = %+v", emptyComma)
	}
	if !reflect.DeepEqual(emptyComma.FullPath, []string{"CLIENT.ORG"}) {
		t.Fatalf("lone comma FullPath = %v", emptyComma.FullPath)
	}

	disabled := mustExpand(t, "EDGE.ORG", "CLIENT.ORG", "CORE.ORG", TransitedOptions{DisableImplicitFinal: true})
	if disabled.ImplicitFinalAdded || disabled.Complete {
		t.Fatalf("disabled implicit final = %+v", disabled)
	}
}

func TestInteriorEmptyRepeatsPreviousRealm(t *testing.T) {
	exp := mustExpand(t, "A.B.C,,D.B.C", "CLIENT.ORG", "SERVER.ORG", TransitedOptions{})
	if !reflect.DeepEqual(exp.Realms, []string{"A.B.C", "A.B.C", "D.B.C"}) {
		t.Fatalf("Realms = %v", exp.Realms)
	}
	if _, err := ExpandTransited([]byte("A.B.C,,D.B.C"), "CLIENT.ORG", "SERVER.ORG", TransitedOptions{DisallowInteriorEmpty: true}); !errors.Is(err, ErrTransitedMalformed) {
		t.Fatalf("DisallowInteriorEmpty error = %v", err)
	}
}

func TestTransitedBounds(t *testing.T) {
	if _, err := ExpandTransited([]byte(strings.Repeat("A", 20)), "C", "S", TransitedOptions{Limits: Limits{MaxEncodedBytes: 10}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("encoded-byte bound error = %v", err)
	}
	if _, err := ExpandTransited([]byte("A.B.C"), "C", "S", TransitedOptions{Limits: Limits{MaxComponents: 2}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("component bound error = %v", err)
	}
	if _, err := ExpandTransited([]byte(strings.Repeat("A", 20)), "C", "S", TransitedOptions{Limits: Limits{MaxRealmBytes: 10, MaxEncodedBytes: 100}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("realm-byte bound error = %v", err)
	}
	if _, err := ExpandTransited([]byte("A,B,C"), "C", "S", TransitedOptions{Limits: Limits{MaxItems: 2}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("item bound error = %v", err)
	}
	if _, err := ExpandTransited([]byte("A,B"), "C", "S", TransitedOptions{Limits: Limits{MaxExpandedRealms: 1}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("expanded realm bound error = %v", err)
	}
	if _, err := ExpandTransited([]byte("AAAAAAAAAA,BBBBBBBBBB"), "C", "S", TransitedOptions{Limits: Limits{MaxExpandedBytes: 15}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("expanded byte bound error = %v", err)
	}
	if _, err := EncodeTransited([]string{strings.Repeat("A", 20)}, EncodeOptions{Limits: Limits{MaxRealmBytes: 10}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("encode realm bound error = %v", err)
	}
	if _, err := EncodeTransited([]string{"A", "B", "C"}, EncodeOptions{Limits: Limits{MaxItems: 2}}); !errors.Is(err, ErrTransitedLimit) {
		t.Fatalf("encode item bound error = %v", err)
	}
}

func TestTransitedMalformed(t *testing.T) {
	cases := []struct {
		encoded string
		client  string
		server  string
	}{
		{".D", "", "S"},
		{".A.B.C.D", "A.B.C", "S"},
		{"FOO..BAR", "C", "S"},
		{"/C=US//O=MIT", "/C=US", "S"}, // empty X.500 component
	}
	for _, tc := range cases {
		if _, err := ExpandTransited([]byte(tc.encoded), tc.client, tc.server, TransitedOptions{}); err == nil {
			t.Fatalf("expected malformed error for %q", tc.encoded)
		}
	}
	if _, err := ExpandTransited([]byte("A\x00B"), "C", "S", TransitedOptions{}); err == nil {
		t.Fatal("expected control-character rejection")
	}
	if err := ValidateTransitedEncoding(TransitedEncoding{Type: 2}, Limits{}); !errors.Is(err, ErrTransitedEncoding) {
		t.Fatalf("bad type error = %v", err)
	}
}

func TestRoundTripRandomRealmPaths(t *testing.T) {
	rng := rand.New(rand.NewSource(4120))
	labels := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	for i := 0; i < 500; i++ {
		depth := 2 + rng.Intn(4)
		var realms []string
		suffix := randomDNSName(rng, labels, depth)
		for j := 0; j < 1+rng.Intn(5); j++ {
			prefix := randomDNSName(rng, labels, depth-1)
			realms = append(realms, prefix+"."+suffix)
		}
		encoded, err := EncodeTransited(realms, EncodeOptions{})
		if err != nil {
			t.Fatalf("encode %v: %v", realms, err)
		}
		exp := mustExpand(t, string(encoded), "CLIENT.ORG", "SERVER.ORG", TransitedOptions{})
		if !reflect.DeepEqual(exp.Realms, realms) {
			t.Fatalf("round trip %v -> %q -> %v", realms, encoded, exp.Realms)
		}
	}
}

func randomDNSName(rng *rand.Rand, labels []string, depth int) string {
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = labels[rng.Intn(len(labels))]
	}
	return strings.Join(parts, ".")
}

func TestEncodeEmptyTrailingComma(t *testing.T) {
	encoded, err := EncodeTransited(nil, EncodeOptions{TrailingComma: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "," {
		t.Fatalf("encoded = %q", encoded)
	}
}

func TestRealmMismatchKindsAreNotCompressed(t *testing.T) {
	realms := []string{"A.B.C", "/C=US/O=MIT"}
	encoded, err := EncodeTransited(realms, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != "A.B.C,/C=US/O=MIT" {
		t.Fatalf("encoded = %q", got)
	}
	exp := mustExpand(t, string(encoded), "CLIENT.ORG", "SERVER.ORG", TransitedOptions{})
	if !reflect.DeepEqual(exp.Realms, realms) {
		t.Fatalf("Realms = %v; want %v", exp.Realms, realms)
	}
}

func ExampleExpandTransited() {
	exp, _ := ExpandTransited([]byte("A.B.C,.D,.D.E,D.E.F,E.F.G"), "CLIENT.ORG", "SERVER.ORG", TransitedOptions{})
	fmt.Println(exp.Realms)
	// Output: [A.B.C D.B.C D.E.C D.E.F E.F.G]
}
