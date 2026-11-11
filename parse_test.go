package knownhosts

import (
	"errors"
	"strings"
	"testing"
)

func TestParseErrors(t *testing.T) {
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
	encoded := encodeLine("host.example.com", key)

	cases := []struct {
		name string
		line string
	}{
		{"only one field", "host.example.com"},
		{"bad base64", "host.example.com not-base64!!"},
		{"truncated key blob", "host.example.com AAAA"},
		{"bad hash base64", "|1|@@@|### " + b64(key)},
		{"malformed hashed host", "|1|salt " + b64(key)},
		{"dangling negation", "! " + b64(key)},
		{"unterminated bracket", "[host.example.com " + b64(key)},
		{"bad bracket port", "[host.example.com]:abc " + b64(key)},
		{"marker without fields", "@revoked host.example.com"},
		{"wildcard key outside revoked", "host.example.com *"},
		{"empty pattern in list", "good.example.com,,bad.example.com " + b64(key)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Join([]string{encoded, tc.line, encoded}, "\n")
			_, err := NewFromReader(strings.NewReader(content))
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("expected *ParseError, got %T: %v", err, err)
			}
			if parseErr.Line != 2 {
				t.Fatalf("expected line number 2, got %d", parseErr.Line)
			}
		})
	}
}

func TestLineAndLines(t *testing.T) {
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}

	got, err := Line("host.example.com " + b64(key) + " a comment")
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if got.KeyType != "ssh-ed25519" || got.Line != 0 {
		t.Fatalf("unexpected KnownKey: %+v", got)
	}

	kh, err := NewFromReader(strings.NewReader(encodeLine("a.example.com", key) + "\n" +
		"@revoked b.example.com *\n"))
	if err != nil {
		t.Fatalf("NewFromReader: %v", err)
	}
	lines := kh.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].Line != 1 || lines[1].Line != 2 || lines[1].KeyType != "*" {
		t.Fatalf("unexpected lines: %+v", lines)
	}
}

func TestNewMissingFile(t *testing.T) {
	if _, err := New("/nonexistent/known_hosts"); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := NewFromReader(nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"*.example.com", "host.example.com", true},
		{"*.example.com", "example.com", false},
		{"host-?.example.com", "host-1.example.com", true},
		{"host-?.example.com", "host-12.example.com", false},
		{"*", "anything.at.all", true},
		{"exact.example.com", "exact.example.com", true},
		{"exact.example.com", "other.example.com", false},
		{"Host.Example.COM", "host.example.com", true},
		{"[::1]", "[::1]", true},
		{"prefix*", "prefix-suffix", true},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.input); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.input, got, tc.want)
		}
	}
}

func TestHashHostnameRoundTrip(t *testing.T) {
	cases := []struct {
		host      string
		port      int
		matchHost string
		matchPort int
		want      bool
	}{
		{"hashed.example.com", 22, "hashed.example.com", 22, true},
		{"hashed.example.com", 22, "hashed.example.com", 2222, false},
		{"hashed.example.com", 22, "other.example.com", 22, false},
		{"2001:db8::7", 22, "2001:db8::7", 22, true},
		{"Upper.Example.com", 22, "upper.example.com", 22, true},
	}
	for _, tc := range cases {
		hashed, err := HashHostname(tc.host, tc.port)
		if err != nil {
			t.Fatalf("HashHostname: %v", err)
		}
		if !strings.HasPrefix(hashed, "|1|") {
			t.Fatalf("unexpected hash format: %q", hashed)
		}
		key := mustEd25519Key(t)
		kh, err := NewFromReader(strings.NewReader(hashed + " " + b64(key)))
		if err != nil {
			t.Fatalf("parse hashed line: %v", err)
		}
		err = kh.HostKeyCallback(tc.matchHost, tc.matchPort, key)
		if tc.want && err != nil {
			t.Errorf("%s:%d vs %s:%d: expected match, got %v", tc.host, tc.port, tc.matchHost, tc.matchPort, err)
		}
		if !tc.want {
			var keyError *KeyError
			if !errors.As(err, &keyError) {
				t.Errorf("%s:%d vs %s:%d: expected KeyError, got %v", tc.host, tc.port, tc.matchHost, tc.matchPort, err)
			}
		}
	}
}

func TestConflictReportsKnownKeys(t *testing.T) {
	pub, _ := ed25519KeyPair()
	known := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
	otherPub, _ := ed25519KeyPair()
	presented := testPublicKey{"ssh-ed25519", marshalEd25519(otherPub)}

	kh, err := NewFromReader(strings.NewReader(encodeLine("conflict.example.com", known)))
	if err != nil {
		t.Fatalf("NewFromReader: %v", err)
	}
	err = kh.HostKeyCallback("conflict.example.com", 22, presented)
	var keyError *KeyError
	if !errors.As(err, &keyError) {
		t.Fatalf("expected KeyError, got %v", err)
	}
	if len(keyError.Want) != 1 || keyError.Want[0].KeyType != "ssh-ed25519" || keyError.Want[0].Line != 1 {
		t.Fatalf("conflict must report the recorded key and line: %+v", keyError.Want)
	}
	if keyError.Got.KeyType != presented.keyType {
		t.Fatalf("conflict must report the presented key: %+v", keyError.Got)
	}
	if !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestCallbackValue(t *testing.T) {
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
	kh, err := NewFromReader(strings.NewReader(encodeLine("cb.example.com", key)))
	if err != nil {
		t.Fatalf("NewFromReader: %v", err)
	}
	cb := kh.Callback()
	if err := cb("cb.example.com", 22, key); err != nil {
		t.Fatalf("callback: %v", err)
	}
}

func b64(key testPublicKey) string {
	return encodeB64(key)
}

func mustEd25519Key(t *testing.T) testPublicKey {
	t.Helper()
	pub, _ := ed25519KeyPair()
	return testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
}
