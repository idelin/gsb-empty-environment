package netsec_test

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0776"
)

const (
	testACS = "https://sp.example.org/saml/acs"
	testIdP = "https://idp.example.org"
)

var testNow = time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

type responseOptions struct {
	responseID               string
	inResponseTo             string
	assertionID              string
	issuer                   string
	recipient                string
	conditionsNotBefore      string
	conditionsNotOnOrAfter   string
	confirmationNotBefore    string
	confirmationNotOnOrAfter string
	confirmationInResponseTo string
}

func newCorrelator(t *testing.T, allowIdPInitiated bool) *netsec.Correlator {
	t.Helper()
	correlator, err := netsec.New(netsec.Config{
		ACSURL:            testACS,
		AllowIdPInitiated: allowIdPInitiated,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return correlator
}

func register(t *testing.T, correlator *netsec.Correlator, id string, now time.Time, ttl time.Duration) {
	t.Helper()
	if err := correlator.RegisterRequest(id, now.Add(ttl)); err != nil {
		t.Fatalf("RegisterRequest(%q): %v", id, err)
	}
}

func validOptions(inResponseTo, assertionID string) responseOptions {
	return responseOptions{
		responseID:               "_response",
		inResponseTo:             inResponseTo,
		assertionID:              assertionID,
		issuer:                   testIdP,
		recipient:                testACS,
		conditionsNotBefore:      testNow.Add(-5 * time.Minute).Format(time.RFC3339),
		conditionsNotOnOrAfter:   testNow.Add(30 * time.Minute).Format(time.RFC3339),
		confirmationInResponseTo: inResponseTo,
		confirmationNotOnOrAfter: testNow.Add(10 * time.Minute).Format(time.RFC3339),
	}
}

func TestSolicitedResponseIsConsumedExactlyOnce(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_request-1", testNow, 10*time.Minute)

	response := makeResponse(validOptions("_request-1", "_assertion-1"))
	validated, err := correlator.ValidateResponseAt(response, testNow)
	if err != nil {
		t.Fatalf("first validation: %v", err)
	}
	if validated.InResponseTo != "_request-1" {
		t.Fatalf("InResponseTo = %q", validated.InResponseTo)
	}
	if validated.IdPInitiated {
		t.Fatal("solicited response marked as IdP-initiated")
	}
	if len(validated.Assertions) != 1 || validated.Assertions[0].ID != "_assertion-1" {
		t.Fatalf("assertions = %+v", validated.Assertions)
	}

	if _, err := correlator.ValidateResponseAt(response, testNow.Add(time.Second)); !errors.Is(err, netsec.ErrRequestReplayed) {
		t.Fatalf("replay error = %v, want ErrRequestReplayed", err)
	}
}

func TestWrongInResponseToIsRejectedWithoutConsumingRegistration(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_expected", testNow, 10*time.Minute)

	response := makeResponse(validOptions("_attacker", "_assertion-wrong"))
	if _, err := correlator.ValidateResponseAt(response, testNow); !errors.Is(err, netsec.ErrUnknownRequest) {
		t.Fatalf("wrong InResponseTo error = %v, want ErrUnknownRequest", err)
	}

	response = makeResponse(validOptions("_expected", "_assertion-right"))
	if _, err := correlator.ValidateResponseAt(response, testNow); err != nil {
		t.Fatalf("correct response after mismatch: %v", err)
	}
}

func TestExpiredRegistrationIsRejected(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_expired", testNow, -time.Second)

	response := makeResponse(validOptions("_expired", "_assertion-expired-request"))
	if _, err := correlator.ValidateResponseAt(response, testNow); !errors.Is(err, netsec.ErrExpiredRequest) {
		t.Fatalf("expired registration error = %v, want ErrExpiredRequest", err)
	}

	register(t, correlator, "_expired", testNow, 10*time.Minute)
	if _, err := correlator.ValidateResponseAt(response, testNow); err != nil {
		t.Fatalf("response after re-registration: %v", err)
	}
}

func TestRegistrationIsExpiredAtItsExpiryInstant(t *testing.T) {
	correlator := newCorrelator(t, false)
	if err := correlator.RegisterRequest("_boundary", testNow); err != nil {
		t.Fatalf("RegisterRequest: %v", err)
	}

	response := makeResponse(validOptions("_boundary", "_boundary-assertion"))
	if _, err := correlator.ValidateResponseAt(response, testNow); !errors.Is(err, netsec.ErrExpiredRequest) {
		t.Fatalf("expiry-boundary error = %v, want ErrExpiredRequest", err)
	}
}

func TestIdPInitiatedPolicy(t *testing.T) {
	options := validOptions("", "_idp-assertion")
	options.confirmationInResponseTo = ""
	response := makeResponse(options)

	disabled := newCorrelator(t, false)
	if _, err := disabled.ValidateResponseAt(response, testNow); !errors.Is(err, netsec.ErrIdPInitiatedDisabled) {
		t.Fatalf("disabled IdP-initiated error = %v, want ErrIdPInitiatedDisabled", err)
	}

	enabled := newCorrelator(t, true)
	validated, err := enabled.ValidateResponseAt(response, testNow)
	if err != nil {
		t.Fatalf("enabled IdP-initiated validation: %v", err)
	}
	if !validated.IdPInitiated {
		t.Fatal("response not marked as IdP-initiated")
	}
	if _, err := enabled.ValidateResponseAt(response, testNow.Add(time.Second)); !errors.Is(err, netsec.ErrAssertionReplayed) {
		t.Fatalf("IdP-initiated replay error = %v, want ErrAssertionReplayed", err)
	}
}

func TestAssertionReplayAcrossSessionsIsRejectedAtomically(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_session-1", testNow, 10*time.Minute)
	response1 := makeResponse(validOptions("_session-1", "_captured-assertion"))
	if _, err := correlator.ValidateResponseAt(response1, testNow); err != nil {
		t.Fatalf("first session: %v", err)
	}

	register(t, correlator, "_session-2", testNow, 10*time.Minute)
	replay := makeResponse(validOptions("_session-2", "_captured-assertion"))
	if _, err := correlator.ValidateResponseAt(replay, testNow.Add(time.Second)); !errors.Is(err, netsec.ErrAssertionReplayed) {
		t.Fatalf("cross-session assertion replay error = %v, want ErrAssertionReplayed", err)
	}

	fresh := makeResponse(validOptions("_session-2", "_fresh-assertion"))
	if _, err := correlator.ValidateResponseAt(fresh, testNow.Add(time.Second)); err != nil {
		t.Fatalf("second session should remain usable after rejected replay: %v", err)
	}
}

func TestRecipientMustEqualACS(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_recipient", testNow, 10*time.Minute)

	options := validOptions("_recipient", "_recipient-assertion")
	options.recipient = "https://other-sp.example.org/acs"
	if _, err := correlator.ValidateResponseAt(makeResponse(options), testNow); !errors.Is(err, netsec.ErrRecipientMismatch) {
		t.Fatalf("recipient mismatch error = %v, want ErrRecipientMismatch", err)
	}

	options.recipient = testACS
	if _, err := correlator.ValidateResponseAt(makeResponse(options), testNow); err != nil {
		t.Fatalf("correct recipient after mismatch: %v", err)
	}
}

func TestNotOnOrAfterAllowsSixtySecondsSkew(t *testing.T) {
	tests := []struct {
		name      string
		deadline  time.Time
		wantError error
	}{
		{name: "inside skew", deadline: testNow.Add(-59 * time.Second)},
		{name: "at skew boundary", deadline: testNow.Add(-60 * time.Second)},
		{name: "outside skew", deadline: testNow.Add(-60*time.Second - time.Nanosecond), wantError: netsec.ErrAssertionExpired},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			correlator := newCorrelator(t, false)
			requestID := fmt.Sprintf("_skew-request-%d", i)
			assertionID := fmt.Sprintf("_skew-assertion-%d", i)
			register(t, correlator, requestID, testNow, 10*time.Minute)

			options := validOptions(requestID, assertionID)
			options.conditionsNotOnOrAfter = test.deadline.Format(time.RFC3339Nano)
			options.confirmationNotOnOrAfter = test.deadline.Format(time.RFC3339Nano)
			_, err := correlator.ValidateResponseAt(makeResponse(options), testNow)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("validation: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantError) {
				t.Fatalf("validation error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestExpiredSubjectConfirmationIsRejected(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_confirmation-expiry", testNow, 10*time.Minute)

	options := validOptions("_confirmation-expiry", "_confirmation-expiry-assertion")
	options.confirmationNotOnOrAfter = testNow.Add(-61 * time.Second).Format(time.RFC3339)
	if _, err := correlator.ValidateResponseAt(makeResponse(options), testNow); !errors.Is(err, netsec.ErrAssertionExpired) {
		t.Fatalf("expired SubjectConfirmationData error = %v, want ErrAssertionExpired", err)
	}
}

func TestOnlyOneConcurrentValidationConsumesTheRequest(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_concurrent", testNow, 10*time.Minute)
	response := makeResponse(validOptions("_concurrent", "_concurrent-assertion"))

	const workers = 64
	var successes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if _, err := correlator.ValidateResponseAt(response, testNow); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful validations = %d, want 1", got)
	}
}

func TestBeginAuthnRequestRegistersGeneratedID(t *testing.T) {
	correlator := newCorrelator(t, false)
	before := time.Now()
	request, err := correlator.BeginAuthnRequest(5 * time.Minute)
	if err != nil {
		t.Fatalf("BeginAuthnRequest: %v", err)
	}
	if !strings.HasPrefix(request.ID, "_") || len(request.ID) < 16 {
		t.Fatalf("generated ID = %q", request.ID)
	}
	if !request.ExpiresAt.After(before.Add(4 * time.Minute)) {
		t.Fatalf("expiry = %v, not within the requested lifetime", request.ExpiresAt)
	}

	now := time.Now()
	options := validOptions(request.ID, "_generated-request-assertion")
	options.conditionsNotBefore = now.Add(-time.Minute).Format(time.RFC3339)
	options.conditionsNotOnOrAfter = now.Add(10 * time.Minute).Format(time.RFC3339)
	options.confirmationNotOnOrAfter = now.Add(10 * time.Minute).Format(time.RFC3339)
	if _, err := correlator.ValidateResponseAt(makeResponse(options), now); err != nil {
		t.Fatalf("validation of generated request: %v", err)
	}
}

func TestMalformedAndIncompleteResponsesAreRejected(t *testing.T) {
	correlator := newCorrelator(t, false)
	register(t, correlator, "_malformed", testNow, 10*time.Minute)

	if _, err := correlator.ValidateResponseAt([]byte("<not-xml"), testNow); !errors.Is(err, netsec.ErrMalformedResponse) {
		t.Fatalf("malformed error = %v, want ErrMalformedResponse", err)
	}

	noAssertion := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" InResponseTo="_malformed"/>`)
	if _, err := correlator.ValidateResponseAt(noAssertion, testNow); !errors.Is(err, netsec.ErrMissingAssertion) {
		t.Fatalf("missing assertion error = %v, want ErrMissingAssertion", err)
	}
}

func makeResponse(options responseOptions) []byte {
	if options.responseID == "" {
		options.responseID = "_response"
	}
	if options.assertionID == "" {
		options.assertionID = "_assertion"
	}
	if options.issuer == "" {
		options.issuer = testIdP
	}
	if options.recipient == "" {
		options.recipient = testACS
	}

	conditions := ""
	if options.conditionsNotBefore != "" || options.conditionsNotOnOrAfter != "" {
		conditions = fmt.Sprintf(
			`<saml:Conditions%s%s><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction></saml:Conditions>`,
			attribute("NotBefore", options.conditionsNotBefore),
			attribute("NotOnOrAfter", options.conditionsNotOnOrAfter),
			html.EscapeString(testACS),
		)
	}

	confirmationData := fmt.Sprintf(
		`<saml:SubjectConfirmationData%s%s%s%s/>`,
		attribute("InResponseTo", options.confirmationInResponseTo),
		attribute("Recipient", options.recipient),
		attribute("NotBefore", options.confirmationNotBefore),
		attribute("NotOnOrAfter", options.confirmationNotOnOrAfter),
	)

	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"
    xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"
    ID="%s"%s Destination="%s" IssueInstant="%s">
  <saml:Issuer>%s</saml:Issuer>
  <samlp:Status>
    <samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/>
  </samlp:Status>
  <saml:Assertion ID="%s" IssueInstant="%s">
    <saml:Issuer>%s</saml:Issuer>
    <saml:Subject>
      <saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified">alice@example.org</saml:NameID>
      <saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">
        %s
      </saml:SubjectConfirmation>
    </saml:Subject>
    %s
    <saml:AuthnStatement AuthnInstant="%s" SessionIndex="_session">
      <saml:AuthnContext>
        <saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef>
      </saml:AuthnContext>
    </saml:AuthnStatement>
  </saml:Assertion>
</samlp:Response>`,
		html.EscapeString(options.responseID),
		attribute("InResponseTo", options.inResponseTo),
		html.EscapeString(options.recipient),
		testNow.Format(time.RFC3339),
		html.EscapeString(options.issuer),
		html.EscapeString(options.assertionID),
		testNow.Format(time.RFC3339),
		html.EscapeString(options.issuer),
		confirmationData,
		conditions,
		testNow.Format(time.RFC3339),
	))
}

func attribute(name, value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf(` %s="%s"`, name, html.EscapeString(value))
}
