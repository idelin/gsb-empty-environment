package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"
)

func TestTwoPartyRealSignature(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	sessionID := []byte("two-party-session")
	message := []byte("transfer 7 units")

	party1 := newTestParty(t, key1, roster, sessionID, message)
	party2 := newTestParty(t, key2, roster, sessionID, message)

	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	commitment1, err := party1.signer.Commit(t.Context(), party1.req)
	if err != nil {
		t.Fatalf("party 1 commit: %v", err)
	}
	commitment2, err := party2.signer.Commit(t.Context(), party2.req)
	if err != nil {
		t.Fatalf("party 2 commit: %v", err)
	}
	if err := session.SubmitCommitment(party1.public, commitment1); err != nil {
		t.Fatalf("submit commitment 1: %v", err)
	}
	if session.Phase() != PhaseCommitments {
		t.Fatalf("phase advanced before all commitments: %s", session.Phase())
	}
	if err := session.SubmitCommitment(party2.public, commitment2); err != nil {
		t.Fatalf("submit commitment 2: %v", err)
	}
	if session.Phase() != PhaseReveals {
		t.Fatalf("phase = %s, want reveals", session.Phase())
	}

	reveal1, err := party1.signer.Reveal(t.Context(), party1.req)
	if err != nil {
		t.Fatalf("party 1 reveal: %v", err)
	}
	reveal2, err := party2.signer.Reveal(t.Context(), party2.req)
	if err != nil {
		t.Fatalf("party 2 reveal: %v", err)
	}
	if err := session.SubmitReveal(party1.public, reveal1); err != nil {
		t.Fatalf("submit reveal 1: %v", err)
	}
	if session.Challenge() != nil {
		t.Fatal("challenge was computed before all reveals")
	}
	if err := session.SubmitReveal(party2.public, reveal2); err != nil {
		t.Fatalf("submit reveal 2: %v", err)
	}
	if session.Phase() != PhasePartials {
		t.Fatalf("phase = %s, want partials", session.Phase())
	}

	aggregateR := session.AggregateNonce()
	challenge := session.Challenge()
	recomputed, err := ComputeChallenge(sessionID, message, roster, aggregateR)
	if err != nil {
		t.Fatalf("recompute challenge: %v", err)
	}
	if !bytes.Equal(challenge, recomputed) {
		t.Fatal("coordinator and signer challenge derivations differ")
	}

	partial1, err := party1.signer.Partial(t.Context(), party1.req, aggregateR)
	if err != nil {
		t.Fatalf("party 1 partial: %v", err)
	}
	partial2, err := party2.signer.Partial(t.Context(), party2.req, aggregateR)
	if err != nil {
		t.Fatalf("party 2 partial: %v", err)
	}
	if err := session.SubmitPartial(party1.public, partial1); err != nil {
		t.Fatalf("submit partial 1: %v", err)
	}
	if err := session.SubmitPartial(party2.public, partial2); err != nil {
		t.Fatalf("submit partial 2: %v", err)
	}
	if session.Phase() != PhaseComplete {
		t.Fatalf("phase = %s, want complete", session.Phase())
	}

	signature, err := session.AggregateSignature()
	if err != nil {
		t.Fatalf("aggregate signature: %v", err)
	}
	if err := VerifySignature(signature, sessionID, message, roster); err != nil {
		t.Fatalf("verify aggregate signature: %v", err)
	}

	// Independent check of zG = R + eP.
	z := new(big.Int).SetBytes(signature.Z)
	e := new(big.Int).SetBytes(signature.E)
	rPoint, err := parseCompressedPoint(signature.R)
	if err != nil {
		t.Fatalf("parse R: %v", err)
	}
	right := addPoints(rPoint, scalarMultPoint(roster.aggregate, e))
	if !scalarBaseMult(z).equal(right) {
		t.Fatal("aggregate Schnorr equation failed")
	}

	// Signer retries are idempotent.
	partial1Again, err := party1.signer.Partial(t.Context(), party1.req, aggregateR)
	if err != nil {
		t.Fatalf("retry partial 1: %v", err)
	}
	if !bytes.Equal(partial1, partial1Again) {
		t.Fatal("retried partial signature changed")
	}

	// The same aggregate signature must not verify under a changed binding.
	if err := VerifySignature(signature, sessionID, []byte("transfer 8 units"), roster); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("changed message verification error = %v, want ErrInvalidSignature", err)
	}
	if err := VerifySignature(signature, []byte("other-session"), message, roster); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("changed session verification error = %v, want ErrInvalidSignature", err)
	}
	if err := session.Abort("late abort"); !errors.Is(err, ErrSessionComplete) {
		t.Fatalf("abort after completion error = %v, want ErrSessionComplete", err)
	}
}

func TestRosterCanonicalOrderingBoundsAndCoefficients(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	key3 := generateTestKey(t)
	roster := mustRoster(t, key1, key2, key3)

	keys := roster.EncodedPublicKeys()
	if len(keys) != 3 {
		t.Fatalf("roster size = %d, want 3", len(keys))
	}
	for i := 1; i < len(keys); i++ {
		if bytes.Compare(keys[i-1], keys[i]) >= 0 {
			t.Fatal("roster public keys are not strictly sorted by compressed encoding")
		}
	}

	reordered := mustRoster(t, key3, key1, key2)
	if !bytes.Equal(roster.Bytes(), reordered.Bytes()) {
		t.Fatal("roster encoding depends on input order")
	}
	for _, key := range keys {
		coefficient, ok := roster.Coefficient(key)
		if !ok {
			t.Fatal("missing coefficient")
		}
		if coefficient.Sign() == 0 || coefficient.Cmp(groupN) >= 0 {
			t.Fatal("coefficient is outside the nonzero group-order range")
		}
	}
	if roster.AggregatePublicKey() == nil || len(roster.AggregatePublicKeyBytes()) != CompressedPointSize {
		t.Fatal("invalid aggregate public key")
	}

	if _, err := NewRoster([]*ecdsa.PublicKey{&key1.PublicKey}); !errors.Is(err, ErrRosterSize) {
		t.Fatalf("one-member roster error = %v, want ErrRosterSize", err)
	}
	if _, err := NewRoster([]*ecdsa.PublicKey{&key1.PublicKey, &key1.PublicKey}); !errors.Is(err, ErrDuplicatePublicKey) {
		t.Fatalf("duplicate roster error = %v, want ErrDuplicatePublicKey", err)
	}
	if _, err := ParseRoster([][]byte{make([]byte, CompressedPointSize), keys[0]}); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("invalid public key error = %v, want ErrInvalidPublicKey", err)
	}

	tooMany := make([]*ecdsa.PublicKey, MaxRosterSize+1)
	for i := range tooMany {
		tooMany[i] = &generateTestKey(t).PublicKey
	}
	if _, err := NewRoster(tooMany[:MaxRosterSize]); err != nil {
		t.Fatalf("maximum-size roster rejected: %v", err)
	}
	if _, err := NewRoster(tooMany); !errors.Is(err, ErrRosterSize) {
		t.Fatalf("oversized roster error = %v, want ErrRosterSize", err)
	}
}

func TestSessionRejectsZeroAggregatePublicKey(t *testing.T) {
	malformed := &Roster{aggregate: infinity()}
	if _, err := NewSession([]byte("id"), []byte("message"), malformed); !errors.Is(err, ErrZeroAggregatePublicKey) {
		t.Fatalf("zero aggregate public key error = %v, want ErrZeroAggregatePublicKey", err)
	}
}

func TestMaximumSizeRosterSigns(t *testing.T) {
	keys := make([]*ecdsa.PrivateKey, MaxRosterSize)
	publicKeys := make([]*ecdsa.PublicKey, MaxRosterSize)
	for i := range keys {
		keys[i] = generateTestKey(t)
		publicKeys[i] = &keys[i].PublicKey
	}
	roster, err := NewRoster(publicKeys)
	if err != nil {
		t.Fatalf("max roster: %v", err)
	}
	sessionID := []byte("max-roster")
	message := []byte("message")
	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	parties := make([]*testParty, len(keys))
	for i, key := range keys {
		parties[i] = newTestParty(t, key, roster, sessionID, message)
		commitment, err := parties[i].signer.Commit(t.Context(), parties[i].req)
		if err != nil {
			t.Fatalf("party %d commit: %v", i, err)
		}
		if err := session.SubmitCommitment(parties[i].public, commitment); err != nil {
			t.Fatalf("party %d submit commitment: %v", i, err)
		}
	}
	for i, party := range parties {
		reveal, err := party.signer.Reveal(t.Context(), party.req)
		if err != nil {
			t.Fatalf("party %d reveal: %v", i, err)
		}
		if err := session.SubmitReveal(party.public, reveal); err != nil {
			t.Fatalf("party %d submit reveal: %v", i, err)
		}
	}
	aggregateR := session.AggregateNonce()
	for i, party := range parties {
		partial, err := party.signer.Partial(t.Context(), party.req, aggregateR)
		if err != nil {
			t.Fatalf("party %d partial: %v", i, err)
		}
		if err := session.SubmitPartial(party.public, partial); err != nil {
			t.Fatalf("party %d submit partial: %v", i, err)
		}
	}
	signature, err := session.AggregateSignature()
	if err != nil {
		t.Fatalf("aggregate signature: %v", err)
	}
	if err := VerifySignature(signature, sessionID, message, roster); err != nil {
		t.Fatalf("verify max roster signature: %v", err)
	}
}

func TestCommitmentsLockedBeforeReveal(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	sessionID := []byte("commitment-lock")
	message := []byte("m")
	party1 := newTestParty(t, key1, roster, sessionID, message)
	party2 := newTestParty(t, key2, roster, sessionID, message)

	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	commitment1, err := party1.signer.Commit(t.Context(), party1.req)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := session.SubmitCommitment(party1.public, commitment1); err != nil {
		t.Fatalf("submit commitment: %v", err)
	}
	reveal1, err := party1.signer.Reveal(t.Context(), party1.req)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if err := session.SubmitReveal(party1.public, reveal1); !errors.Is(err, ErrSessionPhase) {
		t.Fatalf("early reveal error = %v, want ErrSessionPhase", err)
	}

	commitment2, err := party2.signer.Commit(t.Context(), party2.req)
	if err != nil {
		t.Fatalf("commit 2: %v", err)
	}
	if err := session.SubmitCommitment(party2.public, commitment2); err != nil {
		t.Fatalf("submit commitment 2: %v", err)
	}
	wrongReveal := differentPoint(t, reveal1)
	if err := session.SubmitReveal(party1.public, wrongReveal); !errors.Is(err, ErrCommitmentMismatch) {
		t.Fatalf("wrong reveal error = %v, want ErrCommitmentMismatch", err)
	}
	if err := session.SubmitReveal(party1.public, reveal1); err != nil {
		t.Fatalf("valid reveal: %v", err)
	}
	if err := session.SubmitReveal(party1.public, reveal1); err != nil {
		t.Fatalf("idempotent reveal: %v", err)
	}
	if err := session.SubmitReveal(party1.public, wrongReveal); !errors.Is(err, ErrConflictingSubmission) {
		t.Fatalf("conflicting reveal error = %v, want ErrConflictingSubmission", err)
	}
}

func TestSessionRejectsInvalidPartial(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	sessionID := []byte("bad-partial")
	message := []byte("m")
	party1 := newTestParty(t, key1, roster, sessionID, message)
	party2 := newTestParty(t, key2, roster, sessionID, message)
	session, _ := NewSession(sessionID, message, roster)

	commitment1, _ := party1.signer.Commit(t.Context(), party1.req)
	commitment2, _ := party2.signer.Commit(t.Context(), party2.req)
	_ = session.SubmitCommitment(party1.public, commitment1)
	_ = session.SubmitCommitment(party2.public, commitment2)
	reveal1, _ := party1.signer.Reveal(t.Context(), party1.req)
	reveal2, _ := party2.signer.Reveal(t.Context(), party2.req)
	_ = session.SubmitReveal(party1.public, reveal1)
	_ = session.SubmitReveal(party2.public, reveal2)

	partial1, err := party1.signer.Partial(t.Context(), party1.req, session.AggregateNonce())
	if err != nil {
		t.Fatalf("partial: %v", err)
	}
	tampered := cloneBytes(partial1)
	tampered[len(tampered)-1] ^= 1
	if err := session.SubmitPartial(party1.public, tampered); !errors.Is(err, ErrInvalidPartial) {
		t.Fatalf("tampered partial error = %v, want ErrInvalidPartial", err)
	}
	if session.Phase() != PhasePartials {
		t.Fatalf("invalid partial changed phase to %s", session.Phase())
	}
	if err := session.SubmitPartial(party1.public, partial1); err != nil {
		t.Fatalf("valid partial after rejection: %v", err)
	}
}
