package policy_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.org/netsec/q0271/policy"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

const baseline = "# baseline policy\nallow /admin\ndeny /guest\n"

// Both endpoints must consume the exact same lexer. These helpers keep the
// test honest: the verifier only asks for normalized lines and a digest, while
// the runtime asks for parsed rules.
func verifierEndpoint(path string) (*policy.Document, error) {
	return policy.LexFile(path)
}

func runtimeEndpoint(path string) (*policy.Policy, error) {
	return policy.ParseFile(path)
}

func writePolicy(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

type outcome struct {
	verifierAccepted bool
	runtimeAccepted  bool
	verifierErr      error
	runtimeErr       error
	document         *policy.Document
	parsed           *policy.Policy
}

func evaluate(t *testing.T, path string) outcome {
	t.Helper()
	doc, verifierErr := verifierEndpoint(path)
	parsed, runtimeErr := runtimeEndpoint(path)
	return outcome{
		verifierAccepted: verifierErr == nil,
		runtimeAccepted:  runtimeErr == nil,
		verifierErr:      verifierErr,
		runtimeErr:       runtimeErr,
		document:         doc,
		parsed:           parsed,
	}
}

func TestBOMRulesAgreeAcrossEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		accepted bool
		// wantBytes is the exact digest input after the single allowed strip.
		wantBytes []byte
	}{
		{
			name:      "no BOM",
			body:      []byte(baseline),
			accepted:  true,
			wantBytes: []byte(baseline),
		},
		{
			name:      "leading UTF-8 BOM",
			body:      append(append([]byte(nil), utf8BOM...), baseline...),
			accepted:  true,
			wantBytes: []byte(baseline),
		},
		{
			name:     "BOM at line start",
			body:     []byte("allow /guest\n\uFEFFallow /admin\n"),
			accepted: false,
		},
		{
			name:     "BOM inside key",
			body:     []byte("allow /guest\nal\uFEFFlow /admin\n"),
			accepted: false,
		},
		{
			name:     "two leading UTF-8 BOMs",
			body:     append(append(append([]byte(nil), utf8BOM...), utf8BOM...), baseline...),
			accepted: false,
		},
		{
			name:     "leading UTF-8 BOM followed by line-start BOM",
			body:     append(append([]byte(nil), utf8BOM...), []byte("allow /guest\n\uFEFFallow /admin\n")...),
			accepted: false,
		},
		{
			name:     "U+FEFF in value",
			body:     []byte("allow /admin\uFEFF\n"),
			accepted: false,
		},
		{
			name:     "UTF-16 LE BOM",
			body:     []byte{0xFF, 0xFE, 'a', 0x00},
			accepted: false,
		},
		{
			name:     "UTF-16 BE BOM",
			body:     []byte{0xFE, 0xFF, 0x00, 'a'},
			accepted: false,
		},
		{
			name:     "UTF-32 LE BOM",
			body:     []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0x00, 0x00, 0x00},
			accepted: false,
		},
		{
			name:     "UTF-32 BE BOM",
			body:     []byte{0x00, 0x00, 0xFE, 0xFF, 0x00, 0x00, 0x00, 'a'},
			accepted: false,
		},
		{
			name:     "UTF-32 2143 BOM",
			body:     []byte{0xFE, 0xFF, 0x00, 0x00, 'a', 0x00, 0x00, 0x00},
			accepted: false,
		},
		{
			name:     "UTF-32 3412 BOM",
			body:     []byte{0x00, 0x00, 0xFF, 0xFE, 0x00, 0x00, 0x00, 'a'},
			accepted: false,
		},
		{
			name:     "invalid UTF-8",
			body:     []byte("allow /admin\n\xFF"),
			accepted: false,
		},
		{
			name:      "empty file",
			body:      nil,
			accepted:  true,
			wantBytes: []byte{},
		},
		{
			name:      "BOM-only file",
			body:      append([]byte(nil), utf8BOM...),
			accepted:  true,
			wantBytes: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, "policy", tt.body)
			got := evaluate(t, path)

			if got.verifierAccepted != tt.accepted || got.runtimeAccepted != tt.accepted {
				t.Fatalf("endpoint disagreement: verifier accepted=%v, runtime accepted=%v, want accepted=%v (verifier error=%v, runtime error=%v)",
					got.verifierAccepted, got.runtimeAccepted, tt.accepted, got.verifierErr, got.runtimeErr)
			}
			if got.verifierAccepted != got.runtimeAccepted {
				t.Fatalf("verifier and runtime reached different conclusions")
			}

			if !tt.accepted {
				if got.verifierErr == nil || got.runtimeErr == nil {
					t.Fatal("rejected policy returned a nil error")
				}
				if got.verifierErr.Error() != got.runtimeErr.Error() {
					t.Fatalf("endpoints returned different errors: verifier=%q runtime=%q", got.verifierErr, got.runtimeErr)
				}
				return
			}

			if got.document == nil || got.parsed == nil {
				t.Fatal("accepted policy returned a nil document")
			}
			if got.document.Hash != got.parsed.Hash {
				t.Fatal("verifier and runtime hashes differ")
			}
			if !bytes.Equal(got.document.Bytes, tt.wantBytes) {
				t.Fatalf("normalized bytes = %q, want %q", got.document.Bytes, tt.wantBytes)
			}

			wantHash := sha256.Sum256(tt.wantBytes)
			if got.document.Hash != wantHash {
				t.Fatalf("digest = %x, want SHA-256 of stripped bytes %x", got.document.Hash, wantHash)
			}
			if got.document.DigestHex() != got.parsed.DigestHex() {
				t.Fatal("digest strings differ")
			}
		})
	}
}

func TestHashIgnoresOnlyTheSingleLeadingBOM(t *testing.T) {
	withoutBOM, err := policy.Lex([]byte(baseline))
	if err != nil {
		t.Fatalf("Lex without BOM: %v", err)
	}
	withBOM, err := policy.Lex(append(append([]byte(nil), utf8BOM...), baseline...))
	if err != nil {
		t.Fatalf("Lex with BOM: %v", err)
	}
	if withoutBOM.Hash != withBOM.Hash {
		t.Fatal("leading BOM changed the digest")
	}
	if !bytes.Equal(withoutBOM.Bytes, withBOM.Bytes) {
		t.Fatal("leading BOM changed normalized bytes")
	}
}

func TestBodyBOMIsRejectedAtAnyOffset(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("allow /admin\n\uFEFF"),
		[]byte("allow /admin\uFEFF\n"),
		[]byte("al\uFEFFlow /admin\n"),
		[]byte("allow /admin\n\nal\uFEFFlow /guest\n"),
	} {
		if _, err := policy.Lex(body); err == nil {
			t.Fatalf("body BOM was accepted in %q", body)
		}
	}
}

func TestParseRecognizesAllowAndDeny(t *testing.T) {
	parsed, err := policy.Parse([]byte(baseline))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(parsed.Rules))
	}
	if parsed.Rules[0].Action != "allow" || parsed.Rules[0].Value != "/admin" {
		t.Fatalf("first rule = %+v", parsed.Rules[0])
	}
	if parsed.Rules[1].Action != "deny" || parsed.Rules[1].Value != "/guest" {
		t.Fatalf("second rule = %+v", parsed.Rules[1])
	}

	withoutBOM, err := policy.Parse([]byte(baseline))
	if err != nil {
		t.Fatal(err)
	}
	withBOM, err := policy.Parse(append(append([]byte(nil), utf8BOM...), baseline...))
	if err != nil {
		t.Fatal(err)
	}
	if withoutBOM.Hash != withBOM.Hash || len(withBOM.Rules) != len(withoutBOM.Rules) {
		t.Fatal("leading BOM changed runtime policy or hash")
	}
}

func TestUnknownKeyIsRejected(t *testing.T) {
	_, err := policy.Parse([]byte("alllow /admin\n"))
	if err == nil {
		t.Fatal("unknown key was accepted")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("error = %v, want unknown key", err)
	}
}
