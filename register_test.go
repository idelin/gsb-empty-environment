package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	testOrigin = "https://auth.example.com"
	testRPID   = "example.com"
)

// fakeVerifier returns a precomputed Verified result. It is safe for
// concurrent callers and can block on a barrier so overlapping registrations
// truly race inside their transactions.
type fakeVerifier struct {
	mu        sync.Mutex
	verified  Verified
	fail      error
	calls     atomic.Int64
	barrier   chan struct{}
	barrierWG sync.WaitGroup
}

func newFakeVerifier(v Verified) *fakeVerifier {
	return &fakeVerifier{verified: v}
}

// armBarrier makes VerifyAttestation block until releaseBarrier is called,
// allowing all concurrent registrations to reach the transaction together.
func (f *fakeVerifier) armBarrier(n int) {
	f.barrier = make(chan struct{})
	f.barrierWG.Add(n)
}

func (f *fakeVerifier) releaseBarrier() {
	if f.barrier != nil {
		close(f.barrier)
	}
}

func (f *fakeVerifier) VerifyAttestation(ctx context.Context, _ []byte) (Verified, error) {
	f.calls.Add(1)
	if f.barrier != nil {
		f.barrierWG.Done()
		select {
		case <-f.barrier:
		case <-ctx.Done():
			return Verified{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return Verified{}, f.fail
	}
	return f.verified, nil
}

func verifiedFixture(challenge, credentialID, publicKey []byte) Verified {
	return Verified{
		Purpose:      PurposeAttestation,
		Origin:       testOrigin,
		RPID:         testRPID,
		Challenge:    challenge,
		CredentialID: credentialID,
		PublicKey:    publicKey,
	}
}

func requestFixture(challenge, response, idemKey []byte, account string) RegisterRequest {
	return RegisterRequest{
		AccountID:           account,
		ExpectedOrigin:      testOrigin,
		ExpectedRPID:        testRPID,
		ExpectedChallenge:   challenge,
		AttestationResponse: response,
		IdempotencyKey:      idemKey,
	}
}

func newServiceWithChallenge(t *testing.T) (*Service, *MemoryStore, []byte) {
	t.Helper()
	challenge := []byte("challenge-abc")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verifier := newFakeVerifier(verifiedFixture(challenge, []byte("cred-1"), []byte("pub-key-1")))
	return NewService(verifier, store), store, challenge
}

func TestRegisterSuccess(t *testing.T) {
	svc, store, challenge := newServiceWithChallenge(t)
	req := requestFixture(challenge, []byte("attestation"), []byte("idem-1"), "alice")

	result, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if result.Replayed {
		t.Fatal("first registration must not be marked replayed")
	}
	if result.AccountID != "alice" || !bytes.Equal(result.CredentialID, []byte("cred-1")) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if store.CredentialCount() != 1 || store.LookupAccount([]byte("cred-1")) != "alice" {
		t.Fatal("credential was not bound to alice")
	}
}

func TestRegisterRejectsAssertionPurpose(t *testing.T) {
	challenge := []byte("challenge-assert")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verified := verifiedFixture(challenge, []byte("cred-assert"), []byte("pub-assert"))
	verified.Purpose = PurposeAssertion
	svc := NewService(newFakeVerifier(verified), store)

	_, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice"))
	if !errors.Is(err, ErrWrongCeremony) {
		t.Fatalf("expected ErrWrongCeremony, got %v", err)
	}
	if store.CredentialCount() != 0 {
		t.Fatal("assertion must not occupy a credential id")
	}
}

func TestRegisterVerifierFailure(t *testing.T) {
	challenge := []byte("challenge-bad")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verifier := newFakeVerifier(Verified{})
	verifier.fail = errors.New("bad attestation signature")
	svc := NewService(verifier, store)

	_, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice"))
	if !errors.Is(err, ErrVerifierFailed) {
		t.Fatalf("expected ErrVerifierFailed, got %v", err)
	}
	if store.CredentialCount() != 0 {
		t.Fatal("failed verification must not occupy a credential id")
	}
}

func TestRegisterMismatchRejections(t *testing.T) {
	challenge := []byte("challenge-mismatch")
	cases := []struct {
		name     string
		mutate   func(*RegisterRequest, *Verified)
		expected error
	}{
		{
			name: "origin mismatch",
			mutate: func(_ *RegisterRequest, v *Verified) {
				v.Origin = "https://evil.example"
			},
			expected: ErrOriginMismatch,
		},
		{
			name: "rp mismatch",
			mutate: func(_ *RegisterRequest, v *Verified) {
				v.RPID = "evil.example"
			},
			expected: ErrRPMismatch,
		},
		{
			name: "challenge mismatch",
			mutate: func(req *RegisterRequest, _ *Verified) {
				req.ExpectedChallenge = []byte("different-challenge")
			},
			expected: ErrChallengeMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.SaveChallenge(challenge)
			verified := verifiedFixture(challenge, []byte("cred-"+tc.name), []byte("pub-"+tc.name))
			verifier := newFakeVerifier(verified)
			svc := NewService(verifier, store)
			req := requestFixture(challenge, []byte("resp"), nil, "alice")
			tc.mutate(&req, &verified)
			verifier.verified = verified

			_, err := svc.Register(context.Background(), req)
			if !errors.Is(err, tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, err)
			}
			if store.CredentialCount() != 0 {
				t.Fatal("mismatched response must not occupy a credential id")
			}
		})
	}
}

func TestRegisterInvalidRequest(t *testing.T) {
	svc, _, challenge := newServiceWithChallenge(t)
	req := requestFixture(challenge, []byte("attestation"), nil, "")

	if _, err := svc.Register(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestRegisterCrossAccountConflict(t *testing.T) {
	svc, store, challenge := newServiceWithChallenge(t)
	first := requestFixture(challenge, []byte("attestation"), []byte("idem-alice"), "alice")
	if _, err := svc.Register(context.Background(), first); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// The same credential id and public key presented by another account.
	attacker := requestFixture(challenge, []byte("attestation"), []byte("idem-bob"), "bob")
	_, err := svc.Register(context.Background(), attacker)
	if !errors.Is(err, ErrCredentialAccountConflict) {
		t.Fatalf("expected ErrCredentialAccountConflict, got %v", err)
	}
	if store.LookupAccount([]byte("cred-1")) != "alice" || store.CredentialCount() != 1 {
		t.Fatal("credential ownership changed after cross-account attempt")
	}
}

func TestRegisterCrossKeyConflict(t *testing.T) {
	challenge := []byte("challenge-keys")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verifier := newFakeVerifier(verifiedFixture(challenge, []byte("cred-k"), []byte("pub-a")))
	svc := NewService(verifier, store)

	if _, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice")); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Same account, same credential id, but a different public key.
	verifier.verified = verifiedFixture(challenge, []byte("cred-k"), []byte("pub-b"))
	_, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice"))
	if !errors.Is(err, ErrCredentialKeyConflict) {
		t.Fatalf("expected ErrCredentialKeyConflict, got %v", err)
	}

	binding := store.credentials["cred-k"]
	if !bytes.Equal(binding.publicKey, []byte("pub-a")) {
		t.Fatal("public key was overwritten by conflicting registration")
	}
}

func TestRegisterIdempotentReplay(t *testing.T) {
	svc, store, challenge := newServiceWithChallenge(t)
	req := requestFixture(challenge, []byte("attestation"), []byte("idem-key"), "alice")

	first, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Response lost: the client retries byte-for-byte.
	second, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("retry registration failed: %v", err)
	}
	if !second.Replayed {
		t.Fatal("retry must be marked replayed")
	}
	if !bytes.Equal(second.CredentialID, first.CredentialID) || second.AccountID != first.AccountID {
		t.Fatal("retry returned a different result than the original")
	}
	if store.CredentialCount() != 1 {
		t.Fatal("idempotent retry created a second credential binding")
	}
}

func TestRegisterIdempotencyKeyReuseConflict(t *testing.T) {
	svc, store, challenge := newServiceWithChallenge(t)
	req := requestFixture(challenge, []byte("attestation"), []byte("idem-shared"), "alice")
	if _, err := svc.Register(context.Background(), req); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Same idempotency key, but a different attestation payload/credential.
	req.AttestationResponse = []byte("different-attestation")
	verifier := svc.verifier.(*fakeVerifier)
	verifier.verified = verifiedFixture(challenge, []byte("cred-other"), []byte("pub-other"))

	_, err := svc.Register(context.Background(), req)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
	if store.CredentialCount() != 1 {
		t.Fatal("idempotency conflict must not create a new binding")
	}
}

func TestRegisterNaturalReplayAfterResponseLoss(t *testing.T) {
	// No idempotency key: the credential id itself must still dedupe retries.
	svc, store, challenge := newServiceWithChallenge(t)
	req := requestFixture(challenge, []byte("attestation"), nil, "alice")

	first, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	second, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("retry registration failed: %v", err)
	}
	if !second.Replayed || !bytes.Equal(second.CredentialID, first.CredentialID) {
		t.Fatalf("retry did not return the original result: %+v", second)
	}
	if store.CredentialCount() != 1 {
		t.Fatal("natural replay created a second credential binding")
	}
}

func TestRegisterChallengeIsSingleUse(t *testing.T) {
	challenge := []byte("challenge-once")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verifier := newFakeVerifier(verifiedFixture(challenge, []byte("cred-a"), []byte("pub-a")))
	svc := NewService(verifier, store)

	if _, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice")); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// A different credential reusing the consumed challenge must be rejected.
	verifier.verified = verifiedFixture(challenge, []byte("cred-b"), []byte("pub-b"))
	_, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice"))
	if !errors.Is(err, ErrChallengeConsumed) {
		t.Fatalf("expected ErrChallengeConsumed, got %v", err)
	}
	if store.CredentialCount() != 1 {
		t.Fatal("replayed challenge created a second credential")
	}
}

func TestRegisterConcurrentSameCredentialUniqueOwnership(t *testing.T) {
	const contenders = 32
	challenge := []byte("challenge-race")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verified := verifiedFixture(challenge, []byte("cred-race"), []byte("pub-race"))
	verifier := newFakeVerifier(verified)
	svc := NewService(verifier, store)

	verifier.armBarrier(contenders)
	results := make([]Result, contenders)
	errs := make([]error, contenders)
	var wg sync.WaitGroup

	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := fmt.Sprintf("account-%02d", i)
			results[i], errs[i] = svc.Register(
				context.Background(),
				requestFixture(challenge, []byte("attestation"), []byte("idem-"+account), account),
			)
		}(i)
	}

	verifier.barrierWG.Wait()
	verifier.releaseBarrier()
	wg.Wait()

	var successes int
	winner := ""
	for i := 0; i < contenders; i++ {
		if errs[i] == nil {
			successes++
			winner = results[i].AccountID
			continue
		}
		if !errors.Is(errs[i], ErrCredentialAccountConflict) {
			t.Fatalf("contender %d got unexpected error: %v", i, errs[i])
		}
	}

	if successes != 1 {
		t.Fatalf("exactly one registration must win, got %d winners", successes)
	}
	if store.CredentialCount() != 1 {
		t.Fatalf("exactly one credential binding may exist, got %d", store.CredentialCount())
	}
	if owner := store.LookupAccount([]byte("cred-race")); owner != winner {
		t.Fatalf("stored owner %q does not match winner %q", owner, winner)
	}
}

func TestRegisterConcurrentSameAccountRetriesAllSucceed(t *testing.T) {
	// A lost response triggers simultaneous identical retries from one
	// account: every caller must get the same result, never an error or a
	// duplicate binding.
	const retries = 16
	challenge := []byte("challenge-retry-race")
	store := NewMemoryStore()
	store.SaveChallenge(challenge)
	verified := verifiedFixture(challenge, []byte("cred-retry"), []byte("pub-retry"))
	verifier := newFakeVerifier(verified)
	svc := NewService(verifier, store)

	verifier.armBarrier(retries)
	results := make([]Result, retries)
	errs := make([]error, retries)
	var wg sync.WaitGroup

	for i := 0; i < retries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Register(
				context.Background(),
				requestFixture(challenge, []byte("attestation"), nil, "alice"),
			)
		}(i)
	}

	verifier.barrierWG.Wait()
	verifier.releaseBarrier()
	wg.Wait()

	var created int
	for i, err := range errs {
		if err != nil {
			t.Fatalf("identical retry %d failed: %v", i, err)
		}
		if !results[i].Replayed {
			created++
		}
		if results[i].AccountID != "alice" || !bytes.Equal(results[i].CredentialID, []byte("cred-retry")) {
			t.Fatalf("retry %d returned an inconsistent result: %+v", i, results[i])
		}
	}
	if created != 1 {
		t.Fatalf("exactly one call may create the credential, got %d", created)
	}
	if store.CredentialCount() != 1 {
		t.Fatalf("identical retries produced %d bindings", store.CredentialCount())
	}
}

func TestRegisterDistinctCredentialsCoexist(t *testing.T) {
	store := NewMemoryStore()
	challenge := []byte("challenge-multi")
	store.SaveChallenge(challenge)
	verifier := newFakeVerifier(verifiedFixture(challenge, []byte("cred-a"), []byte("pub-a")))
	svc := NewService(verifier, store)

	if _, err := svc.Register(context.Background(), requestFixture(challenge, []byte("resp"), nil, "alice")); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// A second distinct credential under the same account gets its own
	// single-use challenge and occupies its own credential id.
	challenge2 := []byte("challenge-multi-2")
	store.SaveChallenge(challenge2)
	verifier.verified = verifiedFixture(challenge2, []byte("cred-b"), []byte("pub-b"))
	if _, err := svc.Register(context.Background(), requestFixture(challenge2, []byte("resp"), nil, "alice")); err != nil {
		t.Fatalf("second registration failed: %v", err)
	}

	if store.CredentialCount() != 2 {
		t.Fatalf("expected two distinct credentials, got %d", store.CredentialCount())
	}
	if store.LookupAccount([]byte("cred-a")) != "alice" || store.LookupAccount([]byte("cred-b")) != "alice" {
		t.Fatal("distinct credentials were not both bound to alice")
	}
}
