package clientassertion

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

const (
	testIssuer   = "https://as.example.com"
	testTokenEP  = "https://as.example.com/oauth2/token"
	testClientID = "client-123"
	testRS256    = "RS256"
)

var testBase = time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)

type buildOption struct {
	alg       string
	iss       string
	sub       string
	aud       any
	iat       *time.Time
	exp       *time.Time
	jti       string
	omitExp   bool
	omitIat   bool
	omitJTI   bool
	signBadly bool
	key       *rsa.PrivateKey
}

func buildAssertion(t *testing.T, opt buildOption) string {
	t.Helper()
	header := map[string]any{"alg": opt.alg, "typ": "JWT"}
	claims := map[string]any{
		"iss": opt.iss,
		"sub": opt.sub,
		"jti": opt.jti,
	}
	if opt.aud != nil {
		claims["aud"] = opt.aud
	}
	if !opt.omitIat {
		iat := testBase
		if opt.iat != nil {
			iat = *opt.iat
		}
		claims["iat"] = iat.Unix()
	}
	if !opt.omitExp {
		exp := testBase.Add(120 * time.Second)
		if opt.exp != nil {
			exp = *opt.exp
		}
		claims["exp"] = exp.Unix()
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(claimsJSON)

	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, opt.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if opt.signBadly {
		sig[0] ^= 0xFF
	}
	return signingInput + "." + enc.EncodeToString(sig)
}

func newTestValidator(t *testing.T, key *rsa.PrivateKey, now time.Time) (*Validator, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	v, err := New(Config{
		Issuer:      testIssuer,
		AllowedAlgs: []string{testRS256, "ES256"},
		Verifier: func(_ context.Context, _ string, h Header, input, signature []byte) error {
			if h.Alg != testRS256 {
				return fmt.Errorf("unexpected alg %q", h.Alg)
			}
			sum := sha256.Sum256(input)
			return rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], signature)
		},
		Store: store,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v, store
}

func validOption(key *rsa.PrivateKey) buildOption {
	return buildOption{
		alg: testRS256,
		iss: testClientID,
		sub: testClientID,
		aud: testIssuer,
		jti: "jti-1",
		key: key,
	}
}

func TestAuthenticate_Table(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	type testCase struct {
		name    string
		mutate  func(*buildOption)
		req     func(assertion string) Request
		wantErr error
	}

	cases := []testCase{
		{name: "happy path", mutate: func(*buildOption) {}},
		{
			name: "dual credentials rejected",
			req: func(assertion string) Request {
				return Request{
					ClientID:      testClientID,
					ClientSecret:  "top-secret",
					Assertion:     assertion,
					AssertionType: AssertionTypeJWTBearer,
				}
			},
			wantErr: ErrMultipleCredentials,
		},
		{
			name:    "missing assertion",
			req:     func(string) Request { return Request{ClientID: testClientID} },
			wantErr: ErrMissingAssertion,
		},
		{
			name: "wrong assertion type",
			req: func(assertion string) Request {
				return Request{
					ClientID:      testClientID,
					Assertion:     assertion,
					AssertionType: "urn:ietf:params:oauth:client-assertion-type:saml2-bearer",
				}
			},
			wantErr: ErrUnsupportedAssertionType,
		},
		{
			name:    "missing client id",
			req:     func(a string) Request { return Request{Assertion: a, AssertionType: AssertionTypeJWTBearer} },
			wantErr: ErrMissingClientID,
		},
		{
			name: "malformed compact token",
			req: func(string) Request {
				return Request{ClientID: testClientID, Assertion: "aaa.bbb", AssertionType: AssertionTypeJWTBearer}
			},
			wantErr: ErrMalformedAssertion,
		},
		{name: "alg none rejected", mutate: func(o *buildOption) { o.alg = "none" }, wantErr: ErrDisallowedAlgorithm},
		{name: "alg HS256 rejected even if requested", mutate: func(o *buildOption) { o.alg = "HS256" }, wantErr: ErrDisallowedAlgorithm},
		{name: "alg outside whitelist rejected", mutate: func(o *buildOption) { o.alg = "PS512" }, wantErr: ErrDisallowedAlgorithm},
		{name: "iss must equal client id", mutate: func(o *buildOption) { o.iss = "other-client" }, wantErr: ErrInvalidIssuer},
		{name: "sub must equal client id", mutate: func(o *buildOption) { o.sub = "other-client" }, wantErr: ErrInvalidSubject},
		{
			name:    "aud array containing correct issuer rejected",
			mutate:  func(o *buildOption) { o.aud = []string{testIssuer} },
			wantErr: ErrInvalidAudience,
		},
		{
			name:    "aud array with multiple endpoints rejected",
			mutate:  func(o *buildOption) { o.aud = []string{testIssuer, testTokenEP} },
			wantErr: ErrInvalidAudience,
		},
		{
			name:    "aud equal to token endpoint rejected",
			mutate:  func(o *buildOption) { o.aud = testTokenEP },
			wantErr: ErrInvalidAudience,
		},
		{name: "aud null rejected", mutate: func(o *buildOption) { o.aud = nil }, wantErr: ErrInvalidAudience},
		{name: "exp missing", mutate: func(o *buildOption) { o.omitExp = true }, wantErr: ErrMissingExpiration},
		{name: "iat missing", mutate: func(o *buildOption) { o.omitIat = true }, wantErr: ErrMissingIssuedAt},
		{
			name: "expired assertion",
			mutate: func(o *buildOption) {
				exp := testBase.Add(-1 * time.Second)
				iat := testBase.Add(-200 * time.Second)
				o.exp, o.iat = &exp, &iat
			},
			wantErr: ErrTokenExpired,
		},
		{
			name: "lifetime over 300 seconds",
			mutate: func(o *buildOption) {
				exp := testBase.Add(301 * time.Second)
				o.exp = &exp
			},
			wantErr: ErrLifetimeTooLong,
		},
		{
			name: "lifetime exactly 300 seconds allowed",
			mutate: func(o *buildOption) {
				exp := testBase.Add(300 * time.Second)
				o.exp = &exp
			},
		},
		{
			name: "iat more than 30 seconds in future",
			mutate: func(o *buildOption) {
				iat := testBase.Add(31 * time.Second)
				exp := iat.Add(60 * time.Second)
				o.iat, o.exp = &iat, &exp
			},
			wantErr: ErrIssuedAtInFuture,
		},
		{
			name: "iat exactly 30 seconds in future allowed",
			mutate: func(o *buildOption) {
				iat := testBase.Add(30 * time.Second)
				exp := iat.Add(60 * time.Second)
				o.iat, o.exp = &iat, &exp
			},
		},
		{name: "jti missing", mutate: func(o *buildOption) { o.omitJTI = true; o.jti = "" }, wantErr: ErrMissingJTI},
		{name: "bad signature rejected", mutate: func(o *buildOption) { o.signBadly = true }, wantErr: ErrSignatureRejected},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := validOption(key)
			if tc.mutate != nil {
				tc.mutate(&opt)
			}
			assertion := buildAssertion(t, opt)
			req := Request{
				ClientID:      testClientID,
				Assertion:     assertion,
				AssertionType: AssertionTypeJWTBearer,
			}
			if tc.req != nil {
				req = tc.req(assertion)
			}
			validator, _ := newTestValidator(t, key, testBase)
			got, err := validator.Authenticate(context.Background(), req)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				if got != testClientID {
					t.Fatalf("authenticated client = %q, want %q", got, testClientID)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected errors.Is(%v, %v), full err = %v", err, tc.wantErr, err)
			}
		})
	}
}

func TestAuthenticate_JTIReplayPartitionedByClient(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	validator, _ := newTestValidator(t, key, testBase)

	assertion := buildAssertion(t, validOption(key))
	req := Request{
		ClientID:      testClientID,
		Assertion:     assertion,
		AssertionType: AssertionTypeJWTBearer,
	}
	if _, err := validator.Authenticate(context.Background(), req); err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}
	if _, err := validator.Authenticate(context.Background(), req); !errors.Is(err, ErrReplayedJTI) {
		t.Fatalf("replay should fail with ErrReplayedJTI, got %v", err)
	}

	other := validOption(key)
	other.iss, other.sub = "client-456", "client-456"
	otherReq := Request{
		ClientID:      "client-456",
		Assertion:     buildAssertion(t, other),
		AssertionType: AssertionTypeJWTBearer,
	}
	if _, err := validator.Authenticate(context.Background(), otherReq); err != nil {
		t.Fatalf("jti in another client partition should succeed: %v", err)
	}
}

func TestMemoryStore_ExpiredJTICanReuse(t *testing.T) {
	store := NewMemoryStore()
	now := testBase
	store.now = func() time.Time { return now }

	exp := testBase.Add(60 * time.Second)
	if err := store.CheckAndStore(context.Background(), testClientID, "jti-x", exp); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := store.CheckAndStore(context.Background(), testClientID, "jti-x", exp); !errors.Is(err, ErrReplayedJTI) {
		t.Fatalf("duplicate should replay: %v", err)
	}

	now = exp.Add(time.Second)
	newExp := now.Add(60 * time.Second)
	if err := store.CheckAndStore(context.Background(), testClientID, "jti-x", newExp); err != nil {
		t.Fatalf("reuse after expiry should succeed: %v", err)
	}
	if got := len(store.entries[testClientID]); got != 1 {
		t.Fatalf("stale entries not swept, got %d records", got)
	}
}

func TestNew_RejectsBadConfig(t *testing.T) {
	verifier := func(context.Context, string, Header, []byte, []byte) error { return nil }
	if _, err := New(Config{Issuer: "  ", AllowedAlgs: []string{testRS256}, Verifier: verifier}); err == nil {
		t.Fatal("empty issuer should be rejected")
	}
	if _, err := New(Config{Issuer: testIssuer, AllowedAlgs: []string{testRS256}}); err == nil {
		t.Fatal("nil verifier should be rejected")
	}
	if _, err := New(Config{Issuer: testIssuer, AllowedAlgs: []string{"none", "HS256"}, Verifier: verifier}); err == nil {
		t.Fatal("none/HS-only whitelist should be rejected")
	}
	if _, err := New(Config{Issuer: testIssuer, AllowedAlgs: []string{"none", testRS256}, Verifier: verifier}); err != nil {
		t.Fatalf("banned algs should be silently dropped, got %v", err)
	}
}

func TestRequestFromForm(t *testing.T) {
	values := map[string][]string{
		"client_id":             {testClientID},
		"client_assertion":      {"a.b.c"},
		"client_assertion_type": {AssertionTypeJWTBearer},
	}
	req := RequestFromForm(values)
	if req.ClientID != testClientID || req.Assertion != "a.b.c" || req.AssertionType != AssertionTypeJWTBearer {
		t.Fatalf("unexpected request: %+v", req)
	}
	if req.ClientSecret != "" {
		t.Fatalf("client_secret should be empty, got %q", req.ClientSecret)
	}
}
