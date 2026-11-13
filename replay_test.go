package netsec

import (
	"errors"
	"math/big"
	"reflect"
	"testing"
)

func TestReplayableSessionEvents(t *testing.T) {
	roster, session, party1, party2 := twoPartyReady(t, "replay")
	aggregateR := session.AggregateNonce()
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

	events := session.Events()
	replayed, err := ReplaySession(party1.req.SessionID, party1.req.Message, roster, events)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(session.Snapshot(), replayed.Snapshot()) {
		t.Fatalf("replayed state differs\nlive: %#v\nreplay: %#v", session.Snapshot(), replayed.Snapshot())
	}
	signature, err := replayed.AggregateSignature()
	if err != nil {
		t.Fatalf("replayed aggregate signature: %v", err)
	}
	if err := VerifySignature(signature, party1.req.SessionID, party1.req.Message, roster); err != nil {
		t.Fatalf("replayed signature verification: %v", err)
	}

	// Mutating a partial event must be rejected during replay.
	tampered := cloneEvents(events)
	for i := range tampered {
		if tampered[i].Kind == EventPartial {
			tampered[i].Data[0] ^= 1
			break
		}
	}
	if _, err := ReplaySession(party1.req.SessionID, party1.req.Message, roster, tampered); !errors.Is(err, ErrInvalidPartial) {
		t.Fatalf("tampered partial replay error = %v, want ErrInvalidPartial", err)
	}

	// Mutating a commitment must fail when the matching reveal is checked.
	tampered = cloneEvents(events)
	for i := range tampered {
		if tampered[i].Kind == EventCommitment {
			tampered[i].Data[0] ^= 1
			break
		}
	}
	if _, err := ReplaySession(party1.req.SessionID, party1.req.Message, roster, tampered); !errors.Is(err, ErrCommitmentMismatch) {
		t.Fatalf("tampered commitment replay error = %v, want ErrCommitmentMismatch", err)
	}

	// Replay must not depend on the caller mutating the event slices later.
	events[0].Data[0] ^= 1
	if !reflect.DeepEqual(session.Snapshot(), replayed.Snapshot()) {
		t.Fatal("mutable event input changed replayed state")
	}
}

func TestZeroAggregateNonceTerminatesAndReplays(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	roster := mustRoster(t, key1, key2)
	sessionID := []byte("zero-r")
	message := []byte("m")

	point1 := scalarBaseMult(big.NewInt(1)).compressed()   // G
	negativeOne := new(big.Int).Sub(groupN, big.NewInt(1)) // N-1
	point2 := scalarBaseMult(negativeOne).compressed()     // -G
	public1 := mustPublicKey(t, key1)
	public2 := mustPublicKey(t, key2)

	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := session.SubmitCommitment(public1, CommitmentHash(sessionID, public1, point1)); err != nil {
		t.Fatalf("commitment 1: %v", err)
	}
	if err := session.SubmitCommitment(public2, CommitmentHash(sessionID, public2, point2)); err != nil {
		t.Fatalf("commitment 2: %v", err)
	}
	if err := session.SubmitReveal(public1, point1); err != nil {
		t.Fatalf("reveal 1: %v", err)
	}
	if err := session.SubmitReveal(public2, point2); !errors.Is(err, ErrZeroAggregateNonce) {
		t.Fatalf("zero aggregate R error = %v, want ErrZeroAggregateNonce", err)
	}
	if session.Phase() != PhaseAborted {
		t.Fatalf("phase = %s, want aborted", session.Phase())
	}
	if session.AbortReason() != AbortReasonZeroAggregateNonce {
		t.Fatalf("abort reason = %q", session.AbortReason())
	}
	if session.Challenge() != nil || session.AggregateNonce() != nil {
		t.Fatal("zero-R session exposed a challenge or aggregate nonce")
	}

	events := session.Events()
	replayed, err := ReplaySession(sessionID, message, roster, events)
	if err != nil {
		t.Fatalf("replay zero-R session: %v", err)
	}
	if !reflect.DeepEqual(session.Snapshot(), replayed.Snapshot()) {
		t.Fatalf("zero-R replay state differs\nlive: %#v\nreplay: %#v", session.Snapshot(), replayed.Snapshot())
	}
}

func TestSessionAbortIsReplayableAndTerminal(t *testing.T) {
	roster, session, party1, _ := twoPartyReady(t, "abort-replay")
	if err := session.Abort("coordinator aborted"); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if session.Phase() != PhaseAborted || session.AbortReason() != "coordinator aborted" {
		t.Fatalf("unexpected aborted state: %#v", session.Snapshot())
	}
	if err := session.SubmitPartial(party1.public, make([]byte, ScalarSize)); !errors.Is(err, ErrSessionAborted) {
		t.Fatalf("submission after abort error = %v, want ErrSessionAborted", err)
	}

	events := session.Events()
	foundAbort := false
	for _, event := range events {
		if event.Kind == EventAbort {
			foundAbort = true
		}
	}
	if !foundAbort {
		t.Fatal("abort event missing from transcript")
	}
	replayed, err := ReplaySession(party1.req.SessionID, party1.req.Message, roster, events)
	if err != nil {
		t.Fatalf("replay abort: %v", err)
	}
	if !reflect.DeepEqual(session.Snapshot(), replayed.Snapshot()) {
		t.Fatalf("abort replay differs\nlive: %#v\nreplay: %#v", session.Snapshot(), replayed.Snapshot())
	}
}

func cloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	for i, event := range events {
		out[i] = event.Clone()
	}
	return out
}
