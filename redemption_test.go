package netsec_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0061"
)

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
	testKeyErr  error
)

func sharedTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		testKey, testKeyErr = rsa.GenerateKey(rand.Reader, 1024)
	})
	if testKeyErr != nil {
		t.Fatalf("generate RSA key: %v", testKeyErr)
	}
	return testKey
}

func testSigner(key *rsa.PrivateKey) netsec.PrivateKeyOperation {
	return func(blinded *big.Int) (*big.Int, error) {
		return new(big.Int).Exp(blinded, key.D, key.N), nil
	}
}

func ticketID(label string) netsec.TicketID {
	sum := sha256.Sum256([]byte("ticket/" + label))
	var id netsec.TicketID
	copy(id[:], sum[:netsec.TicketIDSize])
	return id
}

func claimDigest(label string) netsec.ClaimDigest {
	return netsec.ClaimDigestOf([]byte("claim/" + label))
}

type verifierEntry struct {
	claim   netsec.ClaimDigest
	expires time.Time
	max     int
	reject  bool
}

type testVerifier struct {
	mu      sync.RWMutex
	entries map[netsec.TicketID]verifierEntry
}

func newTestVerifier() *testVerifier {
	return &testVerifier{entries: make(map[netsec.TicketID]verifierEntry)}
}

func (v *testVerifier) set(id netsec.TicketID, claim netsec.ClaimDigest, expires time.Time, max int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[id] = verifierEntry{claim: claim, expires: expires, max: max}
}

func (v *testVerifier) VerifyTicket(_ context.Context, id netsec.TicketID, claim netsec.ClaimDigest, _ time.Time) (netsec.TicketPolicy, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	entry, ok := v.entries[id]
	if !ok || entry.reject || entry.claim != claim {
		return netsec.TicketPolicy{}, netsec.ErrTicketRejected
	}
	return netsec.TicketPolicy{
		ExpiresAt:      entry.expires,
		MaxRedemptions: entry.max,
	}, nil
}

func newIssuer(t *testing.T, key *rsa.PrivateKey, verifier netsec.TicketVerifier, store netsec.RedemptionStore, sign netsec.PrivateKeyOperation) *netsec.Issuer {
	t.Helper()
	if sign == nil {
		sign = testSigner(key)
	}
	issuer, err := netsec.NewIssuer(netsec.IssuerConfig{
		N:        key.N,
		E:        key.E,
		Sign:     sign,
		Verifier: verifier,
		Store:    store,
	})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return issuer
}

type blindRequest struct {
	message *big.Int
	factor  *big.Int
	blind   *big.Int
	request netsec.RedemptionRequest
}

func makeBlindRequest(t *testing.T, key *rsa.PublicKey, id netsec.TicketID, claim netsec.ClaimDigest, label, requestID string) blindRequest {
	t.Helper()
	messageSum := sha256.Sum256([]byte("message/" + label))
	message := new(big.Int).SetBytes(messageSum[:])
	for attempts := 0; attempts < 32; attempts++ {
		factor, err := netsec.NewBlindingFactor(key.N)
		if err != nil {
			t.Fatalf("NewBlindingFactor: %v", err)
		}
		blind, err := netsec.BlindMessage(message, factor, key.N, key.E)
		if err != nil {
			t.Fatalf("BlindMessage: %v", err)
		}
		if blind.Cmp(big.NewInt(1)) <= 0 || blind.Cmp(key.N) >= 0 {
			continue
		}
		if new(big.Int).GCD(nil, nil, blind, key.N).Cmp(big.NewInt(1)) != 0 {
			continue
		}
		return blindRequest{
			message: message,
			factor:  factor,
			blind:   blind,
			request: netsec.RedemptionRequest{
				TicketID:    id,
				ClaimDigest: claim,
				Blind:       blind,
				RequestID:   requestID,
			},
		}
	}
	t.Fatal("could not construct a usable blinded request")
	return blindRequest{}
}

func unblindAndVerify(t *testing.T, key *rsa.PublicKey, material blindRequest, signature *big.Int) *big.Int {
	t.Helper()
	unblinded, err := netsec.Unblind(signature, material.factor, key.N)
	if err != nil {
		t.Fatalf("Unblind: %v", err)
	}
	if !netsec.VerifyWithPublicKey(key, material.message, unblinded) {
		t.Fatalf("unblinded signature does not verify for message %s", material.message.Text(16))
	}
	return unblinded
}

func TestRedeemOnceAndIdempotent(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("once")
	claim := claimDigest("once")
	verifier.set(id, claim, time.Now().Add(time.Hour), 1)

	var signCalls atomic.Int64
	sign := func(blinded *big.Int) (*big.Int, error) {
		signCalls.Add(1)
		return new(big.Int).Exp(blinded, key.D, key.N), nil
	}
	issuer := newIssuer(t, key, verifier, store, sign)
	firstMaterial := makeBlindRequest(t, &key.PublicKey, id, claim, "once", "request-1")

	first, err := issuer.Redeem(context.Background(), firstMaterial.request)
	if err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}
	if first.Reused {
		t.Fatal("first redemption was reported as reused")
	}
	unblindAndVerify(t, &key.PublicKey, firstMaterial, first.Signature)

	again, err := issuer.Redeem(context.Background(), firstMaterial.request)
	if err != nil {
		t.Fatalf("duplicate Redeem: %v", err)
	}
	if !again.Reused {
		t.Fatal("duplicate redemption was not reported as reused")
	}
	if again.Signature.Cmp(first.Signature) != 0 {
		t.Fatal("duplicate request returned a different signature")
	}
	if _, err := issuer.RedeemBlind(context.Background(), firstMaterial.request); err != nil {
		t.Fatalf("RedeemBlind duplicate: %v", err)
	}
	if got := signCalls.Load(); got != 1 {
		t.Fatalf("signer calls = %d, want 1", got)
	}

	changedBlind := makeBlindRequest(t, &key.PublicKey, id, claim, "once-different-message", "request-1")
	if _, err := issuer.Redeem(context.Background(), changedBlind.request); !errors.Is(err, netsec.ErrBlindMismatch) {
		t.Fatalf("same request ID with a new blind: got %v, want ErrBlindMismatch", err)
	}
	if got := signCalls.Load(); got != 1 {
		t.Fatalf("signer calls after blind mismatch = %d, want 1", got)
	}

	secondRequest := makeBlindRequest(t, &key.PublicKey, id, claim, "once-second", "request-2")
	if _, err := issuer.Redeem(context.Background(), secondRequest.request); !errors.Is(err, netsec.ErrQuotaExceeded) {
		t.Fatalf("second request: got %v, want ErrQuotaExceeded", err)
	}
	if stats := store.Stats(); stats.Reservations != 1 || stats.Signatures != 1 {
		t.Fatalf("unexpected store stats: %+v", stats)
	}
}

func TestConcurrentRetriesConsumeOneSlot(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("concurrent-retry")
	claim := claimDigest("concurrent-retry")
	verifier.set(id, claim, time.Now().Add(time.Hour), 2)

	var signCalls atomic.Int64
	sign := func(blinded *big.Int) (*big.Int, error) {
		signCalls.Add(1)
		return new(big.Int).Exp(blinded, key.D, key.N), nil
	}
	issuer := newIssuer(t, key, verifier, store, sign)
	material := makeBlindRequest(t, &key.PublicKey, id, claim, "concurrent-retry", "shared-request")

	const retries = 48
	type outcome struct {
		result netsec.RedemptionResult
		err    error
	}
	outcomes := make(chan outcome, retries)
	start := make(chan struct{})
	for index := 0; index < retries; index++ {
		go func() {
			<-start
			result, err := issuer.Redeem(context.Background(), material.request)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)

	var firstSignature *big.Int
	for index := 0; index < retries; index++ {
		out := <-outcomes
		if out.err != nil {
			t.Fatalf("concurrent retry: %v", out.err)
		}
		if firstSignature == nil {
			firstSignature = out.result.Signature
		} else if out.result.Signature.Cmp(firstSignature) != 0 {
			t.Fatal("concurrent retries observed different signatures")
		}
		unblindAndVerify(t, &key.PublicKey, material, out.result.Signature)
	}
	if got := signCalls.Load(); got != 1 {
		t.Fatalf("signer calls = %d, want 1; concurrent retries must share the reservation", got)
	}
	if stats := store.Stats(); stats.Reservations != 1 || stats.Signatures != 1 {
		t.Fatalf("unexpected store stats: %+v", stats)
	}
}

func TestConcurrentDistinctRequestsRespectQuota(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("concurrent-quota")
	claim := claimDigest("concurrent-quota")
	const max = 16
	verifier.set(id, claim, time.Now().Add(time.Hour), max)
	issuer := newIssuer(t, key, verifier, store, nil)

	materials := make([]blindRequest, max)
	for index := 0; index < max; index++ {
		materials[index] = makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("quota-message-%d", index),
			fmt.Sprintf("quota-request-%d", index),
		)
	}

	type outcome struct {
		index  int
		result netsec.RedemptionResult
		err    error
	}
	outcomes := make(chan outcome, max*4)
	start := make(chan struct{})
	for index := 0; index < max; index++ {
		for retry := 0; retry < 4; retry++ {
			go func(index int) {
				<-start
				result, err := issuer.Redeem(context.Background(), materials[index].request)
				outcomes <- outcome{index: index, result: result, err: err}
			}(index)
		}
	}
	close(start)

	seenMessages := make(map[string]struct{})
	for index := 0; index < max*4; index++ {
		out := <-outcomes
		if out.err != nil {
			t.Fatalf("request %d: %v", out.index, out.err)
		}
		message := unblindAndVerify(t, &key.PublicKey, materials[out.index], out.result.Signature)
		seenMessages[message.Text(16)] = struct{}{}
	}
	if len(seenMessages) != max {
		t.Fatalf("distinct unblinded messages = %d, want %d", len(seenMessages), max)
	}

	for index := max; index < max+8; index++ {
		material := makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("quota-overflow-message-%d", index),
			fmt.Sprintf("quota-overflow-request-%d", index),
		)
		if _, err := issuer.Redeem(context.Background(), material.request); !errors.Is(err, netsec.ErrQuotaExceeded) {
			t.Fatalf("overflow request %d: got %v, want ErrQuotaExceeded", index, err)
		}
	}
	if stats := store.Stats(); stats.Reservations != max || stats.Signatures != max {
		t.Fatalf("unexpected store stats: %+v", stats)
	}
}

type faultPoint int

const (
	faultReserveBefore faultPoint = iota
	faultReserveAfter
	faultCommitBefore
	faultCommitAfter
)

var errInjectedFault = errors.New("injected persistence fault")

type faultStore struct {
	inner *netsec.MemoryStore
	point faultPoint

	mu    sync.Mutex
	armed bool
}

func newFaultStore(point faultPoint) *faultStore {
	return &faultStore{
		inner: netsec.NewMemoryStore(),
		point: point,
		armed: true,
	}
}

func (s *faultStore) fire(point faultPoint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.armed || s.point != point {
		return false
	}
	s.armed = false
	return true
}

func (s *faultStore) Reserve(ctx context.Context, reservation netsec.Reservation) (netsec.ReserveResult, error) {
	if s.fire(faultReserveBefore) {
		return netsec.ReserveResult{}, errInjectedFault
	}
	result, err := s.inner.Reserve(ctx, reservation)
	if s.fire(faultReserveAfter) {
		return result, errInjectedFault
	}
	return result, err
}

func (s *faultStore) CommitSignature(ctx context.Context, completion netsec.Completion) error {
	if s.fire(faultCommitBefore) {
		return errInjectedFault
	}
	err := s.inner.CommitSignature(ctx, completion)
	if s.fire(faultCommitAfter) {
		return errInjectedFault
	}
	return err
}

func (s *faultStore) Lookup(ctx context.Context, id netsec.TicketID, requestID string) (netsec.Record, bool, error) {
	return s.inner.Lookup(ctx, id, requestID)
}

func TestFaultAtEveryPersistencePoint(t *testing.T) {
	key := sharedTestKey(t)
	points := []faultPoint{
		faultReserveBefore,
		faultReserveAfter,
		faultCommitBefore,
		faultCommitAfter,
	}
	for _, point := range points {
		point := point
		t.Run(fmt.Sprintf("point-%d", point), func(t *testing.T) {
			verifier := newTestVerifier()
			store := newFaultStore(point)
			id := ticketID(fmt.Sprintf("fault-%d", point))
			claim := claimDigest(fmt.Sprintf("fault-%d", point))
			verifier.set(id, claim, time.Now().Add(time.Hour), 1)

			var signCalls atomic.Int64
			sign := func(blinded *big.Int) (*big.Int, error) {
				signCalls.Add(1)
				return new(big.Int).Exp(blinded, key.D, key.N), nil
			}
			material := makeBlindRequest(
				t,
				&key.PublicKey,
				id,
				claim,
				fmt.Sprintf("fault-message-%d", point),
				"fault-request",
			)

			firstIssuer := newIssuer(t, key, verifier, store, sign)
			first, err := firstIssuer.Redeem(context.Background(), material.request)
			if !errors.Is(err, errInjectedFault) {
				t.Fatalf("first attempt: got %v, want injected fault", err)
			}
			if first.Signature != nil {
				t.Fatal("failed persistence attempt leaked a signature")
			}

			recoveredIssuer := newIssuer(t, key, verifier, store, sign)
			recovered, err := recoveredIssuer.Redeem(context.Background(), material.request)
			if err != nil {
				t.Fatalf("recovery attempt: %v", err)
			}
			unblindAndVerify(t, &key.PublicKey, material, recovered.Signature)

			other := makeBlindRequest(
				t,
				&key.PublicKey,
				id,
				claim,
				fmt.Sprintf("fault-other-%d", point),
				"fault-other-request",
			)
			if _, err := recoveredIssuer.Redeem(context.Background(), other.request); !errors.Is(err, netsec.ErrQuotaExceeded) {
				t.Fatalf("post-failure quota: got %v, want ErrQuotaExceeded", err)
			}

			stats := store.inner.Stats()
			if stats.Reservations != 1 || stats.Signatures != 1 {
				t.Fatalf("unexpected inner store stats: %+v", stats)
			}
			if calls := signCalls.Load(); calls < 1 || calls > 2 {
				t.Fatalf("signer calls = %d, want 1 or 2", calls)
			}
		})
	}
}

func TestSignerFailureRetainsReservationAndNeverLeaksPartialResult(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("signer-failure")
	claim := claimDigest("signer-failure")
	verifier.set(id, claim, time.Now().Add(time.Hour), 1)

	var calls atomic.Int64
	errHSM := errors.New("HSM unavailable")
	partial := new(big.Int).SetInt64(1234567)
	sign := func(blinded *big.Int) (*big.Int, error) {
		if calls.Add(1) == 1 {
			return partial, errHSM
		}
		return new(big.Int).Exp(blinded, key.D, key.N), nil
	}
	issuer := newIssuer(t, key, verifier, store, sign)
	material := makeBlindRequest(t, &key.PublicKey, id, claim, "signer-failure", "signer-request")

	result, err := issuer.Redeem(context.Background(), material.request)
	if !errors.Is(err, netsec.ErrSignerFailed) || !errors.Is(err, errHSM) {
		t.Fatalf("first redemption: got %v, want signer failure", err)
	}
	if result.Signature != nil {
		t.Fatal("signer failure leaked a partial signature")
	}
	record, found, err := issuer.Lookup(context.Background(), id, "signer-request")
	if err != nil || !found {
		t.Fatalf("reservation lookup: found=%v err=%v", found, err)
	}
	if record.Signature != nil {
		t.Fatal("partial signature was persisted")
	}

	differentBlind := makeBlindRequest(t, &key.PublicKey, id, claim, "signer-failure-other", "signer-request")
	if _, err := issuer.Redeem(context.Background(), differentBlind.request); !errors.Is(err, netsec.ErrBlindMismatch) {
		t.Fatalf("changed blind after signer failure: got %v, want ErrBlindMismatch", err)
	}

	other := makeBlindRequest(t, &key.PublicKey, id, claim, "signer-failure-quota", "signer-other")
	if _, err := issuer.Redeem(context.Background(), other.request); !errors.Is(err, netsec.ErrQuotaExceeded) {
		t.Fatalf("new request after signer failure: got %v, want ErrQuotaExceeded", err)
	}

	recovered, err := issuer.Redeem(context.Background(), material.request)
	if err != nil {
		t.Fatalf("recover after signer failure: %v", err)
	}
	unblindAndVerify(t, &key.PublicKey, material, recovered.Signature)
	if calls.Load() != 2 {
		t.Fatalf("signer calls = %d, want 2", calls.Load())
	}
}

func TestInvalidSignerOutputIsNotCommitted(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("invalid-signer-output")
	claim := claimDigest("invalid-signer-output")
	verifier.set(id, claim, time.Now().Add(time.Hour), 1)

	var calls atomic.Int64
	sign := func(blinded *big.Int) (*big.Int, error) {
		if calls.Add(1) == 1 {
			return new(big.Int).Add(blinded, big.NewInt(1)), nil
		}
		return new(big.Int).Exp(blinded, key.D, key.N), nil
	}
	issuer := newIssuer(t, key, verifier, store, sign)
	material := makeBlindRequest(t, &key.PublicKey, id, claim, "invalid-signer-output", "invalid-output-request")

	if _, err := issuer.Redeem(context.Background(), material.request); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("invalid signer output: got %v, want ErrInvalidSignature", err)
	}
	record, found, err := issuer.Lookup(context.Background(), id, "invalid-output-request")
	if err != nil || !found || record.Signature != nil {
		t.Fatalf("invalid output must leave an unsigned reservation: found=%v record=%+v err=%v", found, record, err)
	}
	recovered, err := issuer.Redeem(context.Background(), material.request)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	unblindAndVerify(t, &key.PublicKey, material, recovered.Signature)
}

func TestPolicyExpiryAndBlindValidation(t *testing.T) {
	key := sharedTestKey(t)
	ctx := context.Background()

	t.Run("expired", func(t *testing.T) {
		verifier := newTestVerifier()
		store := netsec.NewMemoryStore()
		id := ticketID("expired")
		claim := claimDigest("expired")
		verifier.set(id, claim, time.Now().Add(-time.Minute), 1)
		issuer := newIssuer(t, key, verifier, store, nil)
		material := makeBlindRequest(t, &key.PublicKey, id, claim, "expired", "expired-request")
		if _, err := issuer.Redeem(ctx, material.request); !errors.Is(err, netsec.ErrTicketExpired) {
			t.Fatalf("got %v, want ErrTicketExpired", err)
		}
	})

	for _, max := range []int{0, 17, -1} {
		max := max
		t.Run(fmt.Sprintf("max-%d", max), func(t *testing.T) {
			verifier := newTestVerifier()
			store := netsec.NewMemoryStore()
			id := ticketID(fmt.Sprintf("max-%d", max))
			claim := claimDigest(fmt.Sprintf("max-%d", max))
			verifier.set(id, claim, time.Now().Add(time.Hour), max)
			issuer := newIssuer(t, key, verifier, store, nil)
			material := makeBlindRequest(t, &key.PublicKey, id, claim, "max", "max-request")
			if _, err := issuer.Redeem(ctx, material.request); !errors.Is(err, netsec.ErrInvalidPolicy) {
				t.Fatalf("got %v, want ErrInvalidPolicy", err)
			}
		})
	}

	t.Run("claim-binding", func(t *testing.T) {
		verifier := newTestVerifier()
		store := netsec.NewMemoryStore()
		id := ticketID("claim-binding")
		verifier.set(id, claimDigest("bound"), time.Now().Add(time.Hour), 1)
		issuer := newIssuer(t, key, verifier, store, nil)
		material := makeBlindRequest(t, &key.PublicKey, id, claimDigest("not-bound"), "claim-binding", "claim-binding-request")
		if _, err := issuer.Redeem(ctx, material.request); !errors.Is(err, netsec.ErrTicketRejected) {
			t.Fatalf("got %v, want ErrTicketRejected", err)
		}
	})

	t.Run("blind-values", func(t *testing.T) {
		verifier := newTestVerifier()
		store := netsec.NewMemoryStore()
		id := ticketID("blind-values")
		claim := claimDigest("blind-values")
		verifier.set(id, claim, time.Now().Add(time.Hour), 1)
		var calls atomic.Int64
		sign := func(blinded *big.Int) (*big.Int, error) {
			calls.Add(1)
			return new(big.Int).Exp(blinded, key.D, key.N), nil
		}
		issuer := newIssuer(t, key, verifier, store, sign)
		valid := makeBlindRequest(t, &key.PublicKey, id, claim, "blind-values", "blind-values-request")
		primeFactor := new(big.Int).Set(key.Primes[0])

		cases := []struct {
			name  string
			blind *big.Int
			want  error
		}{
			{name: "nil", blind: nil, want: netsec.ErrInvalidBlind},
			{name: "one", blind: big.NewInt(1), want: netsec.ErrInvalidBlind},
			{name: "modulus", blind: new(big.Int).Set(key.N), want: netsec.ErrInvalidBlind},
			{name: "above-modulus", blind: new(big.Int).Add(key.N, big.NewInt(1)), want: netsec.ErrInvalidBlind},
			{name: "not-coprime", blind: primeFactor, want: netsec.ErrBlindNotCoprime},
		}
		for _, testCase := range cases {
			request := valid.request
			request.Blind = testCase.blind
			request.RequestID = "invalid-blind-" + testCase.name
			if _, err := issuer.Redeem(ctx, request); !errors.Is(err, testCase.want) {
				t.Fatalf("%s: got %v, want %v", testCase.name, err, testCase.want)
			}
		}
		if calls.Load() != 0 {
			t.Fatalf("signer called %d times for invalid blindness values", calls.Load())
		}
	})
}

func TestTicketCopiesAndRequestConflicts(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("copies")
	claim := claimDigest("copies")
	const max = 4
	verifier.set(id, claim, time.Now().Add(time.Hour), max)
	issuer := newIssuer(t, key, verifier, store, nil)

	var successes []blindRequest
	var signatures []*big.Int
	for index := 0; index < max*3; index++ {
		material := makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("copy-message-%d", index),
			fmt.Sprintf("copy-request-%d", index),
		)
		result, err := issuer.Redeem(context.Background(), material.request)
		if err == nil {
			successes = append(successes, material)
			signatures = append(signatures, result.Signature)
			continue
		}
		if !errors.Is(err, netsec.ErrQuotaExceeded) {
			t.Fatalf("copy request %d: unexpected error %v", index, err)
		}
	}
	if len(successes) != max {
		t.Fatalf("successful ticket copies = %d, want %d", len(successes), max)
	}
	distinct := make(map[string]struct{})
	for index := range successes {
		message := unblindAndVerify(t, &key.PublicKey, successes[index], signatures[index])
		distinct[message.Text(16)] = struct{}{}
	}
	if len(distinct) != max {
		t.Fatalf("distinct unblinded messages from copies = %d, want %d", len(distinct), max)
	}

	first := successes[0]
	replayed := first.request
	replayed.ClaimDigest = claimDigest("copies-other-claim")
	verifier.set(id, replayed.ClaimDigest, time.Now().Add(time.Hour), max)
	if _, err := issuer.Redeem(context.Background(), replayed); !errors.Is(err, netsec.ErrRequestConflict) {
		t.Fatalf("same request ID with another claim: got %v, want ErrRequestConflict", err)
	}
}

func TestPolicyCannotExpandAfterFirstReservation(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("policy-mismatch")
	claim := claimDigest("policy-mismatch")
	verifier.set(id, claim, time.Now().Add(time.Hour), 1)
	issuer := newIssuer(t, key, verifier, store, nil)

	first := makeBlindRequest(t, &key.PublicKey, id, claim, "policy-first", "policy-first-request")
	if _, err := issuer.Redeem(context.Background(), first.request); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	verifier.set(id, claim, time.Now().Add(time.Hour), 2)
	second := makeBlindRequest(t, &key.PublicKey, id, claim, "policy-second", "policy-second-request")
	if _, err := issuer.Redeem(context.Background(), second.request); !errors.Is(err, netsec.ErrTicketPolicyMismatch) {
		t.Fatalf("policy expansion: got %v, want ErrTicketPolicyMismatch", err)
	}
}

func TestCrossIssuerConcurrencySharesQuota(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("cross-issuer")
	claim := claimDigest("cross-issuer")
	const max = 8
	verifier.set(id, claim, time.Now().Add(time.Hour), max)

	const issuerCount = 4
	issuers := make([]*netsec.Issuer, issuerCount)
	for index := range issuers {
		issuers[index] = newIssuer(t, key, verifier, store, nil)
	}

	const attempts = 64
	materials := make([]blindRequest, attempts)
	for index := range materials {
		materials[index] = makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("cross-message-%d", index),
			fmt.Sprintf("cross-request-%d", index),
		)
	}

	type outcome struct {
		index  int
		result netsec.RedemptionResult
		err    error
	}
	outcomes := make(chan outcome, attempts)
	start := make(chan struct{})
	for index := range materials {
		go func(index int) {
			<-start
			issuer := issuers[index%issuerCount]
			result, err := issuer.Redeem(context.Background(), materials[index].request)
			outcomes <- outcome{index: index, result: result, err: err}
		}(index)
	}
	close(start)

	distinct := make(map[string]struct{})
	successes := 0
	for index := 0; index < attempts; index++ {
		out := <-outcomes
		if out.err != nil {
			if !errors.Is(out.err, netsec.ErrQuotaExceeded) {
				t.Fatalf("attempt %d: unexpected error %v", out.index, out.err)
			}
			continue
		}
		successes++
		message := unblindAndVerify(t, &key.PublicKey, materials[out.index], out.result.Signature)
		distinct[message.Text(16)] = struct{}{}
	}
	if successes != max {
		t.Fatalf("successful cross-issuer redemptions = %d, want %d", successes, max)
	}
	if len(distinct) != max {
		t.Fatalf("distinct cross-issuer messages = %d, want %d", len(distinct), max)
	}
}

func TestSameRequestAcrossIssuersReturnsOneSignature(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("cross-issuer-same")
	claim := claimDigest("cross-issuer-same")
	verifier.set(id, claim, time.Now().Add(time.Hour), 1)

	const issuerCount = 4
	issuers := make([]*netsec.Issuer, issuerCount)
	for index := range issuers {
		issuers[index] = newIssuer(t, key, verifier, store, nil)
	}
	material := makeBlindRequest(t, &key.PublicKey, id, claim, "cross-issuer-same", "cross-issuer-same-request")

	const attempts = 40
	signatures := make(chan *big.Int, attempts)
	errorsCh := make(chan error, attempts)
	start := make(chan struct{})
	for index := 0; index < attempts; index++ {
		go func(index int) {
			<-start
			result, err := issuers[index%issuerCount].Redeem(context.Background(), material.request)
			if err != nil {
				errorsCh <- err
				return
			}
			signatures <- result.Signature
		}(index)
	}
	close(start)

	var first *big.Int
	for index := 0; index < attempts; index++ {
		select {
		case err := <-errorsCh:
			t.Fatalf("cross-issuer retry: %v", err)
		case signature := <-signatures:
			if first == nil {
				first = signature
			} else if first.Cmp(signature) != 0 {
				t.Fatal("cross-issuer retries returned different signatures")
			}
		}
	}
	unblindAndVerify(t, &key.PublicKey, material, first)
	if stats := store.Stats(); stats.Reservations != 1 || stats.Signatures != 1 {
		t.Fatalf("unexpected store stats: %+v", stats)
	}
}

func TestMixedRetriesCopiesAndBlindSwapsStayWithinQuota(t *testing.T) {
	key := sharedTestKey(t)
	verifier := newTestVerifier()
	store := netsec.NewMemoryStore()
	id := ticketID("mixed-adversary")
	claim := claimDigest("mixed-adversary")
	const max = 6
	verifier.set(id, claim, time.Now().Add(time.Hour), max)

	const issuerCount = 3
	issuers := make([]*netsec.Issuer, issuerCount)
	for index := range issuers {
		issuers[index] = newIssuer(t, key, verifier, store, nil)
	}

	const candidateCount = 24
	materials := make([]blindRequest, candidateCount)
	swapped := make([]blindRequest, candidateCount)
	for index := 0; index < candidateCount; index++ {
		requestID := fmt.Sprintf("mixed-request-%d", index)
		materials[index] = makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("mixed-message-%d", index),
			requestID,
		)
		swapped[index] = makeBlindRequest(
			t,
			&key.PublicKey,
			id,
			claim,
			fmt.Sprintf("mixed-swapped-message-%d", index),
			requestID,
		)
	}

	type outcome struct {
		material blindRequest
		result   netsec.RedemptionResult
		err      error
	}
	outcomes := make(chan outcome, candidateCount*8)
	start := make(chan struct{})
	for index := 0; index < candidateCount; index++ {
		for copyIndex := 0; copyIndex < 3; copyIndex++ {
			go func(index, copyIndex int) {
				<-start
				request := materials[index].request
				request.RequestID = fmt.Sprintf("%s-copy-%d", request.RequestID, copyIndex)
				result, err := issuers[(index+copyIndex)%issuerCount].Redeem(context.Background(), request)
				outcomes <- outcome{material: materials[index], result: result, err: err}
			}(index, copyIndex)
		}
		go func(index int) {
			<-start
			result, err := issuers[index%issuerCount].Redeem(context.Background(), materials[index].request)
			outcomes <- outcome{material: materials[index], result: result, err: err}
		}(index)
		go func(index int) {
			<-start
			result, err := issuers[(index+1)%issuerCount].Redeem(context.Background(), swapped[index].request)
			outcomes <- outcome{material: swapped[index], result: result, err: err}
		}(index)
	}
	close(start)

	distinct := make(map[string]struct{})
	for index := 0; index < candidateCount*5; index++ {
		out := <-outcomes
		if out.err != nil {
			if !errors.Is(out.err, netsec.ErrQuotaExceeded) && !errors.Is(out.err, netsec.ErrBlindMismatch) {
				t.Fatalf("mixed request: unexpected error %v", out.err)
			}
			continue
		}
		message := unblindAndVerify(t, &key.PublicKey, out.material, out.result.Signature)
		distinct[message.Text(16)] = struct{}{}
	}
	if len(distinct) > max {
		t.Fatalf("successfully unblinded distinct messages = %d, exceeds m=%d", len(distinct), max)
	}
	if stats := store.Stats(); stats.Reservations > max || stats.Signatures > max {
		t.Fatalf("store exceeded quota: %+v", stats)
	}
}

func TestBlindConstructionAndUnblinding(t *testing.T) {
	key := sharedTestKey(t)
	message := new(big.Int).SetBytes(bytes.Repeat([]byte{0x42}, 24))
	factor, err := netsec.NewBlindingFactor(key.N)
	if err != nil {
		t.Fatalf("NewBlindingFactor: %v", err)
	}
	if factor.Cmp(big.NewInt(1)) <= 0 || factor.Cmp(key.N) >= 0 {
		t.Fatalf("factor outside (1, N): %s", factor.Text(16))
	}
	if new(big.Int).GCD(nil, nil, factor, key.N).Cmp(big.NewInt(1)) != 0 {
		t.Fatal("factor is not invertible")
	}

	blind, err := netsec.BlindMessage(message, factor, key.N, key.E)
	if err != nil {
		t.Fatalf("BlindMessage: %v", err)
	}
	expected := new(big.Int).Exp(factor, big.NewInt(int64(key.E)), key.N)
	expected.Mul(expected, message)
	expected.Mod(expected, key.N)
	if blind.Cmp(expected) != 0 {
		t.Fatal("BlindMessage did not compute m*r^e mod N")
	}

	signature := new(big.Int).Exp(blind, key.D, key.N)
	unblinded, err := netsec.Unblind(signature, factor, key.N)
	if err != nil {
		t.Fatalf("Unblind: %v", err)
	}
	if !netsec.VerifyWithPublicKey(&key.PublicKey, message, unblinded) {
		t.Fatal("unblinded signature failed verification")
	}
}
