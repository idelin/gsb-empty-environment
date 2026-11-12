package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testTeamID   = "TEAM123456"
	testBundleID = "com.example.app"
	testKeyID    = "attest-key-1"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

type fixture struct {
	t        *testing.T
	store    *MemoryStore
	verifier *Verifier
	clock    *fakeClock
	private  *ecdsa.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	clock := &fakeClock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	store := NewMemoryStore()
	verifier, err := NewVerifier(testTeamID, testBundleID, store)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	verifier.Now = clock.Now

	return &fixture{
		t:        t,
		store:    store,
		verifier: verifier,
		clock:    clock,
		private:  private,
	}
}

func (f *fixture) issueChallenge() string {
	f.t.Helper()
	challenge, err := f.verifier.IssueChallenge()
	if err != nil {
		f.t.Fatalf("issue challenge: %v", err)
	}
	return challenge
}

func (f *fixture) request(challenge string, body []byte, assertion Assertion) VerifyRequest {
	return VerifyRequest{
		KeyID:     testKeyID,
		PublicKey: &f.private.PublicKey,
		Challenge: challenge,
		Body:      body,
		Assertion: assertion,
	}
}

func (f *fixture) recordedCounter() (uint32, bool) {
	f.t.Helper()
	counter, found, err := f.store.Counter(testKeyID)
	if err != nil {
		f.t.Fatalf("read counter: %v", err)
	}
	return counter, found
}

func makeAssertion(t *testing.T, private *ecdsa.PrivateKey, rpID string, flags byte, counter uint32, challenge string, body []byte) Assertion {
	t.Helper()

	rpIDHash := sha256.Sum256([]byte(rpID))
	authenticatorData := make([]byte, authenticatorDataLen)
	copy(authenticatorData[:rpIDHashLen], rpIDHash[:])
	authenticatorData[flagsOffset] = flags
	binary.BigEndian.PutUint32(authenticatorData[counterOffset:], counter)

	bodyHash := sha256.Sum256(body)
	challengeInput := make([]byte, 0, len(challenge)+len(bodyHash))
	challengeInput = append(challengeInput, challenge...)
	challengeInput = append(challengeInput, bodyHash[:]...)
	clientDataHash := sha256.Sum256(challengeInput)

	signatureInput := make([]byte, 0, len(authenticatorData)+len(clientDataHash))
	signatureInput = append(signatureInput, authenticatorData...)
	signatureInput = append(signatureInput, clientDataHash[:]...)
	digest := sha256.Sum256(signatureInput)
	signature, err := ecdsa.SignASN1(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}

	return Assertion{
		AuthenticatorData: authenticatorData,
		ClientDataHash:    clientDataHash[:],
		Signature:         signature,
	}
}

func requireErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want errors.Is(..., %v)", got, want)
	}
}

func TestVerifyAcceptsAndAdvancesCounter(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"transfer","amount":100}`)

	firstChallenge := f.issueChallenge()
	firstAssertion := makeAssertion(t, f.private, testTeamID+"."+testBundleID, 0x00, 1, firstChallenge, body)
	if err := f.verifier.Verify(f.request(firstChallenge, body, firstAssertion)); err != nil {
		t.Fatalf("first verification failed: %v", err)
	}
	if counter, found := f.recordedCounter(); !found || counter != 1 {
		t.Fatalf("counter after first assertion = %d, found=%v; want 1, true", counter, found)
	}

	// Non-consecutive values are allowed as long as they strictly increase.
	secondChallenge := f.issueChallenge()
	secondAssertion := makeAssertion(t, f.private, testTeamID+"."+testBundleID, 0x00, 7, secondChallenge, body)
	if err := f.verifier.Verify(f.request(secondChallenge, body, secondAssertion)); err != nil {
		t.Fatalf("second verification failed: %v", err)
	}
	if counter, found := f.recordedCounter(); !found || counter != 7 {
		t.Fatalf("counter after second assertion = %d, found=%v; want 7, true", counter, found)
	}
}

func TestVerifyRejectsOldCounter(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)
	rpID := testTeamID + "." + testBundleID

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, rpID, 0x00, 5, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("prime verification failed: %v", err)
	}

	for _, counter := range []uint32{5, 4} {
		challenge = f.issueChallenge()
		assertion = makeAssertion(t, f.private, rpID, 0x00, counter, challenge, body)
		err := f.verifier.Verify(f.request(challenge, body, assertion))
		requireErrorIs(t, err, ErrCounterNotAdvanced)
		if got, found := f.recordedCounter(); !found || got != 5 {
			t.Fatalf("counter changed after rejected counter %d: got %d, found=%v; want 5, true", counter, got, found)
		}
	}
}

func TestVerifyRejectsRPIDHashMismatch(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, testTeamID+"."+testBundleID, 0x00, 1, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("prime verification failed: %v", err)
	}

	challenge = f.issueChallenge()
	// The assertion is internally consistent and signed with the right key,
	// but it was made for a different app identity.
	assertion = makeAssertion(t, f.private, "OTHER_TEAM.com.example.other", 0x00, 2, challenge, body)
	err := f.verifier.Verify(f.request(challenge, body, assertion))
	requireErrorIs(t, err, ErrRPIDHashMismatch)

	if got, found := f.recordedCounter(); !found || got != 1 {
		t.Fatalf("counter changed after rpIdHash mismatch: got %d, found=%v; want 1, true", got, found)
	}
}

func TestVerifyRejectsChallengeReuse(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)
	rpID := testTeamID + "." + testBundleID

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, rpID, 0x00, 1, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("prime verification failed: %v", err)
	}

	// A fresh, correctly signed assertion still cannot reuse the challenge.
	replay := makeAssertion(t, f.private, rpID, 0x00, 2, challenge, body)
	err := f.verifier.Verify(f.request(challenge, body, replay))
	requireErrorIs(t, err, ErrChallengeReused)

	if got, found := f.recordedCounter(); !found || got != 1 {
		t.Fatalf("counter changed after challenge reuse: got %d, found=%v; want 1, true", got, found)
	}
}

func TestVerifyRejectsBodySubstitution(t *testing.T) {
	f := newFixture(t)
	originalBody := []byte(`{"action":"transfer","amount":1}`)
	substitutedBody := []byte(`{"action":"transfer","amount":999}`)
	rpID := testTeamID + "." + testBundleID

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, rpID, 0x00, 1, challenge, originalBody)
	err := f.verifier.Verify(f.request(challenge, substitutedBody, assertion))
	requireErrorIs(t, err, ErrClientDataHashMismatch)

	if got, found := f.recordedCounter(); found || got != 0 {
		t.Fatalf("counter changed after body substitution: got %d, found=%v; want 0, false", got, found)
	}
}

func TestVerifyFailureLeavesCounterUnchanged(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)
	rpID := testTeamID + "." + testBundleID

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, rpID, 0x00, 7, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("prime verification failed: %v", err)
	}
	if got, _ := f.recordedCounter(); got != 7 {
		t.Fatalf("prime counter = %d, want 7", got)
	}

	tests := []struct {
		name  string
		want  error
		build func(challenge string) (Assertion, []byte)
	}{
		{
			name: "bad signature",
			want: ErrInvalidSignature,
			build: func(challenge string) (Assertion, []byte) {
				assertion := makeAssertion(t, f.private, rpID, 0x00, 8, challenge, body)
				assertion.Signature[len(assertion.Signature)-1] ^= 0xff
				return assertion, body
			},
		},
		{
			name: "non-zero flags",
			want: ErrInvalidFlags,
			build: func(challenge string) (Assertion, []byte) {
				return makeAssertion(t, f.private, rpID, 0x01, 8, challenge, body), body
			},
		},
		{
			name: "client data hash not bound to body",
			want: ErrClientDataHashMismatch,
			build: func(challenge string) (Assertion, []byte) {
				assertion := makeAssertion(t, f.private, rpID, 0x00, 8, challenge, body)
				assertion.ClientDataHash = make([]byte, sha256.Size)
				return assertion, body
			},
		},
		{
			name: "old counter",
			want: ErrCounterNotAdvanced,
			build: func(challenge string) (Assertion, []byte) {
				return makeAssertion(t, f.private, rpID, 0x00, 6, challenge, body), body
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			challenge := f.issueChallenge()
			assertion, requestBody := test.build(challenge)
			err := f.verifier.Verify(f.request(challenge, requestBody, assertion))
			requireErrorIs(t, err, test.want)

			if got, found := f.recordedCounter(); !found || got != 7 {
				t.Fatalf("counter after failed verification = %d, found=%v; want 7, true", got, found)
			}
		})
	}
}

func TestChallengeExpiresAfterSixtySeconds(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)
	rpID := testTeamID + "." + testBundleID
	start := f.clock.now

	challenge := f.issueChallenge()
	f.clock.now = start.Add(59 * time.Second)
	assertion := makeAssertion(t, f.private, rpID, 0x00, 1, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("verification at 59s failed: %v", err)
	}

	issuedAt := f.clock.now
	challenge = f.issueChallenge()
	f.clock.now = issuedAt.Add(60 * time.Second)
	assertion = makeAssertion(t, f.private, rpID, 0x00, 2, challenge, body)
	err := f.verifier.Verify(f.request(challenge, body, assertion))
	requireErrorIs(t, err, ErrChallengeExpired)

	if got, found := f.recordedCounter(); !found || got != 1 {
		t.Fatalf("counter changed after expired challenge: got %d, found=%v; want 1, true", got, found)
	}
}

func TestVerifyRejectsMalformedLayout(t *testing.T) {
	rpID := testTeamID + "." + testBundleID

	tests := []struct {
		name   string
		want   error
		mutate func(assertion *Assertion)
	}{
		{
			name:   "short authenticator data",
			want:   ErrInvalidAuthenticatorData,
			mutate: func(a *Assertion) { a.AuthenticatorData = a.AuthenticatorData[:len(a.AuthenticatorData)-1] },
		},
		{
			name:   "trailing authenticator data",
			want:   ErrInvalidAuthenticatorData,
			mutate: func(a *Assertion) { a.AuthenticatorData = append(a.AuthenticatorData, 0) },
		},
		{
			name:   "short client data hash",
			want:   ErrInvalidClientDataHash,
			mutate: func(a *Assertion) { a.ClientDataHash = a.ClientDataHash[:sha256.Size-1] },
		},
		{
			name:   "empty signature",
			want:   ErrInvalidSignature,
			mutate: func(a *Assertion) { a.Signature = nil },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			body := []byte(`{}`)
			challenge := f.issueChallenge()
			assertion := makeAssertion(t, f.private, rpID, 0x00, 1, challenge, body)
			test.mutate(&assertion)
			err := f.verifier.Verify(f.request(challenge, body, assertion))
			requireErrorIs(t, err, test.want)

			if got, found := f.recordedCounter(); found || got != 0 {
				t.Fatalf("counter changed after malformed assertion: got %d, found=%v", got, found)
			}
		})
	}
}

func TestVerifyRejectsNonP256Key(t *testing.T) {
	f := newFixture(t)
	private, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 key: %v", err)
	}

	body := []byte(`{}`)
	challenge := f.issueChallenge()
	assertion := Assertion{
		AuthenticatorData: make([]byte, authenticatorDataLen),
		ClientDataHash:    make([]byte, sha256.Size),
		Signature:         []byte{0x30, 0x00},
	}
	req := f.request(challenge, body, assertion)
	req.PublicKey = &private.PublicKey

	err = f.verifier.Verify(req)
	requireErrorIs(t, err, ErrInvalidPublicKey)
}

func TestConcurrentAssertionsCannotBothAdvanceCounter(t *testing.T) {
	f := newFixture(t)
	body := []byte(`{"action":"read"}`)
	rpID := testTeamID + "." + testBundleID

	challenge := f.issueChallenge()
	assertion := makeAssertion(t, f.private, rpID, 0x00, 4, challenge, body)
	if err := f.verifier.Verify(f.request(challenge, body, assertion)); err != nil {
		t.Fatalf("prime verification failed: %v", err)
	}

	challenges := []string{f.issueChallenge(), f.issueChallenge()}
	requests := make([]VerifyRequest, len(challenges))
	for i, challenge := range challenges {
		assertion := makeAssertion(t, f.private, rpID, 0x00, 5, challenge, body)
		requests[i] = f.request(challenge, body, assertion)
	}

	results := make(chan error, len(requests))
	var wg sync.WaitGroup
	for _, req := range requests {
		wg.Add(1)
		go func(req VerifyRequest) {
			defer wg.Done()
			results <- f.verifier.Verify(req)
		}(req)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrCounterConflict) && !errors.Is(err, ErrCounterNotAdvanced) {
			t.Fatalf("unexpected concurrent verification error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent assertions succeeded %d times, want exactly 1", successes)
	}
	if got, found := f.recordedCounter(); !found || got != 5 {
		t.Fatalf("counter after concurrent assertions = %d, found=%v; want 5, true", got, found)
	}
}

func TestMemoryStoreRejectsChallengeReissue(t *testing.T) {
	store := NewMemoryStore()
	expiresAt := time.Now().Add(time.Minute)
	if err := store.PutChallenge("challenge", expiresAt); err != nil {
		t.Fatalf("put challenge: %v", err)
	}
	if err := store.PutChallenge("challenge", expiresAt); !errors.Is(err, ErrChallengeDuplicate) {
		t.Fatalf("re-put error = %v, want ErrChallengeDuplicate", err)
	}
}
