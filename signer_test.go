package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func twoPartyReady(t *testing.T, tag string) (*Roster, *Session, *testParty, *testParty) {
	t.Helper()
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	sessionID := []byte("session-" + tag)
	message := []byte("message-" + tag)
	party1 := newTestParty(t, key1, roster, sessionID, message)
	party2 := newTestParty(t, key2, roster, sessionID, message)
	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	for _, party := range []*testParty{party1, party2} {
		commitment, err := party.signer.Commit(t.Context(), party.req)
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		if err := session.SubmitCommitment(party.public, commitment); err != nil {
			t.Fatalf("submit commitment: %v", err)
		}
	}
	for _, party := range []*testParty{party1, party2} {
		reveal, err := party.signer.Reveal(t.Context(), party.req)
		if err != nil {
			t.Fatalf("reveal: %v", err)
		}
		if err := session.SubmitReveal(party.public, reveal); err != nil {
			t.Fatalf("submit reveal: %v", err)
		}
	}
	return roster, session, party1, party2
}

func TestSignerRejectsChangedMessageRosterAndR(t *testing.T) {
	key3 := generateTestKey(t)
	_, session, party1, _ := twoPartyReady(t, "binding")
	aggregateR := session.AggregateNonce()

	partial, err := party1.signer.Partial(t.Context(), party1.req, aggregateR)
	if err != nil {
		t.Fatalf("partial: %v", err)
	}
	retry, err := party1.signer.Partial(t.Context(), party1.req, aggregateR)
	if err != nil {
		t.Fatalf("retry partial: %v", err)
	}
	if !bytes.Equal(partial, retry) {
		t.Fatal("same request did not return the original partial signature")
	}

	changedMessage := party1.req
	changedMessage.Message = []byte("changed message")
	if _, err := party1.signer.Commit(t.Context(), changedMessage); !errors.Is(err, ErrRequestMismatch) {
		t.Fatalf("commit with changed message error = %v, want ErrRequestMismatch", err)
	}
	if _, err := party1.signer.Partial(t.Context(), changedMessage, aggregateR); !errors.Is(err, ErrRequestMismatch) {
		t.Fatalf("changed message error = %v, want ErrRequestMismatch", err)
	}

	changedRoster := party1.req
	changedRoster.Roster = mustRoster(t, party1.key, key3)
	if _, err := party1.signer.Commit(t.Context(), changedRoster); !errors.Is(err, ErrRequestMismatch) {
		t.Fatalf("commit with changed roster error = %v, want ErrRequestMismatch", err)
	}
	if _, err := party1.signer.Partial(t.Context(), changedRoster, aggregateR); !errors.Is(err, ErrRequestMismatch) {
		t.Fatalf("changed roster error = %v, want ErrRequestMismatch", err)
	}

	otherR := differentPoint(t, aggregateR)
	if _, err := party1.signer.Partial(t.Context(), party1.req, otherR); !errors.Is(err, ErrNonceConsumedConflict) {
		t.Fatalf("changed R error = %v, want ErrNonceConsumedConflict", err)
	}

	if err := party1.signer.Abort(t.Context(), party1.req); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if _, err := party1.signer.Partial(t.Context(), party1.req, aggregateR); !errors.Is(err, ErrNonceAborted) {
		t.Fatalf("partial after abort error = %v, want ErrNonceAborted", err)
	}
	if _, err := party1.signer.Commit(t.Context(), party1.req); !errors.Is(err, ErrNonceAborted) {
		t.Fatalf("commit after abort error = %v, want ErrNonceAborted", err)
	}
	if _, err := party1.signer.Reveal(t.Context(), party1.req); !errors.Is(err, ErrNonceAborted) {
		t.Fatalf("reveal after abort error = %v, want ErrNonceAborted", err)
	}

	// Abort before commitment creates a tombstone.
	key4 := generateTestKey(t)
	key5 := generateTestKey(t)
	freshRoster := mustRoster(t, key4, key5)
	fresh, err := NewSigner(key4, NewMemoryNonceStore())
	if err != nil {
		t.Fatalf("fresh signer: %v", err)
	}
	freshRequest := SignRequest{SessionID: []byte("abort-before-commit"), Message: []byte("m"), Roster: freshRoster}
	if err := fresh.Abort(t.Context(), freshRequest); err != nil {
		t.Fatalf("abort before commit: %v", err)
	}
	if _, err := fresh.Commit(t.Context(), freshRequest); !errors.Is(err, ErrNonceAborted) {
		t.Fatalf("commit after tombstone error = %v, want ErrNonceAborted", err)
	}
}

func TestSignerRevealDoesNotChangeAfterRestart(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	store := NewMemoryNonceStore()
	sessionID := []byte("restart-reveal")
	message := []byte("m")
	request := SignRequest{SessionID: sessionID, Message: message, Roster: roster}

	signer, err := NewSigner(key1, store)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	commitment, err := signer.Commit(t.Context(), request)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	reveal, err := signer.Reveal(t.Context(), request)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	restarted, err := NewSigner(key1, store)
	if err != nil {
		t.Fatalf("restart signer: %v", err)
	}
	commitmentAgain, err := restarted.Commit(t.Context(), request)
	if err != nil {
		t.Fatalf("restart commit: %v", err)
	}
	revealAgain, err := restarted.Reveal(t.Context(), request)
	if err != nil {
		t.Fatalf("restart reveal: %v", err)
	}
	if !bytes.Equal(commitment, commitmentAgain) || !bytes.Equal(reveal, revealAgain) {
		t.Fatal("commitment or reveal changed after restart")
	}
}
