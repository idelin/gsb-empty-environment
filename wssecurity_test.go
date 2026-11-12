package netsec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testWSU  = nsWSU
	testWSSE = nsWSSE
)

var testNow = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

// fakeSigner stands in for the caller-injected XML-DSIG verifier.
type fakeSigner struct {
	refs []string
	err  error
}

func (f fakeSigner) verify(_ context.Context, _ []byte) (*SignatureResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &SignatureResult{SignedReferences: f.refs}, nil
}

type envelope struct {
	soapEnv      string
	soapPrefix   string
	wsuPrefix    string
	wssePrefix   string
	decl         string
	security     string
	bodyAttrs    string
	bodyAttrsSet bool
	body         string
}

func (e envelope) build() string {
	if e.soapEnv == "" {
		e.soapEnv = nsSOAP11
	}
	if e.soapPrefix == "" {
		e.soapPrefix = "soap"
	}
	if e.wsuPrefix == "" {
		e.wsuPrefix = "wsu"
	}
	if e.wssePrefix == "" {
		e.wssePrefix = "wsse"
	}
	if e.decl == "" {
		e.decl = `<?xml version="1.0" encoding="UTF-8"?>`
	}
	// bodyAttrsSet distinguishes "explicitly no attribute" from the unset
	// default, since an empty string is the test case itself.
	if !e.bodyAttrsSet {
		e.bodyAttrs = e.wsuPrefix + `:Id="Body-1"`
	}
	return e.decl +
		`<` + e.soapPrefix + `:Envelope xmlns:` + e.soapPrefix + `="` + e.soapEnv + `" ` +
		`xmlns:` + e.wsuPrefix + `="` + testWSU + `" ` +
		`xmlns:` + e.wssePrefix + `="` + testWSSE + `">` +
		`<` + e.soapPrefix + `:Header>` + e.security + `</` + e.soapPrefix + `:Header>` +
		`<` + e.soapPrefix + `:Body ` + e.bodyAttrs + `>` + e.body + `</` + e.soapPrefix + `:Body>` +
		`</` + e.soapPrefix + `:Envelope>`
}

func timestampInner(id string, created, expires time.Time) string {
	return `<wsu:Timestamp wsu:Id="` + id + `">` +
		`<wsu:Created>` + created.UTC().Format(time.RFC3339) + `</wsu:Created>` +
		`<wsu:Expires>` + expires.UTC().Format(time.RFC3339) + `</wsu:Expires>` +
		`</wsu:Timestamp>`
}

func timestampBlock(id string, created, expires time.Time) string {
	return `<wsse:Security soap:mustUnderstand="1">` +
		timestampInner(id, created, expires) + `</wsse:Security>`
}

func timestampWithToken(id string, created, expires time.Time, nonce, utCreated string) string {
	return `<wsse:Security soap:mustUnderstand="1">` +
		timestampInner(id, created, expires) +
		`<wsse:UsernameToken wsu:Id="UT-1">` +
		`<wsse:Username>gw-user</wsse:Username>` +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">x</wsse:Password>` +
		`<wsse:Nonce>` + nonce + `</wsse:Nonce>` +
		`<wsu:Created>` + utCreated + `</wsu:Created>` +
		`</wsse:UsernameToken></wsse:Security>`
}

func validEnvelope(security string) string {
	return envelope{security: security, body: `<trade id="7"/>`}.build()
}

func newTestValidator(store NonceStore, refs []string) *Validator {
	v := New(fakeSigner{refs: refs}.verify, store)
	v.SetClock(func() time.Time { return testNow })
	return v
}

func validateMsg(t *testing.T, v *Validator, xmlText string) error {
	t.Helper()
	return v.Validate(context.Background(), strings.NewReader(xmlText))
}

func assertReject(t *testing.T, err error, sentinel error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection wrapping %v, got acceptance", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error wrapping %v, got %v", sentinel, err)
	}
}

func TestValidateAcceptsWellFormedMessage(t *testing.T) {
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return testNow }
	v := newTestValidator(store, []string{"#TS-1", "#Body-1"})

	security := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"nonce-alpha", testNow.UTC().Format(time.RFC3339))
	if err := validateMsg(t, v, validEnvelope(security)); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("expected one retained nonce, got %d", store.Len())
	}
}

func TestValidateRejectsReplayedNonce(t *testing.T) {
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return testNow }
	v := newTestValidator(store, []string{"#TS-1", "#Body-1"})

	security := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"replayed-nonce", testNow.UTC().Format(time.RFC3339))
	msg := validEnvelope(security)

	if err := validateMsg(t, v, msg); err != nil {
		t.Fatalf("first message should pass, got %v", err)
	}
	assertReject(t, validateMsg(t, v, msg), ErrReplay)
}

func TestSameNonceDifferentCreatedIsDistinctKey(t *testing.T) {
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return testNow }
	v := newTestValidator(store, []string{"#TS-1", "#Body-1"})

	first := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"shared-nonce", testNow.UTC().Format(time.RFC3339))
	second := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"shared-nonce", testNow.Add(time.Second).UTC().Format(time.RFC3339))

	if err := validateMsg(t, v, validEnvelope(first)); err != nil {
		t.Fatalf("first message should pass, got %v", err)
	}
	if err := validateMsg(t, v, validEnvelope(second)); err != nil {
		t.Fatalf("distinct (Nonce, Created) pair should pass, got %v", err)
	}
}

func TestValidateRejectsDuplicateTimestamp(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#TS-2", "#Body-1"})

	security := `<wsse:Security soap:mustUnderstand="1">` +
		timestampInner("TS-1", testNow, testNow.Add(time.Minute)) +
		timestampInner("TS-2", testNow, testNow.Add(2*time.Minute)) +
		`</wsse:Security>`

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrDuplicateTimestamp)
}

func TestValidateRejectsUnsignedTimestamp(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#Body-1"})
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrUnsignedTimestamp)
}

func TestValidateRejectsUnsignedBody(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1"})
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrUnsignedBody)
}

func TestValidateRejectsDuplicateWsuIdInBody(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))
	body := `<trade wsu:Id="TS-1" xmlns:wsu="` + testWSU + `">99</trade>`
	msg := envelope{security: security, body: body}.build()

	assertReject(t, validateMsg(t, v, msg), ErrDuplicateID)
}

func TestValidateRejectsIdReusedOnBody(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1"})
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))
	msg := envelope{
		security:     security,
		bodyAttrs:    `wsu:Id="TS-1"`,
		bodyAttrsSet: true,
		body:         `<trade id="3"/>`,
	}.build()

	assertReject(t, validateMsg(t, v, msg), ErrDuplicateID)
}

func TestValidateEnforcesCreatedSkewWindow(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})

	atLimit := timestampBlock("TS-1", testNow.Add(60*time.Second), testNow.Add(4*time.Minute))
	if err := validateMsg(t, v, validEnvelope(atLimit)); err != nil {
		t.Fatalf("Created exactly at the 60s boundary should pass, got %v", err)
	}

	overLimit := timestampBlock("TS-1", testNow.Add(61*time.Second), testNow.Add(4*time.Minute))
	assertReject(t, validateMsg(t, v, validEnvelope(overLimit)), ErrCreatedInFuture)
}

func TestValidateRejectsExpiredTimestamp(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})
	security := timestampBlock("TS-1", testNow.Add(-2*time.Minute), testNow.Add(-time.Second))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrExpired)
}

func TestValidateEnforcesTTLBoundaries(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})

	maxTTL := timestampBlock("TS-1", testNow, testNow.Add(300*time.Second))
	if err := validateMsg(t, v, validEnvelope(maxTTL)); err != nil {
		t.Fatalf("TTL exactly at the 300s boundary should pass, got %v", err)
	}

	tooLong := timestampBlock("TS-1", testNow, testNow.Add(301*time.Second))
	assertReject(t, validateMsg(t, v, validEnvelope(tooLong)), ErrInvalidTTL)

	zeroTTL := timestampBlock("TS-1", testNow, testNow)
	assertReject(t, validateMsg(t, v, validEnvelope(zeroTTL)), ErrInvalidTTL)
}

func TestValidatePropagatesSignatureFailure(t *testing.T) {
	v := New(
		fakeSigner{err: errors.New("digest mismatch")}.verify,
		NewMemoryNonceStore(),
	)
	v.SetClock(func() time.Time { return testNow })
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrSignatureVerify)
}

func TestValidateRejectsMissingTimestamp(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#Body-1"})
	msg := envelope{security: `<!-- no security assertions -->`, body: `<trade/>`}.build()

	assertReject(t, validateMsg(t, v, msg), ErrMissingTimestamp)
}

func TestTimestampOutsideSecurityHeaderIsNotAccepted(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})

	// A Timestamp smuggled into the Body instead of wsse:Security must not be
	// mistaken for the security header timestamp.
	body := `<wsu:Timestamp wsu:Id="TS-1" xmlns:wsu="` + testWSU + `">` +
		`<wsu:Created>` + testNow.UTC().Format(time.RFC3339) + `</wsu:Created>` +
		`<wsu:Expires>` + testNow.Add(time.Minute).UTC().Format(time.RFC3339) + `</wsu:Expires>` +
		`</wsu:Timestamp>`
	msg := envelope{security: ``, body: body}.build()

	assertReject(t, validateMsg(t, v, msg), ErrMissingTimestamp)
}

func TestValidateRejectsBodyWithoutId(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1"})
	msg := envelope{
		security:     timestampBlock("TS-1", testNow, testNow.Add(time.Minute)),
		bodyAttrs:    ``,
		bodyAttrsSet: true,
		body:         `<trade/>`,
	}.build()

	assertReject(t, validateMsg(t, v, msg), ErrMissingBodyID)
}

func TestValidateIsNamespacePrefixAgnostic(t *testing.T) {
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return testNow }
	v := newTestValidator(store, []string{"#TS-9", "#Body-9"})

	security := `<secext:Security xmlns:secext="` + testWSSE + `" xmlns:util="` + testWSU + `">` +
		`<util:Timestamp util:Id="TS-9">` +
		`<util:Created>` + testNow.UTC().Format(time.RFC3339) + `</util:Created>` +
		`<util:Expires>` + testNow.Add(time.Minute).UTC().Format(time.RFC3339) + `</util:Expires>` +
		`</util:Timestamp></secext:Security>`

	msg := envelope{
		soapEnv:      nsSOAP12,
		soapPrefix:   "env",
		wssePrefix:   "secext",
		wsuPrefix:    "util",
		security:     security,
		bodyAttrs:    `util:Id="Body-9"`,
		bodyAttrsSet: true,
		body:         `<order/>`,
	}.build()

	if err := validateMsg(t, v, msg); err != nil {
		t.Fatalf("SOAP 1.2 with custom prefixes should pass, got %v", err)
	}
}

func TestValidateRejectsMalformedXML(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})
	assertReject(t, validateMsg(t, v, `<soap:Envelope><soap:Body>`), ErrInvalidMessage)
}

func TestInvalidMessageDoesNotConsumeNonce(t *testing.T) {
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return testNow }
	v := newTestValidator(store, []string{"#TS-1", "#Body-1"})

	security := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"once-only", testNow.UTC().Format(time.RFC3339))
	msg := validEnvelope(security)

	// Expired messages are rejected before the NonceStore is touched. The
	// pair keeps the same (Nonce, Created) values as the later valid message.
	expiredSecurity := timestampWithToken("TS-1", testNow.Add(-3*time.Minute),
		testNow.Add(-time.Second), "once-only", testNow.UTC().Format(time.RFC3339))
	expired := validEnvelope(expiredSecurity)
	assertReject(t, validateMsg(t, v, expired), ErrExpired)
	if store.Len() != 0 {
		t.Fatalf("rejected message must not populate the nonce store, got %d entries", store.Len())
	}

	if err := validateMsg(t, v, msg); err != nil {
		t.Fatalf("fresh message with the same nonce should still pass, got %v", err)
	}
}

func TestMemoryNonceStoreExpiresEntries(t *testing.T) {
	clock := testNow
	store := NewMemoryNonceStore()
	store.Now = func() time.Time { return clock }

	if err := store.CheckAndStore("k", time.Minute); err != nil {
		t.Fatalf("first use should pass, got %v", err)
	}
	if err := store.CheckAndStore("k", time.Minute); !errors.Is(err, ErrReplay) {
		t.Fatalf("immediate reuse should be ErrReplay, got %v", err)
	}

	clock = clock.Add(2 * time.Minute)
	if err := store.CheckAndStore("k", time.Minute); err != nil {
		t.Fatalf("reuse after TTL should pass, got %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("expired entry should have been swept, len=%d", store.Len())
	}
}

func TestMemoryNonceStoreAllowsExactlyOneConcurrentWinner(t *testing.T) {
	store := NewMemoryNonceStore()
	const goroutines = 64

	var wg sync.WaitGroup
	results := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			results <- store.CheckAndStore("hot-key", time.Minute)
		}()
	}
	wg.Wait()
	close(results)

	winners, replays := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrReplay):
			replays++
		default:
			t.Fatalf("unexpected store error: %v", err)
		}
	}
	if winners != 1 || replays != goroutines-1 {
		t.Fatalf("expected 1 winner and %d replays, got %d winners and %d replays",
			goroutines-1, winners, replays)
	}
}

func TestMalformedUsernameTokenIsRejected(t *testing.T) {
	v := newTestValidator(NewMemoryNonceStore(), []string{"#TS-1", "#Body-1"})
	security := `<wsse:Security>` +
		timestampInner("TS-1", testNow, testNow.Add(time.Minute)) +
		`<wsse:UsernameToken><wsse:Username>gw</wsse:Username></wsse:UsernameToken>` +
		`</wsse:Security>`

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrMalformedUsernameToken)
}

func TestUsernameTokenWithoutStoreIsConfigurationError(t *testing.T) {
	v := newTestValidator(nil, []string{"#TS-1", "#Body-1"})
	security := timestampWithToken("TS-1", testNow, testNow.Add(2*time.Minute),
		"nonce-x", testNow.UTC().Format(time.RFC3339))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrConfiguration)
}

func TestMissingVerifierIsConfigurationError(t *testing.T) {
	v := New(nil, NewMemoryNonceStore())
	v.SetClock(func() time.Time { return testNow })
	security := timestampBlock("TS-1", testNow, testNow.Add(time.Minute))

	assertReject(t, validateMsg(t, v, validEnvelope(security)), ErrConfiguration)
}
