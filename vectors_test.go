package tls13

import (
	"encoding/hex"
	"strings"
	"testing"
)

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	clean := strings.NewReplacer(" ", "", "\n", "", "\t", "", "\r", "", ":", "").Replace(value)
	out, err := hex.DecodeString(clean)
	if err != nil {
		t.Fatalf("decode hex: %v\n%s", err, value)
	}
	return out
}

func assertHex(t *testing.T, name string, got []byte, wantHex string) {
	t.Helper()
	want := mustHex(t, wantHex)
	if !equalBytes(got, want) {
		t.Fatalf("%s mismatch\n got: %x\nwant: %x", name, got, want)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
