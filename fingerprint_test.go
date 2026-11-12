package netsec

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestFingerprintOfCertificateUsesDERBody(t *testing.T) {
	if got := FingerprintOfCertificate(nil); !got.IsZero() {
		t.Fatalf("FingerprintOfCertificate(nil) = %s, want zero", got)
	}
	if got := FingerprintOfDER(nil); !got.IsZero() {
		t.Fatalf("FingerprintOfDER(nil) = %s, want zero", got)
	}
	der := []byte("certificate-der")
	want := Fingerprint(sha256.Sum256(der))
	if got := FingerprintOfDER(der); got != want {
		t.Fatalf("FingerprintOfDER() = %s, want %s", got, want)
	}
	if got := FingerprintOfCertificate(testCertificate("certificate-der")); got != want {
		t.Fatalf("FingerprintOfCertificate() = %s, want %s", got, want)
	}
}

func TestFingerprintParseAndFormat(t *testing.T) {
	fingerprint := FingerprintOfDER([]byte("certificate"))
	text := fingerprint.String()
	if len(text) != FingerprintSize*2 {
		t.Fatalf("String() length = %d, want %d", len(text), FingerprintSize*2)
	}

	parsed, err := ParseFingerprint(text)
	if err != nil {
		t.Fatalf("ParseFingerprint(lowercase) error = %v", err)
	}
	if parsed != fingerprint {
		t.Fatalf("ParseFingerprint(lowercase) = %s, want %s", parsed, fingerprint)
	}

	colonized := strings.ToUpper(text[:2]) + ":" + text[2:4] + ":" + strings.Join(splitPairs(text[4:]), ":")
	parsed, err = ParseFingerprint("sha256:" + colonized)
	if err != nil {
		t.Fatalf("ParseFingerprint(colonized) error = %v", err)
	}
	if parsed != fingerprint {
		t.Fatalf("ParseFingerprint(colonized) = %s, want %s", parsed, fingerprint)
	}

	for _, invalid := range []string{"", "xyz", strings.Repeat("0", FingerprintSize*2), text + "00"} {
		if _, err := ParseFingerprint(invalid); err == nil {
			t.Fatalf("ParseFingerprint(%q) error = nil, want error", invalid)
		}
	}
}

func TestFingerprintTextMarshalling(t *testing.T) {
	fingerprint := FingerprintOfDER([]byte("certificate"))
	text, err := fingerprint.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	var decoded Fingerprint
	if err := decoded.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText() error = %v", err)
	}
	if decoded != fingerprint {
		t.Fatalf("round trip = %s, want %s", decoded, fingerprint)
	}
	var zero Fingerprint
	if _, err := zero.MarshalText(); err == nil {
		t.Fatal("zero Fingerprint.MarshalText() error = nil, want error")
	}
}

func splitPairs(s string) []string {
	var pairs []string
	for i := 0; i+1 < len(s); i += 2 {
		pairs = append(pairs, s[i:i+2])
	}
	return pairs
}
