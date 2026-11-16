package wssecurity_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"example.org/netsec/q0355/wssecurity"
)

const envelopeTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
  xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <soap:Header>
    <wsse:Security soap:mustUnderstand="1">
      <wsu:Timestamp wsu:Id="TS-1">
        <wsu:Created>%[1]s</wsu:Created>
        <wsu:Expires>%[2]s</wsu:Expires>
      </wsu:Timestamp>
      <wsse:UsernameToken wsu:Id="UT-1">
        <wsse:Username>gateway-client</wsse:Username>
        <wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText">s3cret</wsse:Password>
        <wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%[3]s</wsse:Nonce>
        <wsu:Created>%[1]s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </soap:Header>
  <soap:Body wsu:Id="BODY-1">
    <op:Echo xmlns:op="http://example.org/soap/op">hello</op:Echo>
  </soap:Body>
</soap:Envelope>`

var fixedNow = time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)

func rfc3339(t time.Time) string { return t.Format(time.RFC3339) }

func formatEnvelope(created, expires, nonce string) string {
	return fmt.Sprintf(envelopeTemplate, created, expires, nonce)
}

func validEnvelope(nonce string) string {
	created := fixedNow.Add(-5 * time.Second)
	expires := created.Add(120 * time.Second)
	return formatEnvelope(rfc3339(created), rfc3339(expires), nonce)
}

const noHeaderTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <soap:Header></soap:Header>
  <soap:Body wsu:Id="BODY-1"><op:Echo xmlns:op="http://example.org/soap/op">hello</op:Echo></soap:Body>
</soap:Envelope>`

// alternatePrefixEnvelope uses arbitrary prefixes (sec/utility/e) while keeping
// the same namespace URIs, proving prefixes cannot bypass validation.
const alternatePrefixTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:sec="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
  xmlns:utility="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <e:Header>
    <sec:Security e:mustUnderstand="1">
      <utility:Timestamp utility:Id="TS-1">
        <utility:Created>%[1]s</utility:Created>
        <utility:Expires>%[2]s</utility:Expires>
      </utility:Timestamp>
      <sec:UsernameToken utility:Id="UT-1">
        <sec:Username>gateway-client</sec:Username>
        <sec:Nonce>%[3]s</sec:Nonce>
        <utility:Created>%[1]s</utility:Created>
      </sec:UsernameToken>
    </sec:Security>
  </e:Header>
  <e:Body utility:Id="BODY-1"><op:Echo xmlns:op="http://example.org/soap/op">hello</op:Echo></e:Body>
</e:Envelope>`

func alternatePrefixEnvelope(nonce string) string {
	created := fixedNow.Add(-5 * time.Second)
	return fmt.Sprintf(alternatePrefixTemplate, rfc3339(created), rfc3339(created.Add(120*time.Second)), nonce)
}

const soap12Template = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
  xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
  xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <env:Header>
    <wsse:Security env:mustUnderstand="true">
      <wsu:Timestamp wsu:Id="TS-1">
        <wsu:Created>%[1]s</wsu:Created>
        <wsu:Expires>%[2]s</wsu:Expires>
      </wsu:Timestamp>
      <wsse:UsernameToken wsu:Id="UT-1">
        <wsse:Nonce>%[3]s</wsse:Nonce>
        <wsu:Created>%[1]s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </env:Header>
  <env:Body wsu:Id="BODY-1"><op:Echo xmlns:op="http://example.org/soap/op">hello</op:Echo></env:Body>
</env:Envelope>`

func soap12Envelope(nonce string) string {
	created := fixedNow.Add(-5 * time.Second)
	return fmt.Sprintf(soap12Template, rfc3339(created), rfc3339(created.Add(120*time.Second)), nonce)
}

type fakeVerifier struct {
	refs   []string
	ok     bool
	err    error
	called int
}

func (f *fakeVerifier) verify(_ context.Context, _ io.Reader) ([]string, bool, error) {
	f.called++
	return f.refs, f.ok, f.err
}

func newValidator(t *testing.T, v *fakeVerifier, cache wssecurity.NonceCache) *wssecurity.Validator {
	t.Helper()
	if cache == nil {
		cache = wssecurity.NewMemoryNonceCache(wssecurity.DefaultNonceRetention)
	}
	validator, err := wssecurity.New(wssecurity.Config{
		VerifySignature: v.verify,
		NonceCache:      cache,
		Now:             func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return validator
}

func fullSignedVerifier() *fakeVerifier {
	return &fakeVerifier{refs: []string{"#TS-1", "#BODY-1"}, ok: true}
}

func TestValidateTable(t *testing.T) {
	created := fixedNow.Add(-5 * time.Second)

	type testCase struct {
		name     string
		message  func() string
		verifier func() *fakeVerifier
		wantErr  error
	}

	tests := []testCase{
		{
			name:     "valid message accepted",
			message:  func() string { return validEnvelope("nonce-ok") },
			verifier: fullSignedVerifier,
		},
		{
			name:     "replayed nonce rejected on second use",
			message:  func() string { return validEnvelope("nonce-table-replay-xyz") },
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrReplayedNonce,
		},
		{
			name: "two timestamps rejected",
			message: func() string {
				extra := `<wsu:Timestamp wsu:Id="TS-2"><wsu:Created>` + rfc3339(created) +
					`</wsu:Created><wsu:Expires>` + rfc3339(created.Add(60*time.Second)) +
					`</wsu:Expires></wsu:Timestamp>`
				return strings.Replace(validEnvelope("nonce-double-ts"),
					"</wsse:Security>", extra+"</wsse:Security>", 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMultipleTimestamps,
		},
		{
			name:     "unsigned timestamp rejected",
			message:  func() string { return validEnvelope("nonce-unsigned-ts") },
			verifier: func() *fakeVerifier { return &fakeVerifier{refs: []string{"#BODY-1"}, ok: true} },
			wantErr:  wssecurity.ErrUnsignedTimestamp,
		},
		{
			name:     "unsigned body rejected",
			message:  func() string { return validEnvelope("nonce-unsigned-body") },
			verifier: func() *fakeVerifier { return &fakeVerifier{refs: []string{"#TS-1"}, ok: true} },
			wantErr:  wssecurity.ErrUnsignedBody,
		},
		{
			name: "duplicate wsu id rejected",
			message: func() string {
				return strings.Replace(validEnvelope("nonce-dup-id"),
					`wsu:Id="BODY-1"`, `wsu:Id="TS-1"`, 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrDuplicateID,
		},
		{
			name: "created more than 60s in future rejected",
			message: func() string {
				future := fixedNow.Add(61 * time.Second)
				return formatEnvelope(rfc3339(future), rfc3339(future.Add(60*time.Second)), "nonce-future")
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrCreatedInFuture,
		},
		{
			name: "created within 60s skew accepted",
			message: func() string {
				future := fixedNow.Add(30 * time.Second)
				return formatEnvelope(rfc3339(future), rfc3339(future.Add(60*time.Second)), "nonce-skew")
			},
			verifier: fullSignedVerifier,
		},
		{
			name: "expires not after created rejected",
			message: func() string {
				return formatEnvelope(rfc3339(created), rfc3339(created.Add(-time.Second)), "nonce-order")
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrExpiresOrder,
		},
		{
			name: "lifetime over 300s rejected",
			message: func() string {
				return formatEnvelope(rfc3339(created), rfc3339(created.Add(301*time.Second)), "nonce-ttl")
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrTimestampLifetime,
		},
		{
			name: "expired timestamp rejected",
			message: func() string {
				pastCreated := fixedNow.Add(-120 * time.Second)
				return formatEnvelope(rfc3339(pastCreated), rfc3339(pastCreated.Add(60*time.Second)), "nonce-expired")
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrTimestampExpired,
		},
		{
			name:     "signature verifier false rejected",
			message:  func() string { return validEnvelope("nonce-bad-sig") },
			verifier: func() *fakeVerifier { return &fakeVerifier{ok: false} },
			wantErr:  wssecurity.ErrSignatureVerify,
		},
		{
			name:    "signature verifier error rejected",
			message: func() string { return validEnvelope("nonce-sig-err") },
			verifier: func() *fakeVerifier {
				return &fakeVerifier{ok: true, err: errors.New("digest mismatch")}
			},
			wantErr: wssecurity.ErrSignatureVerify,
		},
		{
			name:     "missing security header rejected",
			message:  func() string { return noHeaderTemplate },
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrNoSecurityHeader,
		},
		{
			name: "missing body wsu id rejected",
			message: func() string {
				return strings.Replace(validEnvelope("nonce-no-body-id"),
					` wsu:Id="BODY-1"`, "", 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMissingID,
		},
		{
			name: "missing timestamp wsu id rejected",
			message: func() string {
				return strings.Replace(validEnvelope("nonce-no-ts-id"),
					` wsu:Id="TS-1"`, "", 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMissingID,
		},
		{
			name: "missing nonce rejected",
			message: func() string {
				return strings.Replace(validEnvelope("nonce-missing"),
					`<wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">nonce-missing</wsse:Nonce>`,
					"", 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMissingNonce,
		},
		{
			name: "missing username token created rejected",
			message: func() string {
				return strings.Replace(validEnvelope("nonce-no-ut-created"),
					"<wsu:Created>"+rfc3339(created)+"</wsu:Created>\n      </wsse:UsernameToken>",
					"</wsse:UsernameToken>", 1)
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMissingUTCreated,
		},
		{
			name: "malformed xml rejected",
			message: func() string {
				return `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Header>`
			},
			verifier: fullSignedVerifier,
			wantErr:  wssecurity.ErrMalformed,
		},
		{
			name:     "arbitrary namespace prefixes accepted",
			message:  func() string { return alternatePrefixEnvelope("nonce-prefix") },
			verifier: fullSignedVerifier,
		},
		{
			name:     "soap 1.2 envelope accepted",
			message:  func() string { return soap12Envelope("nonce-soap12") },
			verifier: func() *fakeVerifier { return &fakeVerifier{refs: []string{"TS-1", "BODY-1"}, ok: true} },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := tc.verifier()
			var sharedCache wssecurity.NonceCache
			if tc.name == "replayed nonce rejected on second use" {
				sharedCache = wssecurity.NewMemoryNonceCache(wssecurity.DefaultNonceRetention)
			}
			validator := newValidator(t, verifier, sharedCache)

			if tc.name == "replayed nonce rejected on second use" {
				if err := validator.Validate(context.Background(), strings.NewReader(tc.message())); err != nil {
					t.Fatalf("first use should pass, got %v", err)
				}
			}

			err := validator.Validate(context.Background(), strings.NewReader(tc.message()))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error wrapping %v, got %v", tc.wantErr, err)
			}
		})
	}
}
