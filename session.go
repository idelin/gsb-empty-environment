package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
)

// Phase is a coordinator session phase.
type Phase uint8

const (
	// PhaseCommitments collects all nonce commitments before any reveal.
	PhaseCommitments Phase = iota
	// PhaseReveals accepts nonce reveals and verifies each against its
	// commitment.
	PhaseReveals
	// PhasePartials accepts and verifies partial signatures.
	PhasePartials
	// PhaseComplete means the aggregate signature is available.
	PhaseComplete
	// PhaseAborted is terminal.
	PhaseAborted
)

func (p Phase) String() string {
	switch p {
	case PhaseCommitments:
		return "commitments"
	case PhaseReveals:
		return "reveals"
	case PhasePartials:
		return "partials"
	case PhaseComplete:
		return "complete"
	case PhaseAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

const (
	// AbortReasonZeroAggregateNonce records the required terminal condition
	// sum(R_i) == infinity.
	AbortReasonZeroAggregateNonce = "zero aggregate nonce"
)

// EventKind identifies a replayable coordinator event.
type EventKind string

const (
	// EventCommitment carries a 32-byte nonce commitment.
	EventCommitment EventKind = "commitment"
	// EventReveal carries a compressed R_i = r_i G point.
	EventReveal EventKind = "reveal"
	// EventPartial carries a 32-byte partial signature z_i.
	EventPartial EventKind = "partial"
	// EventAbort carries a human-readable abort reason.
	EventAbort EventKind = "abort"
)

// Event is one coordinator protocol event. Replaying the same ordered events
// against the same session ID, message, and roster deterministically rebuilds
// the session state.
type Event struct {
	Kind   EventKind
	Signer []byte
	Data   []byte
}

// Clone returns a deep copy of the event.
func (e Event) Clone() Event {
	return Event{
		Kind:   e.Kind,
		Signer: cloneBytes(e.Signer),
		Data:   cloneBytes(e.Data),
	}
}

// Session is a coordinator instance for one session ID, message, and roster.
type Session struct {
	sessionID []byte
	message   []byte
	roster    *Roster

	phase       Phase
	abortReason string

	commitments map[string][]byte
	reveals     map[string]point
	revealBytes map[string][]byte
	partials    map[string][]byte

	aggregateR      point
	challenge       []byte
	challengeScalar *big.Int
	aggregateZ      *big.Int

	events []Event
}

// NewSession creates a coordinator session. It rejects a zero aggregate
// public key before any protocol message is accepted.
func NewSession(sessionID, message []byte, roster *Roster) (*Session, error) {
	if roster == nil {
		return nil, errors.New("netsec: nil roster")
	}
	if roster.aggregate.isInfinity() {
		return nil, ErrZeroAggregatePublicKey
	}
	if len(roster.keys) < MinRosterSize || len(roster.keys) > MaxRosterSize {
		return nil, ErrRosterSize
	}
	if len(roster.keys) != len(roster.encodedKeys) ||
		len(roster.keys) != len(roster.coefficients) ||
		roster.index == nil ||
		len(roster.encoded) == 0 {
		return nil, errors.New("netsec: malformed roster")
	}

	return &Session{
		sessionID:   cloneBytes(sessionID),
		message:     cloneBytes(message),
		roster:      roster,
		phase:       PhaseCommitments,
		commitments: make(map[string][]byte, roster.Size()),
		reveals:     make(map[string]point, roster.Size()),
		revealBytes: make(map[string][]byte, roster.Size()),
		partials:    make(map[string][]byte, roster.Size()),
		aggregateR:  infinity(),
	}, nil
}

// SessionID returns a copy of the session identifier.
func (s *Session) SessionID() []byte {
	return cloneBytes(s.sessionID)
}

// Message returns a copy of the message being signed.
func (s *Session) Message() []byte {
	return cloneBytes(s.message)
}

// Phase returns the current protocol phase.
func (s *Session) Phase() Phase {
	return s.phase
}

// AbortReason returns the terminal reason, if the session aborted.
func (s *Session) AbortReason() string {
	return s.abortReason
}

// Challenge returns the bound challenge e, or nil before all reveals.
func (s *Session) Challenge() []byte {
	return cloneBytes(s.challenge)
}

// AggregatePublicKey returns the compressed P, or nil for a malformed roster.
func (s *Session) AggregatePublicKey() []byte {
	return s.roster.AggregatePublicKeyBytes()
}

// AggregateNonce returns the compressed R, or nil before all reveals.
func (s *Session) AggregateNonce() []byte {
	if s.aggregateR.isInfinity() {
		return nil
	}
	return s.aggregateR.compressed()
}

func (s *Session) canonicalSigner(publicKey []byte) (string, int, point, error) {
	parsed, err := parseCompressedPoint(publicKey)
	if err != nil {
		return "", 0, point{}, err
	}
	canonical := parsed.compressed()
	index, ok := s.roster.index[string(canonical)]
	if !ok {
		return "", 0, point{}, ErrUnknownSigner
	}
	return string(canonical), index, parsed, nil
}

func (s *Session) applyCommitment(publicKey, commitment []byte) (bool, error) {
	if s.phase == PhaseAborted {
		return false, ErrSessionAborted
	}
	key, _, _, err := s.canonicalSigner(publicKey)
	if err != nil {
		return false, err
	}
	if len(commitment) != 32 {
		return false, ErrInvalidCommitment
	}
	if existing, ok := s.commitments[key]; ok {
		if bytes.Equal(existing, commitment) {
			return false, nil
		}
		return false, ErrConflictingSubmission
	}
	if s.phase != PhaseCommitments {
		return false, ErrSessionPhase
	}
	s.commitments[key] = cloneBytes(commitment)
	if len(s.commitments) == s.roster.Size() {
		s.phase = PhaseReveals
	}
	return true, nil
}

func (s *Session) applyReveal(publicKey, noncePointBytes []byte) (bool, error) {
	if s.phase == PhaseAborted {
		return false, ErrSessionAborted
	}
	key, _, _, err := s.canonicalSigner(publicKey)
	if err != nil {
		return false, err
	}
	noncePoint, err := parseCompressedPoint(noncePointBytes)
	if err != nil {
		return false, err
	}
	if noncePoint.isInfinity() {
		return false, ErrInvalidPoint
	}
	canonicalNonce := noncePoint.compressed()
	if existing, ok := s.revealBytes[key]; ok {
		if bytes.Equal(existing, canonicalNonce) {
			return false, nil
		}
		return false, ErrConflictingSubmission
	}
	if s.phase != PhaseReveals {
		return false, ErrSessionPhase
	}

	commitment, ok := s.commitments[key]
	if !ok {
		return false, ErrSessionPhase
	}
	expected := CommitmentHash(s.sessionID, []byte(key), canonicalNonce)
	if !bytes.Equal(expected, commitment) {
		return false, ErrCommitmentMismatch
	}

	s.reveals[key] = noncePoint
	s.revealBytes[key] = canonicalNonce
	if len(s.reveals) == s.roster.Size() {
		aggregate := infinity()
		for _, rosterKey := range s.roster.encodedKeys {
			aggregate = addPoints(aggregate, s.reveals[string(rosterKey)])
		}
		s.aggregateR = aggregate
		if aggregate.isInfinity() {
			s.phase = PhaseAborted
			s.abortReason = AbortReasonZeroAggregateNonce
			return true, ErrZeroAggregateNonce
		}
		challenge, err := ComputeChallenge(s.sessionID, s.message, s.roster, aggregate.compressed())
		if err != nil {
			s.phase = PhaseAborted
			s.abortReason = err.Error()
			return true, err
		}
		s.challenge = challenge
		s.challengeScalar = new(big.Int).SetBytes(challenge)
		s.phase = PhasePartials
	}
	return true, nil
}

func (s *Session) applyPartial(publicKey, partialBytes []byte) (bool, error) {
	if s.phase == PhaseAborted {
		return false, ErrSessionAborted
	}
	key, index, _, err := s.canonicalSigner(publicKey)
	if err != nil {
		return false, err
	}
	if existing, ok := s.partials[key]; ok {
		if bytes.Equal(existing, partialBytes) {
			return false, nil
		}
		return false, ErrConflictingSubmission
	}
	if s.phase != PhasePartials {
		return false, ErrSessionPhase
	}
	partial, err := parseScalar(partialBytes)
	if err != nil {
		return false, ErrInvalidPartial
	}
	revealed, ok := s.reveals[key]
	if !ok {
		return false, ErrSessionPhase
	}
	if s.challengeScalar == nil || s.aggregateR.isInfinity() {
		return false, ErrSessionPhase
	}

	coefficient := s.roster.coefficients[index]
	// z_i G == R_i + e*a_i*P_i
	expectedRight := addPoints(
		revealed,
		scalarMultPoint(
			point{x: s.roster.keys[index].X, y: s.roster.keys[index].Y},
			new(big.Int).Mul(s.challengeScalar, coefficient),
		),
	)
	if !scalarBaseMult(partial).equal(expectedRight) {
		return false, ErrInvalidPartial
	}

	s.partials[key] = cloneBytes(partialBytes)
	if len(s.partials) == s.roster.Size() {
		sum := new(big.Int)
		for _, rosterKey := range s.roster.encodedKeys {
			sum.Add(sum, new(big.Int).SetBytes(s.partials[string(rosterKey)]))
		}
		sum.Mod(sum, groupN)

		expectedRight = addPoints(
			s.aggregateR,
			scalarMultPoint(s.roster.aggregate, s.challengeScalar),
		)
		if !scalarBaseMult(sum).equal(expectedRight) {
			return false, errors.New("netsec: aggregate partial signature check failed")
		}
		s.aggregateZ = sum
		s.phase = PhaseComplete
	}
	return true, nil
}

func (s *Session) applyAbort(reason string) error {
	if s.phase == PhaseComplete {
		return ErrSessionComplete
	}
	if s.phase == PhaseAborted {
		return nil
	}
	s.phase = PhaseAborted
	s.abortReason = reason
	return nil
}

// SubmitCommitment records one signer's nonce commitment. Once every roster
// member has committed, the session advances to the reveal phase.
func (s *Session) SubmitCommitment(publicKey, commitment []byte) error {
	added, err := s.applyCommitment(publicKey, commitment)
	if added {
		s.events = append(s.events, Event{
			Kind:   EventCommitment,
			Signer: cloneBytes(publicKey),
			Data:   cloneBytes(commitment),
		})
	}
	return err
}

// SubmitReveal verifies R_i against the earlier commitment and records it.
// The final reveal computes R and e. A zero aggregate R terminates the
// session with ErrZeroAggregateNonce.
func (s *Session) SubmitReveal(publicKey, noncePoint []byte) error {
	added, err := s.applyReveal(publicKey, noncePoint)
	if added {
		s.events = append(s.events, Event{
			Kind:   EventReveal,
			Signer: cloneBytes(publicKey),
			Data:   cloneBytes(noncePoint),
		})
	}
	return err
}

// SubmitPartial verifies one partial signature and records it. The final
// partial completes the aggregate signature.
func (s *Session) SubmitPartial(publicKey, partial []byte) error {
	added, err := s.applyPartial(publicKey, partial)
	if added {
		s.events = append(s.events, Event{
			Kind:   EventPartial,
			Signer: cloneBytes(publicKey),
			Data:   cloneBytes(partial),
		})
	}
	return err
}

// Abort terminates the coordinator session locally. It is separate from
// Signer.Abort, which permanently discards the signer's nonce.
func (s *Session) Abort(reason string) error {
	if err := s.applyAbort(reason); err != nil {
		return err
	}
	s.events = append(s.events, Event{Kind: EventAbort, Data: []byte(reason)})
	return nil
}

// Events returns a deep copy of the replayable coordinator transcript.
func (s *Session) Events() []Event {
	out := make([]Event, len(s.events))
	for i, event := range s.events {
		out[i] = event.Clone()
	}
	return out
}

// AggregateSignature returns the completed signature (R, e, z).
func (s *Session) AggregateSignature() (*Signature, error) {
	if s.phase != PhaseComplete {
		return nil, ErrSessionPhase
	}
	if s.aggregateR.isInfinity() || s.aggregateZ == nil || len(s.challenge) != ScalarSize {
		return nil, ErrNonceCorrupt
	}
	return &Signature{
		R: s.aggregateR.compressed(),
		E: cloneBytes(s.challenge),
		Z: scalarBytes(s.aggregateZ),
	}, nil
}

// SessionSnapshot is a comparable, public view of replayable session state.
type SessionSnapshot struct {
	Phase              Phase
	AbortReason        string
	CommitmentCount    int
	RevealCount        int
	PartialCount       int
	EventCount         int
	Challenge          []byte
	AggregatePublicKey []byte
	AggregateNonce     []byte
	AggregateZ         []byte
}

// Snapshot returns a deep copy of the observable session state.
func (s *Session) Snapshot() SessionSnapshot {
	snapshot := SessionSnapshot{
		Phase:              s.phase,
		AbortReason:        s.abortReason,
		CommitmentCount:    len(s.commitments),
		RevealCount:        len(s.reveals),
		PartialCount:       len(s.partials),
		EventCount:         len(s.events),
		Challenge:          cloneBytes(s.challenge),
		AggregatePublicKey: s.AggregatePublicKey(),
	}
	if !s.aggregateR.isInfinity() {
		snapshot.AggregateNonce = s.aggregateR.compressed()
	}
	if s.aggregateZ != nil {
		snapshot.AggregateZ = scalarBytes(s.aggregateZ)
	}
	return snapshot
}

// ReplaySession deterministically rebuilds a session from its ordered event
// transcript. The zero aggregate R terminal event is accepted because it
// leaves the session in the terminal aborted state.
func ReplaySession(sessionID, message []byte, roster *Roster, events []Event) (*Session, error) {
	session, err := NewSession(sessionID, message, roster)
	if err != nil {
		return nil, err
	}
	for i, event := range events {
		switch event.Kind {
		case EventCommitment:
			_, err = session.applyCommitment(event.Signer, event.Data)
		case EventReveal:
			_, err = session.applyReveal(event.Signer, event.Data)
		case EventPartial:
			_, err = session.applyPartial(event.Signer, event.Data)
		case EventAbort:
			err = session.applyAbort(string(event.Data))
		default:
			err = fmt.Errorf("netsec: unknown event kind %q", event.Kind)
		}
		if err != nil && !errors.Is(err, ErrZeroAggregateNonce) {
			return nil, fmt.Errorf("netsec: replay event %d: %w", i, err)
		}
	}
	session.events = make([]Event, len(events))
	for i, event := range events {
		session.events[i] = event.Clone()
	}
	return session, nil
}
