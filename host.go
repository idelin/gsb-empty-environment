package netsec

import (
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

// Phase is the externally visible state of a round.
type Phase uint8

const (
	// PhaseCommit accepts commitments and rejects reveals.
	PhaseCommit Phase = iota
	// PhaseReveal accepts reveals. It begins only when every participant has
	// committed.
	PhaseReveal
	// PhaseFinalized has a result.
	PhaseFinalized
	// PhaseAborted has no result and can never advance.
	PhaseAborted
)

func (p Phase) String() string {
	switch p {
	case PhaseCommit:
		return "commit"
	case PhaseReveal:
		return "reveal"
	case PhaseFinalized:
		return "finalized"
	case PhaseAborted:
		return "aborted"
	default:
		return fmt.Sprintf("phase(%d)", uint8(p))
	}
}

// AbortReason explains why a round was aborted.
type AbortReason string

const (
	// AbortNone means the round is not aborted.
	AbortNone AbortReason = ""
	// AbortTimeout means the host deadline expired before every reveal.
	AbortTimeout AbortReason = "timeout"
)

// RoundConfig freezes one round's participant set and deadline.
type RoundConfig struct {
	// ID must be unique for the lifetime of the Host's journal. Reusing a
	// round ID would make cross-round replay possible, so it is rejected.
	ID string
	// Participants must contain between 1 and MaxParticipants unique,
	// non-empty IDs.
	Participants []string
	// Deadline is an absolute time obtained from the host's monotonic clock.
	// It must be after the now passed to StartRound.
	Deadline time.Time
}

// RoundState is a point-in-time snapshot of a round.
type RoundState struct {
	ID string
	// Participants is a copy of the frozen set, sorted by participant ID.
	Participants []string
	Deadline     time.Time
	Phase        Phase
	AbortReason  AbortReason
	Commitments  int
	Reveals      int
	Result       [sha256.Size]byte
	HasResult    bool
}

// Host owns round state and serializes all transitions. Its internal mutex
// also defines the linearization order for concurrent events.
type Host struct {
	mu      sync.Mutex
	journal Journal
	rounds  map[string]*roundState
}

type roundState struct {
	id           string
	participants []string
	members      map[string]struct{}
	deadline     time.Time
	phase        Phase
	abortReason  AbortReason
	commitments  map[string][CommitmentSize]byte
	reveals      map[string][NonceSize]byte
	result       [sha256.Size]byte
	hasResult    bool
}

// NewHost creates a Host and replays every event already in journal.
func NewHost(journal Journal) (*Host, error) {
	if journal == nil {
		return nil, ErrNilJournal
	}
	h := &Host{
		journal: journal,
		rounds:  make(map[string]*roundState),
	}
	if err := journal.Replay(func(record []byte) error {
		ev, err := decodeEvent(record)
		if err != nil {
			return err
		}
		return h.applyReplay(ev)
	}); err != nil {
		return nil, fmt.Errorf("netsec: replay journal: %w", err)
	}
	return h, nil
}

// StartRound freezes a participant set. It returns nil for an identical
// retransmission of an already-started round, and ErrRoundExists if the round
// ID is bound to a different participant set or deadline.
func (h *Host) StartRound(cfg RoundConfig, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	next, err := newRoundState(cfg.ID, cfg.Participants, cfg.Deadline)
	if err != nil {
		return err
	}
	if current, ok := h.rounds[cfg.ID]; ok {
		if sameRoundConfig(current, next) {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrRoundExists, cfg.ID)
	}
	if !now.Before(next.deadline) {
		return fmt.Errorf("%w: round %q deadline %s", ErrDeadlineExceeded, cfg.ID, next.deadline)
	}

	ev := event{
		Kind:             eventStartRound,
		Round:            next.id,
		Participants:     append([]string(nil), next.participants...),
		DeadlineUnixNano: next.deadline.UnixNano(),
	}
	if err := h.persist(ev); err != nil {
		return err
	}
	h.rounds[next.id] = next
	return nil
}

// Commit accepts one commitment from a frozen participant.
//
// The first commitment for a participant wins. An exact retransmission of that
// commitment is acknowledged without appending a second event; a different
// value returns ErrCommitmentConflict. A commitment at or after the deadline
// aborts the round with AbortTimeout and returns ErrDeadlineExceeded.
func (h *Host) Commit(roundID, participant string, commitment []byte, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	rs, ok := h.rounds[roundID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	expired, err := h.expireIfDueLocked(rs, now)
	if err != nil {
		return err
	}
	if expired {
		return fmt.Errorf("%w: round %q", ErrDeadlineExceeded, roundID)
	}
	if len(commitment) != CommitmentSize {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidCommitment, len(commitment), CommitmentSize)
	}
	var value [CommitmentSize]byte
	copy(value[:], commitment)

	next, ev, err := rs.prepareCommit(participant, value)
	if err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	if err := h.persist(*ev); err != nil {
		return err
	}
	h.rounds[roundID] = next
	return nil
}

// Reveal accepts one nonce from a frozen participant.
//
// Reveals are rejected until every participant has committed. The first valid
// reveal for a participant wins. An exact retransmission is acknowledged
// without appending a second event; a different value returns
// ErrRevealConflict. The last valid reveal finalizes the round.
func (h *Host) Reveal(roundID, participant string, nonce []byte, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	rs, ok := h.rounds[roundID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	expired, err := h.expireIfDueLocked(rs, now)
	if err != nil {
		return err
	}
	if expired {
		return fmt.Errorf("%w: round %q", ErrDeadlineExceeded, roundID)
	}
	if len(nonce) != NonceSize {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidNonce, len(nonce), NonceSize)
	}
	var value [NonceSize]byte
	copy(value[:], nonce)

	next, ev, err := rs.prepareReveal(participant, value)
	if err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	if err := h.persist(*ev); err != nil {
		return err
	}
	h.rounds[roundID] = next
	return nil
}

// Timeout lets the host report a monotonic-clock reading. Before the deadline
// it returns ErrNotExpired and changes nothing. At or after the deadline it
// durably aborts a non-terminal round. A terminal round is left unchanged.
func (h *Host) Timeout(roundID string, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	rs, ok := h.rounds[roundID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	if rs.phase == PhaseFinalized || rs.phase == PhaseAborted {
		return nil
	}
	if now.Before(rs.deadline) {
		return fmt.Errorf("%w: round %q deadline %s", ErrNotExpired, roundID, rs.deadline)
	}
	_, err := h.expireIfDueLocked(rs, now)
	return err
}

// Result returns the finalized digest. Aborted rounds never produce a result,
// and missing nonces are never replaced with zeroes.
func (h *Host) Result(roundID string) ([sha256.Size]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	rs, ok := h.rounds[roundID]
	if !ok {
		return [sha256.Size]byte{}, fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	if rs.phase == PhaseAborted {
		return [sha256.Size]byte{}, fmt.Errorf("%w: %q (%s)", ErrRoundAborted, roundID, rs.abortReason)
	}
	if !rs.hasResult {
		return [sha256.Size]byte{}, fmt.Errorf("%w: %q is in %s phase", ErrNotFinalized, roundID, rs.phase)
	}
	return rs.result, nil
}

// State returns a snapshot of one round.
func (h *Host) State(roundID string) (RoundState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	rs, ok := h.rounds[roundID]
	if !ok {
		return RoundState{}, fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	return RoundState{
		ID:           rs.id,
		Participants: append([]string(nil), rs.participants...),
		Deadline:     rs.deadline,
		Phase:        rs.phase,
		AbortReason:  rs.abortReason,
		Commitments:  len(rs.commitments),
		Reveals:      len(rs.reveals),
		Result:       rs.result,
		HasResult:    rs.hasResult,
	}, nil
}

// RoundIDs returns the known round IDs in sorted order.
func (h *Host) RoundIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	ids := make([]string, 0, len(h.rounds))
	for id := range h.rounds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (h *Host) persist(ev event) error {
	record, err := encodeEvent(ev)
	if err != nil {
		return err
	}
	if err := h.journal.Append(record); err != nil {
		return fmt.Errorf("netsec: persist %s event: %w", ev.Kind, err)
	}
	return nil
}

func (h *Host) expireIfDueLocked(rs *roundState, now time.Time) (bool, error) {
	if rs.phase == PhaseFinalized || rs.phase == PhaseAborted {
		return false, nil
	}
	if now.Before(rs.deadline) {
		return false, nil
	}

	ev := event{Kind: eventTimeout, Round: rs.id}
	if err := h.persist(ev); err != nil {
		return false, err
	}
	next := rs.clone()
	next.phase = PhaseAborted
	next.abortReason = AbortTimeout
	h.rounds[rs.id] = next
	return true, nil
}

func (h *Host) applyReplay(ev event) error {
	switch ev.Kind {
	case eventStartRound:
		deadline := time.Unix(0, ev.DeadlineUnixNano).UTC()
		rs, err := newRoundState(ev.Round, ev.Participants, deadline)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrJournalCorrupt, err)
		}
		if current, ok := h.rounds[ev.Round]; ok {
			if sameRoundConfig(current, rs) {
				return nil
			}
			return fmt.Errorf("%w: conflicting start event for %q", ErrJournalCorrupt, ev.Round)
		}
		h.rounds[ev.Round] = rs
		return nil

	case eventCommitment:
		rs, ok := h.rounds[ev.Round]
		if !ok {
			return fmt.Errorf("%w: commitment for unknown round %q", ErrJournalCorrupt, ev.Round)
		}
		if len(ev.Data) != CommitmentSize {
			return fmt.Errorf("%w: commitment for %q has %d bytes", ErrJournalCorrupt, ev.Round, len(ev.Data))
		}
		var value [CommitmentSize]byte
		copy(value[:], ev.Data)
		next, _, err := rs.prepareCommit(ev.Participant, value)
		if err != nil {
			return fmt.Errorf("%w: replay commitment for %q: %v", ErrJournalCorrupt, ev.Round, err)
		}
		if next != nil {
			h.rounds[ev.Round] = next
		}
		return nil

	case eventReveal:
		rs, ok := h.rounds[ev.Round]
		if !ok {
			return fmt.Errorf("%w: reveal for unknown round %q", ErrJournalCorrupt, ev.Round)
		}
		if len(ev.Data) != NonceSize {
			return fmt.Errorf("%w: reveal for %q has %d bytes", ErrJournalCorrupt, ev.Round, len(ev.Data))
		}
		var value [NonceSize]byte
		copy(value[:], ev.Data)
		next, _, err := rs.prepareReveal(ev.Participant, value)
		if err != nil {
			return fmt.Errorf("%w: replay reveal for %q: %v", ErrJournalCorrupt, ev.Round, err)
		}
		if next != nil {
			h.rounds[ev.Round] = next
		}
		return nil

	case eventTimeout:
		rs, ok := h.rounds[ev.Round]
		if !ok {
			return fmt.Errorf("%w: timeout for unknown round %q", ErrJournalCorrupt, ev.Round)
		}
		if rs.phase == PhaseAborted {
			return nil
		}
		if rs.phase == PhaseFinalized {
			return fmt.Errorf("%w: timeout after finalization of %q", ErrJournalCorrupt, ev.Round)
		}
		next := rs.clone()
		next.phase = PhaseAborted
		next.abortReason = AbortTimeout
		h.rounds[ev.Round] = next
		return nil
	}

	return fmt.Errorf("%w: unhandled event %q", ErrJournalCorrupt, ev.Kind)
}

func (rs *roundState) prepareCommit(participant string, value [CommitmentSize]byte) (*roundState, *event, error) {
	if _, ok := rs.members[participant]; !ok {
		return nil, nil, fmt.Errorf("%w: %q", ErrInvalidParticipant, participant)
	}
	if rs.phase == PhaseAborted {
		return nil, nil, fmt.Errorf("%w: %q", ErrRoundAborted, rs.id)
	}
	if old, ok := rs.commitments[participant]; ok {
		if old == value {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%w: participant %q in round %q", ErrCommitmentConflict, participant, rs.id)
	}
	if rs.phase == PhaseFinalized {
		return nil, nil, fmt.Errorf("%w: %q", ErrRoundFinalized, rs.id)
	}
	if rs.phase != PhaseCommit {
		return nil, nil, fmt.Errorf("netsec: internal state error: commitment for %q during %s", participant, rs.phase)
	}

	next := rs.clone()
	next.commitments[participant] = value
	if len(next.commitments) == len(next.participants) {
		next.phase = PhaseReveal
	}
	data := make([]byte, CommitmentSize)
	copy(data, value[:])
	ev := &event{
		Kind:        eventCommitment,
		Round:       rs.id,
		Participant: participant,
		Data:        data,
	}
	return next, ev, nil
}

func (rs *roundState) prepareReveal(participant string, nonce [NonceSize]byte) (*roundState, *event, error) {
	if _, ok := rs.members[participant]; !ok {
		return nil, nil, fmt.Errorf("%w: %q", ErrInvalidParticipant, participant)
	}
	if rs.phase == PhaseAborted {
		return nil, nil, fmt.Errorf("%w: %q", ErrRoundAborted, rs.id)
	}
	if old, ok := rs.reveals[participant]; ok {
		if old == nonce {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%w: participant %q in round %q", ErrRevealConflict, participant, rs.id)
	}
	if rs.phase == PhaseFinalized {
		return nil, nil, fmt.Errorf("%w: %q", ErrRoundFinalized, rs.id)
	}
	if rs.phase == PhaseCommit {
		return nil, nil, fmt.Errorf("%w: round %q", ErrEarlyReveal, rs.id)
	}

	commitment, ok := rs.commitments[participant]
	if !ok {
		return nil, nil, fmt.Errorf("%w: participant %q in round %q", ErrNoCommitment, participant, rs.id)
	}
	expected, err := Commitment(rs.id, participant, nonce[:])
	if err != nil {
		return nil, nil, err
	}
	if expected != commitment {
		return nil, nil, fmt.Errorf("%w: participant %q in round %q", ErrRevealMismatch, participant, rs.id)
	}

	next := rs.clone()
	next.reveals[participant] = nonce
	if len(next.reveals) == len(next.participants) {
		next.result = resultDigest(next.participants, next.reveals)
		next.hasResult = true
		next.phase = PhaseFinalized
	}
	data := make([]byte, NonceSize)
	copy(data, nonce[:])
	ev := &event{
		Kind:        eventReveal,
		Round:       rs.id,
		Participant: participant,
		Data:        data,
	}
	return next, ev, nil
}

func (rs *roundState) clone() *roundState {
	next := &roundState{
		id:           rs.id,
		participants: rs.participants,
		members:      rs.members,
		deadline:     rs.deadline,
		phase:        rs.phase,
		abortReason:  rs.abortReason,
		commitments:  make(map[string][CommitmentSize]byte, len(rs.commitments)),
		reveals:      make(map[string][NonceSize]byte, len(rs.reveals)),
		result:       rs.result,
		hasResult:    rs.hasResult,
	}
	for participant, value := range rs.commitments {
		next.commitments[participant] = value
	}
	for participant, value := range rs.reveals {
		next.reveals[participant] = value
	}
	return next
}

func newRoundState(id string, participants []string, deadline time.Time) (*roundState, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty round ID", ErrInvalidConfig)
	}
	if uint64(len(id)) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: round ID is too long", ErrInvalidConfig)
	}
	if deadline.IsZero() {
		return nil, fmt.Errorf("%w: round %q has a zero deadline", ErrInvalidConfig, id)
	}
	if len(participants) == 0 {
		return nil, fmt.Errorf("%w: round %q has no participants", ErrInvalidConfig, id)
	}
	if len(participants) > MaxParticipants {
		return nil, fmt.Errorf("%w: got %d, maximum is %d", ErrTooManyParticipants, len(participants), MaxParticipants)
	}

	sorted := append([]string(nil), participants...)
	sort.Strings(sorted)
	members := make(map[string]struct{}, len(sorted))
	for _, participant := range sorted {
		if participant == "" {
			return nil, fmt.Errorf("%w: empty participant ID", ErrInvalidParticipant)
		}
		if uint64(len(participant)) > math.MaxUint32 {
			return nil, fmt.Errorf("%w: participant ID is too long", ErrInvalidParticipant)
		}
		if _, exists := members[participant]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateParticipant, participant)
		}
		members[participant] = struct{}{}
	}

	return &roundState{
		id:           id,
		participants: sorted,
		members:      members,
		deadline:     deadline,
		phase:        PhaseCommit,
		commitments:  make(map[string][CommitmentSize]byte, len(sorted)),
		reveals:      make(map[string][NonceSize]byte, len(sorted)),
	}, nil
}

func sameRoundConfig(a, b *roundState) bool {
	return a.deadline.Equal(b.deadline) && slices.Equal(a.participants, b.participants)
}
