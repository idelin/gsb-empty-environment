package netsec

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func rsaParser(bits int) RSAParser {
	return func(_ []byte) (int, error) {
		return bits, nil
	}
}

var errInjectedParse = errors.New("injected parser failure")

func failingRSAParser(_ []byte) (int, error) {
	return 0, errInjectedParse
}

func b64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func validSignature() SignatureInput {
	return SignatureInput{
		Domain:        "example.com",
		Identity:      "@example.com",
		HashAlgorithm: "sha256",
	}
}

func TestEvaluateKeyRecord(t *testing.T) {
	tests := []struct {
		name        string
		records     []TxtRecord
		sig         SignatureInput
		parser      RSAParser
		wantErr     error
		wantWeak    bool
		wantTesting bool
		wantAlgo    string
	}{
		{
			name: "duplicate tag is invalid",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=rsa; k=ed25519; p=" + b64([]byte("rsa-key-bytes")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrDuplicateTag,
		},
		{
			name: "malformed tag entry is invalid",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; noequals; p=" + b64([]byte("rsa-key-bytes")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrMalformedTag,
		},
		{
			name: "empty p revokes the key",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=",
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrKeyRevoked,
		},
		{
			name: "whitespace-only p revokes the key",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=   ",
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrKeyRevoked,
		},
		{
			name: "unsupported key type",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=ecdsa; p=" + b64([]byte("key")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrInvalidKeyType,
		},
		{
			name: "missing mandatory p tag",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=rsa",
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrMissingPublicKey,
		},
		{
			name: "weak 1024-bit rsa is flagged",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=rsa; p=" + b64([]byte("rsa-1024")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(1024),
			wantWeak: true,
			wantAlgo: "rsa",
		},
		{
			name: "rsa 2047 bits is still weak",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa-2047")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2047),
			wantWeak: true,
			wantAlgo: "rsa",
		},
		{
			name: "rsa 2048 bits is strong",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa-2048")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "rsa below 1024 bits is invalid",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa-512")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(512),
			wantErr: ErrWeakRSA,
		},
		{
			name: "injected rsa parser error propagates",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa-bad")),
			}}},
			sig:     validSignature(),
			parser:  failingRSAParser,
			wantErr: errInjectedParse,
		},
		{
			name: "valid ed25519 key is 32 bytes",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=ed25519; p=" + b64(make([]byte, 32)),
			}}},
			sig:      validSignature(),
			wantAlgo: "ed25519",
		},
		{
			name: "ed25519 key shorter than 32 bytes",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=ed25519; p=" + b64(make([]byte, 31)),
			}}},
			sig:     validSignature(),
			wantErr: ErrInvalidED25519Length,
		},
		{
			name: "ed25519 key longer than 32 bytes",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; k=ed25519; p=" + b64(make([]byte, 33)),
			}}},
			sig:     validSignature(),
			wantErr: ErrInvalidED25519Length,
		},
		{
			name: "service type other than email is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; s=other; p=" + b64([]byte("rsa")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrInvalidServiceType,
		},
		{
			name: "empty service type is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; s=; p=" + b64([]byte("rsa")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrInvalidServiceType,
		},
		{
			name: "s=email is accepted",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; s=email; p=" + b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "s=star is accepted",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; s=*; p=" + b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "h list missing signature hash is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; h=sha1; p=" + b64([]byte("rsa")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrHashNotAllowed,
		},
		{
			name: "h list containing signature hash is accepted",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; h=sha1:sha256; p=" + b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "subdomain identity is allowed without t=s",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa")),
			}}},
			sig: SignatureInput{
				Domain:        "example.com",
				Identity:      "alice@mail.example.com",
				HashAlgorithm: "sha256",
			},
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "subdomain identity is rejected with t=s",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; t=s; p=" + b64([]byte("rsa")),
			}}},
			sig: SignatureInput{
				Domain:        "example.com",
				Identity:      "alice@mail.example.com",
				HashAlgorithm: "sha256",
			},
			parser:  rsaParser(2048),
			wantErr: ErrIdentityDomain,
		},
		{
			name: "unrelated identity domain is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa")),
			}}},
			sig: SignatureInput{
				Domain:        "example.com",
				Identity:      "alice@attacker.example.org",
				HashAlgorithm: "sha256",
			},
			parser:  rsaParser(2048),
			wantErr: ErrIdentityDomain,
		},
		{
			name: "t=s accepts exact domain identity",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; t=s; p=" + b64([]byte("rsa")),
			}}},
			sig: SignatureInput{
				Domain:        "example.com",
				Identity:      "bob@example.com",
				HashAlgorithm: "sha256",
			},
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "t=y only marks testing",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; t=y; p=" + b64([]byte("rsa")),
			}}},
			sig:         validSignature(),
			parser:      rsaParser(2048),
			wantTesting: true,
			wantAlgo:    "rsa",
		},
		{
			name: "unknown tags are ignored",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; x=12345; note=hello; p=" + b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "missing v defaults to DKIM1",
			records: []TxtRecord{{Strings: []string{
				"p=" + b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "unsupported explicit v is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM2; p=" + b64([]byte("rsa")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrInvalidVersion,
		},
		{
			name: "multiple strings inside one TXT are concatenated",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=", b64([]byte("rsa")),
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "two versioned records are invalid",
			records: []TxtRecord{
				{Strings: []string{"v=DKIM1; p=" + b64([]byte("a"))}},
				{Strings: []string{"v=DKIM1; p=" + b64([]byte("b"))}},
			},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrMultipleKeyRecords,
		},
		{
			name:    "no records means no key record",
			records: nil,
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrNoKeyRecord,
		},
		{
			name: "trailing whitespace inside p is stripped",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=" + b64([]byte("rsa-key-bytes")) + " ",
			}}},
			sig:      validSignature(),
			parser:   rsaParser(2048),
			wantAlgo: "rsa",
		},
		{
			name: "invalid base64 in p is rejected",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; p=!!!not-base64!!!",
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrInvalidBase64,
		},
		{
			name: "tag names are case sensitive so uppercase P is missing",
			records: []TxtRecord{{Strings: []string{
				"v=DKIM1; P=" + b64([]byte("rsa")),
			}}},
			sig:     validSignature(),
			parser:  rsaParser(2048),
			wantErr: ErrMissingPublicKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict, err := EvaluateKeyRecord(tc.records, tc.sig, tc.parser)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.WeakRSA != tc.wantWeak {
				t.Errorf("WeakRSA = %v, want %v", verdict.WeakRSA, tc.wantWeak)
			}
			if verdict.Testing != tc.wantTesting {
				t.Errorf("Testing = %v, want %v", verdict.Testing, tc.wantTesting)
			}
			if verdict.Algorithm != tc.wantAlgo {
				t.Errorf("Algorithm = %q, want %q", verdict.Algorithm, tc.wantAlgo)
			}
		})
	}
}

func TestCheckFromBinding(t *testing.T) {
	tests := []struct {
		name            string
		headerLines     []string
		signedHeaders   []string
		requireOversign bool
		wantErr         error
		wantOversigned  bool
		wantSignedFrom  int
	}{
		{
			name: "single From covered by signature",
			headerLines: []string{
				"From: alice@example.com",
				"To: bob@example.com",
				"Subject: hello",
			},
			signedHeaders:  []string{"from", "to", "subject"},
			wantSignedFrom: 1,
		},
		{
			name: "multiple From headers always fail",
			headerLines: []string{
				"From: alice@example.com",
				"From: attacker@evil.example",
			},
			signedHeaders: []string{"from"},
			wantErr:       ErrMultipleFromHeaders,
		},
		{
			name: "From missing from h= fails",
			headerLines: []string{
				"From: alice@example.com",
				"Subject: hello",
			},
			signedHeaders: []string{"subject", "to"},
			wantErr:       ErrFromNotSigned,
		},
		{
			name: "oversign requires one extra From entry",
			headerLines: []string{
				"From: alice@example.com",
			},
			signedHeaders:   []string{"from", "from", "subject"},
			requireOversign: true,
			wantOversigned:  true,
			wantSignedFrom:  2,
		},
		{
			name: "oversign without extra entry fails",
			headerLines: []string{
				"From: alice@example.com",
			},
			signedHeaders:   []string{"from", "subject"},
			requireOversign: true,
			wantErr:         ErrOversignRequired,
		},
		{
			name: "header name comparison is case insensitive",
			headerLines: []string{
				"FROM: alice@example.com",
			},
			signedHeaders:  []string{"From"},
			wantSignedFrom: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict, err := CheckFromBinding(tc.headerLines, tc.signedHeaders, tc.requireOversign)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Oversigned != tc.wantOversigned {
				t.Errorf("Oversigned = %v, want %v", verdict.Oversigned, tc.wantOversigned)
			}
			if verdict.SignedFromCount != tc.wantSignedFrom {
				t.Errorf("SignedFromCount = %d, want %d", verdict.SignedFromCount, tc.wantSignedFrom)
			}
		})
	}
}

func TestNormalizeSelector(t *testing.T) {
	tests := []struct {
		name         string
		selector     string
		domain       string
		wantSelector string
		wantDomain   string
		wantErr      error
	}{
		{
			name:         "lowercases selector and domain",
			selector:     "SEL.ector",
			domain:       "EXAMPLE.COM",
			wantSelector: "sel.ector",
			wantDomain:   "example.com",
		},
		{
			name:         "hyphen and digit labels are valid",
			selector:     "s1-2026",
			domain:       "example.com",
			wantSelector: "s1-2026",
			wantDomain:   "example.com",
		},
		{
			name:     "leading hyphen is not a DNS label",
			selector: "-bad",
			domain:   "example.com",
			wantErr:  ErrInvalidSelector,
		},
		{
			name:     "empty label is invalid",
			selector: "good..bad",
			domain:   "example.com",
			wantErr:  ErrInvalidSelector,
		},
		{
			name:     "underscore is not a DNS label",
			selector: "bad_name",
			domain:   "example.com",
			wantErr:  ErrInvalidSelector,
		},
		{
			name:     "label longer than 63 octets is invalid",
			selector: strings.Repeat("a", 64),
			domain:   "example.com",
			wantErr:  ErrInvalidSelector,
		},
		{
			name:         "63 octet label is valid",
			selector:     strings.Repeat("a", 63),
			domain:       "example.com",
			wantSelector: strings.Repeat("a", 63),
			wantDomain:   "example.com",
		},
		{
			name:     "empty domain is invalid",
			selector: "sel",
			domain:   "  ",
			wantErr:  ErrInvalidDomain,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selector, domain, err := NormalizeSelector(tc.selector, tc.domain)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if selector != tc.wantSelector {
				t.Errorf("selector = %q, want %q", selector, tc.wantSelector)
			}
			if domain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tc.wantDomain)
			}
		})
	}
}
