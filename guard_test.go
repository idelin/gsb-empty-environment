package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newTestClock() *testClock {
	return &testClock{at: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
}

func (fake *testClock) Now() time.Time {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.at
}

func (fake *testClock) Advance(duration time.Duration) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.at = fake.at.Add(duration)
}

func signedResponse(t *testing.T, key []byte, challenge Challenge) AuthenticationResponse {
	t.Helper()
	payload := AuthenticationPayload(challenge.TransactionID, challenge.ServerChallenge)
	return AuthenticationResponse{
		TransactionID:   challenge.TransactionID,
		ServerChallenge: append([]byte(nil), challenge.ServerChallenge...),
		Signature:       SignHMAC(key, payload),
	}
}

func registerHMACOrder(guard *Guard, matchingID, eid, code string) []byte {
	key := []byte("key-" + matchingID)
	_ = guard.RegisterOrder(Order{
		MatchingID:       matchingID,
		EID:              eid,
		ConfirmationCode: code,
		Verifier:         NewHMACVerifier(key),
	})
	return key
}

func profileRequest(challenge Challenge, matchingID, eid, code string) ProfileRequest {
	return ProfileRequest{
		TransactionID:    challenge.TransactionID,
		MatchingID:       matchingID,
		EID:              eid,
		ConfirmationCode: code,
	}
}

func TestHappyPathConsumesAndBindsMatchingID(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now), WithSessionTTL(time.Minute))
	keyA := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")
	registerHMACOrder(guard, "matching-b", "EID-B", "87654321")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatalf("InitiateAuthentication: %v", err)
	}
	if challenge.TransactionID == "" || len(challenge.ServerChallenge) != serverChallengeBytes {
		t.Fatalf("unexpected challenge: %+v", challenge)
	}
	if want := clock.Now().Add(time.Minute); !challenge.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", challenge.ExpiresAt, want)
	}

	response := signedResponse(t, keyA, challenge)
	response.EID = "EID-A"
	if err := guard.AuthenticateClient(response); err != nil {
		t.Fatalf("AuthenticateClient: %v", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "12345678")); err != nil {
		t.Fatalf("GetBoundProfilePackage: %v", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "12345678")); !errors.Is(err, ErrMatchingIDConsumed) {
		t.Fatalf("second profile download = %v, want ErrMatchingIDConsumed", err)
	}

	eid, consumed, found := guard.MatchingIDBinding("matching-a")
	if !found || !consumed || eid != "EID-A" {
		t.Fatalf("binding = (%q, %v, %v), want (EID-A, true, true)", eid, consumed, found)
	}
	if _, err := guard.InitiateAuthentication("matching-a"); !errors.Is(err, ErrMatchingIDConsumed) {
		t.Fatalf("re-initiate consumed matchingId: %v, want ErrMatchingIDConsumed", err)
	}

	// The other order is unaffected.
	if _, err := guard.InitiateAuthentication("matching-b"); err != nil {
		t.Fatalf("matching-b should still be available: %v", err)
	}
}

func TestProfileCannotBeFetchedBeforeAuthentication(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	registerHMACOrder(guard, "matching-a", "EID-A", "12345678")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "12345678")); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("pre-auth profile request = %v, want ErrAuthenticationRequired", err)
	}
}

func TestReplayOfAuthenticationResponseIsRejected(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	response := signedResponse(t, key, challenge)
	response.EID = "EID-A"
	if err := guard.AuthenticateClient(response); err != nil {
		t.Fatalf("first authentication: %v", err)
	}
	if err := guard.AuthenticateClient(response); !errors.Is(err, ErrAuthenticationReplay) {
		t.Fatalf("replay = %v, want ErrAuthenticationReplay", err)
	}

	// A fresh transactionId does not make an old response valid either.
	fresh, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	oldChallengeResponse := signedResponse(t, key, challenge)
	oldChallengeResponse.TransactionID = fresh.TransactionID
	oldChallengeResponse.EID = "EID-A"
	if err := guard.AuthenticateClient(oldChallengeResponse); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("old challenge on fresh transaction = %v, want ErrChallengeMismatch", err)
	}

	// Retrying with the captured exact response for the fresh transactionId
	// must not work.
	if err := guard.AuthenticateClient(response); !errors.Is(err, ErrAuthenticationReplay) {
		t.Fatalf("captured exact response retry = %v, want ErrAuthenticationReplay", err)
	}
}

func TestOnlyOneConcurrentUseOfAuthenticationResponse(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")
	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	response := signedResponse(t, key, challenge)
	response.EID = "EID-A"

	const workers = 16
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := guard.AuthenticateClient(response); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful uses = %d, want exactly 1", got)
	}
}

func TestSessionExpiryInvalidatesChallenge(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now), WithSessionTTL(30*time.Second))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	response := signedResponse(t, key, challenge)
	response.EID = "EID-A"

	clock.Advance(31 * time.Second)
	if err := guard.AuthenticateClient(response); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired authentication = %v, want ErrSessionExpired", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "12345678")); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired profile request = %v, want ErrSessionExpired", err)
	}
}

func TestSupersededChallengeCannotBeUsed(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")

	first, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.TransactionID == second.TransactionID {
		t.Fatal("transactionId was reused")
	}

	old := signedResponse(t, key, first)
	old.EID = "EID-A"
	if err := guard.AuthenticateClient(old); !errors.Is(err, ErrSessionSuperseded) {
		t.Fatalf("old challenge = %v, want ErrSessionSuperseded", err)
	}

	current := signedResponse(t, key, second)
	current.EID = "EID-A"
	if err := guard.AuthenticateClient(current); err != nil {
		t.Fatalf("current challenge: %v", err)
	}
}

func TestNewChallengeSupersedesInFlightVerification(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := guard.RegisterOrder(Order{
		MatchingID:       "matching-a",
		EID:              "EID-A",
		ConfirmationCode: "12345678",
		Verifier: VerifierFunc(func(_, _ []byte) error {
			once.Do(func() { close(started) })
			<-release
			return nil
		}),
	}); err != nil {
		t.Fatal(err)
	}

	first, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	firstResponse := signedResponse(t, nil, first)
	firstResponse.EID = "EID-A"

	result := make(chan error, 1)
	go func() {
		result <- guard.AuthenticateClient(firstResponse)
	}()
	<-started

	second, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrSessionSuperseded) {
		t.Fatalf("in-flight verification = %v, want ErrSessionSuperseded", err)
	}

	secondResponse := signedResponse(t, nil, second)
	secondResponse.EID = "EID-A"
	if err := guard.AuthenticateClient(secondResponse); err != nil {
		t.Fatalf("new challenge after supersede: %v", err)
	}
}

func TestCrossOrderMatchingIDAndEIDAreRejected(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	keyA := registerHMACOrder(guard, "matching-a", "EID-A", "code-a")
	keyB := registerHMACOrder(guard, "matching-b", "EID-B", "code-b")

	challengeA, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	responseA := signedResponse(t, keyA, challengeA)
	responseA.EID = "EID-A"
	if err := guard.AuthenticateClient(responseA); err != nil {
		t.Fatal(err)
	}

	// Authenticated transaction A cannot be used to fetch order B's profile.
	if err := guard.GetBoundProfilePackage(profileRequest(challengeA, "matching-b", "EID-B", "code-b")); !errors.Is(err, ErrMatchingIDMismatch) {
		t.Fatalf("transaction A + matching-b = %v, want ErrMatchingIDMismatch", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challengeA, "matching-a", "EID-B", "code-a")); !errors.Is(err, ErrEIDMismatch) {
		t.Fatalf("transaction A + EID-B = %v, want ErrEIDMismatch", err)
	}

	// Nor can another eUICC respond to order A's challenge while claiming
	// order B's EID.
	challengeA2, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	eidSwap := signedResponse(t, keyA, challengeA2)
	eidSwap.EID = "EID-B"
	if err := guard.AuthenticateClient(eidSwap); !errors.Is(err, ErrEIDMismatch) {
		t.Fatalf("order A challenge + EID-B = %v, want ErrEIDMismatch", err)
	}

	// The attacker cannot authenticate order B's challenge with order A's key.
	challengeB, err := guard.InitiateAuthentication("matching-b")
	if err != nil {
		t.Fatal(err)
	}
	stolenKeyResponse := signedResponse(t, keyA, challengeB)
	stolenKeyResponse.EID = "EID-B"
	if err := guard.AuthenticateClient(stolenKeyResponse); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("order A key on order B = %v, want ErrSignatureInvalid", err)
	}

	// A valid order B flow still works and does not inherit bogus failure
	// counters from the cross-order attempts.
	challengeB2, err := guard.InitiateAuthentication("matching-b")
	if err != nil {
		t.Fatal(err)
	}
	validB2 := signedResponse(t, keyB, challengeB2)
	validB2.EID = "EID-B"
	if err := guard.AuthenticateClient(validB2); err != nil {
		t.Fatalf("valid order B authentication: %v", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challengeB2, "matching-b", "EID-B", "code-b")); err != nil {
		t.Fatalf("valid order B profile: %v", err)
	}
}

func TestSignatureMustCoverTransactionIDAndChallenge(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "12345678")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}

	transactionOnly := AuthenticationResponse{
		TransactionID:   challenge.TransactionID,
		ServerChallenge: challenge.ServerChallenge,
		EID:             "EID-A",
		Signature:       SignHMAC(key, []byte(challenge.TransactionID)),
	}
	if err := guard.AuthenticateClient(transactionOnly); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("signature over transactionId only = %v, want ErrSignatureInvalid", err)
	}

	// The failed attempt consumed the session; use a new challenge.
	challenge, err = guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	challengeOnly := AuthenticationResponse{
		TransactionID:   challenge.TransactionID,
		ServerChallenge: challenge.ServerChallenge,
		EID:             "EID-A",
		Signature:       SignHMAC(key, challenge.ServerChallenge),
	}
	if err := guard.AuthenticateClient(challengeOnly); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("signature over challenge only = %v, want ErrSignatureInvalid", err)
	}

	challenge, err = guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	tampered := signedResponse(t, key, challenge)
	tampered.EID = "EID-A"
	tampered.ServerChallenge = bytes.Clone(challenge.ServerChallenge)
	tampered.ServerChallenge[0] ^= 0xff
	if err := guard.AuthenticateClient(tampered); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("tampered challenge = %v, want ErrChallengeMismatch", err)
	}
}

func TestConfirmationCodeHashFormula(t *testing.T) {
	const (
		code        = "12345678"
		transaction = "0123456789abcdef"
	)
	inner := sha256.Sum256([]byte(code))
	input := append(inner[:], []byte(transaction)...)
	want := sha256.Sum256(input)

	got := ConfirmationCodeHash(code, transaction)
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("ConfirmationCodeHash = %x, want %x", got, want)
	}
	if bytes.Equal(got, ConfirmationCodeHash(code, transaction+"x")) {
		t.Fatal("confirmation hash is not bound to the transactionId")
	}
}

func TestWrongConfirmationCodesLockOrder(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now), WithFailureLimit(3))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "correct-code")

	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	response := signedResponse(t, key, challenge)
	response.EID = "EID-A"
	if err := guard.AuthenticateClient(response); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong-code"))
		if !errors.Is(err, ErrConfirmationCodeInvalid) {
			t.Fatalf("wrong attempt %d = %v, want ErrConfirmationCodeInvalid", attempt, err)
		}
	}
	err = guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong-code"))
	if !errors.Is(err, ErrOrderLocked) {
		t.Fatalf("fourth wrong attempt = %v, want ErrOrderLocked", err)
	}

	// Even the correct code, or a brand-new authenticated session, cannot
	// unlock the order.
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "correct-code")); !errors.Is(err, ErrOrderLocked) {
		t.Fatalf("correct code after lock = %v, want ErrOrderLocked", err)
	}
	if _, err := guard.InitiateAuthentication("matching-a"); !errors.Is(err, ErrOrderLocked) {
		t.Fatalf("initiate after lock = %v, want ErrOrderLocked", err)
	}
}

func TestCorrectConfirmationCodeIsStillAcceptedAtLimit(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now), WithFailureLimit(3))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "correct-code")
	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	response := signedResponse(t, key, challenge)
	response.EID = "EID-A"
	if err := guard.AuthenticateClient(response); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong")); !errors.Is(err, ErrConfirmationCodeInvalid) {
			t.Fatalf("wrong attempt %d = %v", attempt+1, err)
		}
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "correct-code")); err != nil {
		t.Fatalf("correct code at the limit: %v", err)
	}
}

func TestConfirmationFailuresPersistAcrossSessions(t *testing.T) {
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now), WithFailureLimit(3))
	key := registerHMACOrder(guard, "matching-a", "EID-A", "correct-code")

	makeAuthenticatedChallenge := func() Challenge {
		challenge, err := guard.InitiateAuthentication("matching-a")
		if err != nil {
			t.Fatal(err)
		}
		response := signedResponse(t, key, challenge)
		response.EID = "EID-A"
		if err := guard.AuthenticateClient(response); err != nil {
			t.Fatal(err)
		}
		return challenge
	}

	challenge := makeAuthenticatedChallenge()
	for attempt := 0; attempt < 2; attempt++ {
		if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong")); !errors.Is(err, ErrConfirmationCodeInvalid) {
			t.Fatalf("failure %d = %v", attempt, err)
		}
	}
	challenge = makeAuthenticatedChallenge()
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong")); !errors.Is(err, ErrConfirmationCodeInvalid) {
		t.Fatalf("third failure = %v", err)
	}
	if err := guard.GetBoundProfilePackage(profileRequest(challenge, "matching-a", "EID-A", "wrong")); !errors.Is(err, ErrOrderLocked) {
		t.Fatalf("fourth failure = %v, want ErrOrderLocked", err)
	}
}

func TestECDSAVerifierCoversPayload(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := newTestClock()
	guard := NewGuard(WithClock(clock.Now))
	if err := guard.RegisterOrder(Order{
		MatchingID:       "matching-a",
		EID:              "EID-A",
		ConfirmationCode: "12345678",
		Verifier:         NewECDSAVerifier(&privateKey.PublicKey),
	}); err != nil {
		t.Fatal(err)
	}
	challenge, err := guard.InitiateAuthentication("matching-a")
	if err != nil {
		t.Fatal(err)
	}
	payload := AuthenticationPayload(challenge.TransactionID, challenge.ServerChallenge)
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.AuthenticateClient(AuthenticationResponse{
		TransactionID:   challenge.TransactionID,
		ServerChallenge: challenge.ServerChallenge,
		EID:             "EID-A",
		Signature:       signature,
	}); err != nil {
		t.Fatalf("ECDSA authentication: %v", err)
	}
}
