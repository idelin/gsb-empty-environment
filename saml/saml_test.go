package saml

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeVerifier is a stand-in for real XML-DSig validation. It only confirms
// signatures whose Reference URI matches the signed element's own ID, and
// records the exact *Element it was asked to trust. Tests can therefore assert
// that claims come from the same node the verifier confirmed.
type fakeVerifier struct {
	mu       sync.Mutex
	verified []*Element
	failIDs  map[string]bool
}

func newFakeVerifier() *fakeVerifier {
	return &fakeVerifier{failIDs: make(map[string]bool)}
}

func (f *fakeVerifier) Verify(signature, signed *Element) error {
	var ref *Element
	walk(signature, func(el *Element) bool {
		if el.Name.Local == "Reference" && el.Name.Space == NSSignature {
			ref = el
			return false
		}
		return true
	})
	if ref == nil {
		return errors.New("no Reference")
	}
	uri := attrValue(ref, "", "URI")
	id, ok := elementID(signed)
	if !ok || uri != "#"+id {
		return errors.New("reference does not match signed node")
	}
	if f.failIDs[id] {
		return errors.New("verifier configured to reject")
	}
	f.mu.Lock()
	f.verified = append(f.verified, signed)
	f.mu.Unlock()
	return nil
}

func (f *fakeVerifier) lastVerified() *Element {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.verified) == 0 {
		return nil
	}
	return f.verified[len(f.verified)-1]
}

const validResponse = `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"
	xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"
	xmlns:ds="http://www.w3.org/2000/09/xmldsig#"
	ID="_response-1" Version="2.0" IssueInstant="2026-11-11T01:00:00Z">
  <saml:Issuer>https://idp.example.org</saml:Issuer>
  <saml:Assertion ID="_assertion-1" IssueInstant="2026-11-11T01:00:00Z" Version="2.0">
    <ds:Signature>
      <ds:SignedInfo>
        <ds:Reference URI="#_assertion-1">
          <ds:DigestValue>abc</ds:DigestValue>
        </ds:Reference>
      </ds:SignedInfo>
      <ds:SignatureValue>c2ln</ds:SignatureValue>
    </ds:Signature>
    <saml:Issuer>https://idp.example.org</saml:Issuer>
    <saml:Subject>
      <saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">alice@example.org</saml:NameID>
    </saml:Subject>
    <saml:Conditions NotBefore="2026-11-11T00:55:00Z" NotOnOrAfter="2026-11-11T01:05:00Z">
      <saml:AudienceRestriction>
        <saml:Audience>https://sp.example.org</saml:Audience>
      </saml:AudienceRestriction>
    </saml:Conditions>
  </saml:Assertion>
</samlp:Response>`

func newTestProcessor(v Verifier) (*Processor, *MemoryReplay) {
	replay := NewMemoryReplay()
	now := func() time.Time { return time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC) }
	return &Processor{Verifier: v, Replay: replay, Now: now, ClockSkew: time.Minute}, replay
}

func TestProcessValidResponse(t *testing.T) {
	v := newFakeVerifier()
	p, replay := newTestProcessor(v)

	claims, err := p.Process(strings.NewReader(validResponse))
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if claims.Subject != "alice@example.org" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if claims.Audience != "https://sp.example.org" {
		t.Fatalf("audience = %q", claims.Audience)
	}
	if claims.AssertionID != "_assertion-1" {
		t.Fatalf("assertion id = %q", claims.AssertionID)
	}
	if claims.Issuer != "https://idp.example.org" {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
	wantNB := time.Date(2026, 11, 11, 0, 55, 0, 0, time.UTC)
	if !claims.NotBefore.Equal(wantNB) {
		t.Fatalf("notBefore = %v", claims.NotBefore)
	}
	wantNOA := time.Date(2026, 11, 11, 1, 5, 0, 0, time.UTC)
	if !claims.NotOnOrAfter.Equal(wantNOA) {
		t.Fatalf("notOnOrAfter = %v", claims.NotOnOrAfter)
	}

	signed := v.lastVerified()
	if signed == nil || signed.Name.Local != "Assertion" {
		t.Fatalf("verifier did not confirm an assertion node")
	}
	nameID := firstChild(firstChild(signed, "Subject", NSAssertion), "NameID", NSAssertion)
	if nameID == nil || strings.TrimSpace(nameID.Text) != "alice@example.org" {
		t.Fatalf("claims not read from verified node")
	}
	if !replay.Seen("_assertion-1") {
		t.Fatal("accepted assertion ID was not recorded in replay store")
	}
}

// Same-ID wrapping: a second Assertion reuses the signed element's ID. The
// document-wide index must reject it before any claim is read.
func TestRejectsSameIDWrapping(t *testing.T) {
	wrapped := strings.Replace(validResponse,
		`<saml:Assertion ID="_assertion-1"`,
		`<saml:Assertion ID="_assertion-1" Version="2.0"></saml:Assertion>`+
			`<saml:Assertion ID="_assertion-1"`, 1)

	v := newFakeVerifier()
	p, replay := newTestProcessor(v)
	_, err := p.Process(strings.NewReader(wrapped))
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
	if replay.Seen("_assertion-1") {
		t.Fatal("rejected message must not consume an ID")
	}

	// The original message must still be usable: rejection is isolated.
	if _, err := p.Process(strings.NewReader(validResponse)); err != nil {
		t.Fatalf("valid response rejected after wrapping attempt: %v", err)
	}
}

// Namespace substitution: an Assertion-shaped element is carried in an
// attacker-controlled namespace while reusing SAML local names.
func TestRejectsNamespaceSubstitutionByURI(t *testing.T) {
	evil := strings.Replace(validResponse,
		`xmlns:ds="http://www.w3.org/2000/09/xmldsig#"`,
		`xmlns:ds="http://www.w3.org/2000/09/xmldsig#" xmlns:evil="https://attacker.example/ns"`, 1)
	evil = strings.Replace(evil,
		`<saml:Assertion ID="_assertion-1"`,
		`<evil:Assertion ID="_assertion-1"`, 1)

	p, _ := newTestProcessor(newFakeVerifier())
	_, err := p.Process(strings.NewReader(evil))
	if !errors.Is(err, ErrNamespaceSubstitution) {
		t.Fatalf("err = %v, want ErrNamespaceSubstitution", err)
	}
}

// xmlns rebinding: the saml prefix is re-declared on the assertion to a
// hostile URI. The decoder resolves the URI and the vocabulary check fires.
func TestRejectsNamespaceRebinding(t *testing.T) {
	evil := strings.Replace(validResponse,
		`<saml:Assertion ID="_assertion-1"`,
		`<saml:Assertion xmlns:saml="https://attacker.example/ns" ID="_assertion-1"`, 1)

	p, _ := newTestProcessor(newFakeVerifier())
	_, err := p.Process(strings.NewReader(evil))
	if !errors.Is(err, ErrNamespaceSubstitution) {
		t.Fatalf("err = %v, want ErrNamespaceSubstitution", err)
	}
}

// Signature points at an ID that does not exist in the index (classic XSW
// where the signed node is hidden behind an unsigned wrapper).
func TestRejectsDanglingReference(t *testing.T) {
	evil := strings.Replace(validResponse,
		`URI="#_assertion-1"`, `URI="#_ghost-assertion"`, 1)

	p, _ := newTestProcessor(newFakeVerifier())
	_, err := p.Process(strings.NewReader(evil))
	if !errors.Is(err, ErrReferenceTarget) {
		t.Fatalf("err = %v, want ErrReferenceTarget", err)
	}
}

// Claims must be extracted from the exact node the verifier confirmed.
func TestClaimsReadOnlyFromVerifiedNode(t *testing.T) {
	doc, err := Parse(strings.NewReader(validResponse))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v := newFakeVerifier()
	sigs := doc.Signatures()
	if len(sigs) != 1 {
		t.Fatalf("signatures = %d", len(sigs))
	}
	target, err := resolveReference(doc, sigs[0])
	if err != nil {
		t.Fatalf("resolveReference: %v", err)
	}
	if err := v.Verify(sigs[0], target); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	claims, err := extractClaims(v.lastVerified())
	if err != nil {
		t.Fatalf("extractClaims: %v", err)
	}
	if claims.Subject != "alice@example.org" {
		t.Fatalf("subject read from wrong node: %q", claims.Subject)
	}
	if claims.Audience != "https://sp.example.org" {
		t.Fatalf("audience read from wrong node: %q", claims.Audience)
	}
}

func TestVerifierFailureRejectsDocument(t *testing.T) {
	v := newFakeVerifier()
	v.failIDs["_assertion-1"] = true
	p, replay := newTestProcessor(v)

	_, err := p.Process(strings.NewReader(validResponse))
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if replay.Seen("_assertion-1") {
		t.Fatal("failed verification must not consume an ID")
	}
}

func TestReplayRejectedOnSecondUse(t *testing.T) {
	p, _ := newTestProcessor(newFakeVerifier())
	if _, err := p.Process(strings.NewReader(validResponse)); err != nil {
		t.Fatalf("first Process: %v", err)
	}
	_, err := p.Process(strings.NewReader(validResponse))
	if !errors.Is(err, ErrReplay) {
		t.Fatalf("second Process err = %v, want ErrReplay", err)
	}
}

// Concurrent first-use: exactly one of N goroutines may consume the same ID.
func TestConcurrentReplayIsolation(t *testing.T) {
	replay := NewMemoryReplay()
	const n = 64
	var wg sync.WaitGroup
	results := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			results <- replay.Consume("_assertion-1")
		}()
	}
	wg.Wait()
	close(results)

	var accepted, rejected int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrReplay):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || rejected != n-1 {
		t.Fatalf("accepted=%d rejected=%d, want 1 and %d", accepted, rejected, n-1)
	}
}

// Many distinct documents processed concurrently must each succeed once and
// stay isolated through the shared replay store.
func TestConcurrentDistinctIDs(t *testing.T) {
	replay := NewMemoryReplay()
	const docs = 32
	var wg sync.WaitGroup
	errs := make(chan error, docs)
	wg.Add(docs)
	for i := 0; i < docs; i++ {
		i := i
		go func() {
			defer wg.Done()
			xmlDoc := strings.ReplaceAll(validResponse, "_assertion-1",
				fmt.Sprintf("_assertion-%d", i))
			p := &Processor{
				Verifier:  newFakeVerifier(),
				Replay:    replay,
				Now:       func() time.Time { return time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC) },
				ClockSkew: time.Minute,
			}
			_, err := p.Process(strings.NewReader(xmlDoc))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Process: %v", err)
		}
	}
}

func TestTimeWindowEnforced(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want error
	}{
		{"inside window", time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC), nil},
		{"before NotBefore", time.Date(2026, 11, 11, 0, 50, 0, 0, time.UTC), ErrCondition},
		{"at NotOnOrAfter beyond skew", time.Date(2026, 11, 11, 1, 6, 30, 0, time.UTC), ErrCondition},
		{"after expiry", time.Date(2026, 11, 11, 1, 10, 0, 0, time.UTC), ErrCondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := NewMemoryReplay()
			p := &Processor{
				Verifier:  newFakeVerifier(),
				Replay:    replay,
				Now:       func() time.Time { return tc.now },
				ClockSkew: time.Minute,
			}
			_, err := p.Process(strings.NewReader(validResponse))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if replay.Seen("_assertion-1") {
				t.Fatal("time-rejected message must not consume an ID")
			}
		})
	}
}

func TestRejectsDOCTYPE(t *testing.T) {
	withDTD := `<!DOCTYPE samlp:Response [<!ENTITY x "y">]>` + validResponse
	p, _ := newTestProcessor(newFakeVerifier())
	_, err := p.Process(strings.NewReader(withDTD))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestNoSignatureRejected(t *testing.T) {
	unsigned := strings.Replace(validResponse,
		`<ds:Signature>
      <ds:SignedInfo>
        <ds:Reference URI="#_assertion-1">
          <ds:DigestValue>abc</ds:DigestValue>
        </ds:Reference>
      </ds:SignedInfo>
      <ds:SignatureValue>c2ln</ds:SignatureValue>
    </ds:Signature>`, "", 1)
	p, _ := newTestProcessor(newFakeVerifier())
	_, err := p.Process(strings.NewReader(unsigned))
	if !errors.Is(err, ErrNoSignature) {
		t.Fatalf("err = %v, want ErrNoSignature", err)
	}
}

func TestProcessorRequiresDependencies(t *testing.T) {
	if _, err := (&Processor{}).Process(strings.NewReader(validResponse)); err == nil {
		t.Fatal("expected error for processor without verifier")
	}
	if _, err := (&Processor{Verifier: newFakeVerifier()}).Process(strings.NewReader(validResponse)); err == nil {
		t.Fatal("expected error for processor without replay store")
	}
}
