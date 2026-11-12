package webauthn

import (
	"crypto/sha256"
	"errors"
	"testing"
)

const testRPID = "example-brand.com"

func testManifest(t *testing.T) *Manifest {
	t.Helper()
	manifest, err := ParseManifest([]byte(`{
		"origins": [
			"https://example-brand.com",
			"https://auth.example-brand.com",
			"https://www.example-brand.com",
			"https://brand2.example",
			"https://brand3.example:443"
		]
	}`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return manifest
}

func authenticatorData(t *testing.T, rpID string) []byte {
	t.Helper()
	hash := RPIDHash(rpID)
	data := make([]byte, 37) // 32-byte rpIdHash + flags + 4-byte counter
	copy(data, hash[:])
	return data
}

func TestCanonicalOrigin(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{name: "https origin", input: "https://example-brand.com", want: "https://example-brand.com", wantOK: true},
		{name: "default https port stripped", input: "https://example-brand.com:443", want: "https://example-brand.com", wantOK: true},
		{name: "custom port kept", input: "https://example-brand.com:8443", want: "https://example-brand.com:8443", wantOK: true},
		{name: "http loopback", input: "http://localhost:3000", want: "http://localhost:3000", wantOK: true},
		{name: "http loopback default port", input: "http://127.0.0.1:80", want: "http://127.0.0.1", wantOK: true},
		{name: "http remote rejected", input: "http://example-brand.com", wantOK: false},
		{name: "path rejected", input: "https://example-brand.com/login", wantOK: false},
		{name: "query rejected", input: "https://example-brand.com?x=1", wantOK: false},
		{name: "fragment rejected", input: "https://example-brand.com#frag", wantOK: false},
		{name: "userinfo rejected", input: "https://user@example-brand.com", wantOK: false},
		{name: "non http scheme rejected", input: "javascript:alert(1)", wantOK: false},
		{name: "trailing dot rejected", input: "https://example-brand.com.", wantOK: false},
		{name: "uppercase host rejected", input: "https://EXAMPLE-BRAND.com", wantOK: false},
		{name: "empty rejected", input: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalOrigin(tc.input)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("CanonicalOrigin(%q): unexpected error %v", tc.input, err)
				}
				if got != tc.want {
					t.Fatalf("CanonicalOrigin(%q) = %q, want %q", tc.input, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("CanonicalOrigin(%q) = %q, expected error", tc.input, got)
			}
		})
	}
}

func TestManifestExactMatchingBlocksLookalikes(t *testing.T) {
	manifest := testManifest(t)

	allowed := []string{
		"https://example-brand.com",
		"https://brand3.example:443",
	}
	for _, origin := range allowed {
		if !manifest.Allows(origin) {
			t.Errorf("manifest.Allows(%q) = false, want true", origin)
		}
	}

	blocked := []string{
		"https://examp1e-brand.com",
		"https://example-brand.com.evil.io",
		"https://login.example-brand.com",
		"https://example-brand.com:8443",
		"http://example-brand.com",
		"https://brand2.example:8443",
	}
	for _, origin := range blocked {
		if manifest.Allows(origin) {
			t.Errorf("manifest.Allows(%q) = true, want false", origin)
		}
	}
}

func TestParseManifestValidation(t *testing.T) {
	t.Run("five entries accepted", func(t *testing.T) {
		if got := len(testManifest(t).Origins); got != MaxManifestOrigins {
			t.Fatalf("len(Origins) = %d, want %d", got, MaxManifestOrigins)
		}
	})

	t.Run("sixth entry rejected", func(t *testing.T) {
		_, err := ParseManifest([]byte(`{"origins":[
			"https://a.example","https://b.example","https://c.example",
			"https://d.example","https://e.example","https://f.example"]}`))
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("empty list rejected", func(t *testing.T) {
		if _, err := ParseManifest([]byte(`{"origins":[]}`)); !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("duplicate rejected", func(t *testing.T) {
		_, err := ParseManifest([]byte(`{"origins":[
			"https://a.example","https://a.example:443"]}`))
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("invalid entry rejected", func(t *testing.T) {
		_, err := ParseManifest([]byte(`{"origins":["https://a.example/path"]}`))
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("malformed json rejected", func(t *testing.T) {
		if _, err := ParseManifest([]byte(`{not json`)); !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})
}

func TestValidateRPIDAndWellKnownURL(t *testing.T) {
	good := []string{"example-brand.com", "auth.example-brand.com", "localhost"}
	for _, rpID := range good {
		if err := ValidateRPID(rpID); err != nil {
			t.Errorf("ValidateRPID(%q): %v", rpID, err)
		}
	}

	bad := []string{
		"",
		".example-brand.com",
		"*.example-brand.com",
		"https://example-brand.com",
		"example-brand.com:443",
		"example-brand.com/path",
		"example_brand.com",
		"example..brand.com",
		"-bad.example.com",
	}
	for _, rpID := range bad {
		if err := ValidateRPID(rpID); !errors.Is(err, ErrRPID) {
			t.Errorf("ValidateRPID(%q) error = %v, want ErrRPID", rpID, err)
		}
	}

	got, err := WellKnownURL(testRPID)
	if err != nil {
		t.Fatalf("WellKnownURL: %v", err)
	}
	want := "https://" + testRPID + "/.well-known/webauthn"
	if got != want {
		t.Fatalf("WellKnownURL = %q, want %q", got, want)
	}
}

func TestVerifyRPIDHash(t *testing.T) {
	verifier, err := NewVerifier(testRPID, testManifest(t))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	t.Run("matching hash", func(t *testing.T) {
		if err := verifier.VerifyRPIDHash(authenticatorData(t, testRPID)); err != nil {
			t.Fatalf("VerifyRPIDHash: %v", err)
		}
	})

	t.Run("foreign RP ID hash", func(t *testing.T) {
		err := verifier.VerifyRPIDHash(authenticatorData(t, "attacker.example"))
		if !errors.Is(err, ErrRPIDHash) {
			t.Fatalf("error = %v, want ErrRPIDHash", err)
		}
	})

	t.Run("truncated authenticator data", func(t *testing.T) {
		err := verifier.VerifyRPIDHash(make([]byte, sha256.Size-1))
		if !errors.Is(err, ErrRPIDHash) {
			t.Fatalf("error = %v, want ErrRPIDHash", err)
		}
	})

	t.Run("expected hash is sha256", func(t *testing.T) {
		if verifier.ExpectedRPIDHash() != RPIDHash(testRPID) {
			t.Fatal("ExpectedRPIDHash does not match RPIDHash")
		}
	})
}

func TestNewVerifierValidationAndAccessors(t *testing.T) {
	manifest := testManifest(t)

	if _, err := NewVerifier("not a domain", manifest); !errors.Is(err, ErrRPID) {
		t.Fatalf("NewVerifier with bad RP ID error = %v, want ErrRPID", err)
	}
	if _, err := NewVerifier(testRPID, nil); !errors.Is(err, ErrManifest) {
		t.Fatalf("NewVerifier with nil manifest error = %v, want ErrManifest", err)
	}

	verifier, err := NewVerifier(testRPID, manifest)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if verifier.RPID() != testRPID {
		t.Fatalf("RPID() = %q, want %q", verifier.RPID(), testRPID)
	}
	if verifier.Manifest() != manifest {
		t.Fatal("Manifest() did not return the configured manifest")
	}
}

func TestCanonicalOriginAdditionalBranches(t *testing.T) {
	bad := []string{
		"https://example-brand.com:abc",
		"ftp://example-brand.com",
		"https://[::1]:bad",
	}
	for _, origin := range bad {
		if _, err := CanonicalOrigin(origin); err == nil {
			t.Errorf("CanonicalOrigin(%q) expected error", origin)
		}
	}

	good := map[string]string{
		"https://[2606:4700::1]:8443": "https://[2606:4700::1]:8443",
		"https://127.0.0.1:443":       "https://127.0.0.1",
		"http://[::1]":                "http://[::1]",
		"http://localhost":            "http://localhost",
	}
	for input, want := range good {
		got, err := CanonicalOrigin(input)
		if err != nil {
			t.Errorf("CanonicalOrigin(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalOrigin(%q) = %q, want %q", input, got, want)
		}
	}

	// Whitespace inside a port is a parse error for url.Parse.
	if _, err := CanonicalOrigin("https://example-brand.com: 443"); err == nil {
		t.Error("expected error for port with whitespace")
	}
	if _, err := CanonicalOrigin("https:///path-only"); err == nil {
		t.Error("expected error for origin without host")
	}
}

func TestVerifyAssertion(t *testing.T) {
	verifier, err := NewVerifier(testRPID, testManifest(t))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	goodAuthData := authenticatorData(t, testRPID)

	cases := []struct {
		name    string
		req     AssertionRequest
		wantErr error
	}{
		{
			name: "legal first party assertion",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://example-brand.com"},
				AuthenticatorData: goodAuthData,
			},
		},
		{
			name: "legal listed subdomain assertion",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://auth.example-brand.com"},
				AuthenticatorData: goodAuthData,
			},
		},
		{
			name: "look-alike domain rejected",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://examp1e-brand.com"},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrOriginNotAllowed,
		},
		{
			name: "hijacked unlisted subdomain rejected",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://login.example-brand.com"},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrOriginNotAllowed,
		},
		{
			name: "different port rejected",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://example-brand.com:8443"},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrOriginNotAllowed,
		},
		{
			name: "legal cross origin nested frame",
			req: AssertionRequest{
				ClientData: ClientData{
					Origin:      "https://auth.example-brand.com",
					CrossOrigin: true,
					TopOrigin:   "https://brand2.example",
				},
				AuthenticatorData: goodAuthData,
			},
		},
		{
			name: "cross origin without top origin rejected",
			req: AssertionRequest{
				ClientData: ClientData{
					Origin:      "https://auth.example-brand.com",
					CrossOrigin: true,
				},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrTopOriginRequired,
		},
		{
			name: "cross origin with evil top page rejected",
			req: AssertionRequest{
				ClientData: ClientData{
					Origin:      "https://auth.example-brand.com",
					CrossOrigin: true,
					TopOrigin:   "https://attacker.example",
				},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrTopOriginRequired,
		},
		{
			name: "same origin nested frame requires listed top origin",
			req: AssertionRequest{
				ClientData: ClientData{
					Origin:      "https://auth.example-brand.com",
					CrossOrigin: false,
					TopOrigin:   "https://example-brand.com",
				},
				AuthenticatorData: goodAuthData,
			},
		},
		{
			name: "explicit top origin on evil page rejected even without crossOrigin flag",
			req: AssertionRequest{
				ClientData: ClientData{
					Origin:    "https://auth.example-brand.com",
					TopOrigin: "https://attacker.example",
				},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrTopOriginRequired,
		},
		{
			name: "valid origins but foreign rpIdHash rejected",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "https://example-brand.com"},
				AuthenticatorData: authenticatorData(t, "attacker.example"),
			},
			wantErr: ErrRPIDHash,
		},
		{
			name: "malformed frame origin rejected",
			req: AssertionRequest{
				ClientData:        ClientData{Origin: "not-an-origin"},
				AuthenticatorData: goodAuthData,
			},
			wantErr: ErrOriginNotAllowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifier.VerifyAssertion(tc.req)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("VerifyAssertion: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("VerifyAssertion error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
