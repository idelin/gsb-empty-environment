package netsec

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	// MaxParticipants is the largest participant set a round may contain.
	MaxParticipants = 32

	// NonceSize is the size of a participant's random nonce.
	NonceSize = 32

	// CommitmentSize is the size of a commitment and of the final result.
	CommitmentSize = 32
)

var (
	// ErrInvalidConfig reports an invalid round configuration.
	ErrInvalidConfig = errors.New("netsec: invalid round configuration")
	// ErrRoundExists reports reuse of a round ID with different parameters.
	ErrRoundExists = errors.New("netsec: round already exists")
	// ErrRoundNotFound reports an unknown round ID.
	ErrRoundNotFound = errors.New("netsec: round not found")
	// ErrUnknownParticipant reports a sender outside the frozen participant set.
	ErrUnknownParticipant = errors.New("netsec: participant is not in the frozen set")
	// ErrCommitConflict reports a second, different commitment from one participant.
	ErrCommitConflict = errors.New("netsec: participant already committed a different value")
	// ErrRevealConflict reports a second, different reveal from one participant.
	ErrRevealConflict = errors.New("netsec: participant already revealed a different value")
	// ErrRevealMismatch reports a nonce that does not match the commitment.
	ErrRevealMismatch = errors.New("netsec: reveal does not match commitment")
	// ErrRevealNotOpen reports a reveal before every commitment is present.
	ErrRevealNotOpen = errors.New("netsec: reveal phase has not started")
	// ErrWrongPhase reports an event that the current phase does not accept.
	ErrWrongPhase = errors.New("netsec: round phase does not accept this event")
	// ErrRoundAborted reports an operation on an aborted round.
	ErrRoundAborted = errors.New("netsec: round was aborted")
	// ErrRoundFinalized reports an operation on a finalized round.
	ErrRoundFinalized = errors.New("netsec: round is already finalized")
	// ErrDeadlinePassed reports an event at or after its phase deadline.
	ErrDeadlinePassed = errors.New("netsec: event arrived at or after the deadline")
	// ErrPersistence reports that a durable write failed. The in-memory state is
	// unchanged when this error is returned.
	ErrPersistence = errors.New("netsec: persistence failed")
)

// Commitment is a SHA-256 commitment to a round, participant, and nonce.
type Commitment [CommitmentSize]byte

// Nonce is a participant's 32-byte random value.
type Nonce [NonceSize]byte

// Result is the SHA-256 of the sorted, concatenated reveal nonces.
type Result [CommitmentSize]byte

// Phase is the lifecycle state of a round.
type Phase uint8

const (
	// PhaseCommit accepts commitments.
	PhaseCommit Phase = iota + 1
	// PhaseReveal accepts reveals.
	PhaseReveal
	// PhaseFinalized means every reveal was verified and Result is available.
	PhaseFinalized
	// PhaseAborted means the round ended without a result.
	PhaseAborted
)

// String implements fmt.Stringer.
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
		return "unknown"
	}
}

// AbortReason explains why a round produced no result.
type AbortReason string

const (
	// AbortCommitTimeout means at least one commitment was missing at the
	// commitment deadline.
	AbortCommitTimeout AbortReason = "commit-timeout"
	// AbortRevealTimeout means at least one valid reveal was missing at the
	// reveal deadline.
	AbortRevealTimeout AbortReason = "reveal-timeout"
)

// Outcome distinguishes a newly applied event from an idempotent replay.
type Outcome uint8

const (
	// Accepted means the event was durably applied.
	Accepted Outcome = iota + 1
	// Duplicate means an identical event had already been applied.
	Duplicate
)

// Clock supplies the host's monotonic time source. The default is time.Now,
// whose readings carry Go's monotonic component. Injected clocks should also
// be monotonic; wall-clock jumps must not extend or shorten a deadline.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Option configures an Engine.
type Option func(*engineConfig) error

type engineConfig struct {
	clock Clock
}

// WithClock supplies a host clock. The host is responsible for driving
// timeouts with Tick or Advance from the same monotonic source.
func WithClock(clock Clock) Option {
	return func(cfg *engineConfig) error {
		if clock == nil {
			return errors.New("netsec: nil clock")
		}
		cfg.clock = clock
		return nil
	}
}

// RoundConfig defines a new round. The participant slice is copied and frozen.
type RoundConfig struct {
	RoundID       string
	Participants  []string
	CommitTimeout time.Duration
	RevealTimeout time.Duration
}

// RoundView is a read-only snapshot of a round.
type RoundView struct {
	RoundID        string
	Participants   []string
	Phase          Phase
	CommitCount    int
	RevealCount    int
	StartedAt      time.Time
	CommitDeadline time.Time
	RevealDeadline time.Time
	Result         Result
	HasResult      bool
	AbortReason    AbortReason
}

// CommitmentFor computes the specification's canonical commitment. Both
// lengths are encoded as unsigned 64-bit big-endian integers.
func CommitmentFor(roundID, participantID string, nonce Nonce) Commitment {
	var length [8]byte
	h := sha256.New()

	binary.BigEndian.PutUint64(length[:], uint64(len(roundID)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(roundID))

	binary.BigEndian.PutUint64(length[:], uint64(len(participantID)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(participantID))

	_, _ = h.Write(nonce[:])

	var commitment Commitment
	copy(commitment[:], h.Sum(nil))
	return commitment
}

// VerifyCommitment checks a commitment in constant time.
func VerifyCommitment(commitment Commitment, roundID, participantID string, nonce Nonce) bool {
	expected := CommitmentFor(roundID, participantID, nonce)
	return subtle.ConstantTimeCompare(commitment[:], expected[:]) == 1
}

// NewNonce returns a cryptographically random 32-byte nonce.
func NewNonce() (Nonce, error) {
	var nonce Nonce
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return Nonce{}, fmt.Errorf("netsec: generate nonce: %w", err)
	}
	return nonce, nil
}

type roundState struct {
	id             string
	participants   []string
	participantSet map[string]struct{}
	phase          Phase
	commits        map[string]Commitment
	reveals        map[string]Nonce
	startedAt      time.Time
	commitDeadline time.Time
	revealDeadline time.Time
	commitTimeout  time.Duration
	revealTimeout  time.Duration
	result         Result
	hasResult      bool
	abortReason    AbortReason
}

// Engine owns in-memory round state backed by an append-only journal.
// All state-changing methods are serialized.
type Engine struct {
	mu      sync.Mutex
	store   Store
	clock   Clock
	journal []byte
	rounds  map[string]*roundState
}

// New opens or creates an Engine and replays the journal from store. If replay
// fails, New returns an error and no partially replayed Engine.
func New(store Store, opts ...Option) (*Engine, error) {
	if store == nil {
		return nil, errors.New("netsec: nil store")
	}

	cfg := engineConfig{clock: systemClock{}}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if cfg.clock == nil {
		return nil, errors.New("netsec: nil clock")
	}

	data, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("netsec: load journal: %w", err)
	}

	e := &Engine{
		store:  store,
		clock:  cfg.clock,
		rounds: make(map[string]*roundState),
	}
	if err := e.replay(data); err != nil {
		return nil, err
	}
	e.journal = append([]byte(nil), data...)
	return e, nil
}

// StartRound durably creates a round and freezes its participant set.
func (e *Engine) StartRound(cfg RoundConfig) (RoundView, Outcome, error) {
	participants, err := validateConfig(cfg)
	if err != nil {
		return RoundView{}, 0, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if existing, ok := e.rounds[cfg.RoundID]; ok {
		if sameConfig(existing, cfg, participants) {
			return existing.view(), Duplicate, nil
		}
		return RoundView{}, 0, fmt.Errorf("%w: %q", ErrRoundExists, cfg.RoundID)
	}

	now := e.clock.Now()
	record := journalRecord{
		Version:       journalVersion,
		Kind:          recordStart,
		Round:         cfg.RoundID,
		Participants:  participants,
		At:            now.UnixNano(),
		CommitTimeout: int64(cfg.CommitTimeout),
		RevealTimeout: int64(cfg.RevealTimeout),
	}
	if err := e.appendLocked(record); err != nil {
		return RoundView{}, 0, err
	}

	r := newRound(cfg, participants, now)
	e.rounds[r.id] = r
	return r.view(), Accepted, nil
}

// Commit durably records a participant's commitment. An identical retry is
// reported as Duplicate; a different second commitment is rejected without
// replacing the first.
func (e *Engine) Commit(roundID, participantID string, commitment Commitment) (Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, err := e.roundLocked(roundID)
	if err != nil {
		return 0, err
	}
	if _, ok := r.participantSet[participantID]; !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownParticipant, participantID)
	}

	if previous, ok := r.commits[participantID]; ok {
		if previous == commitment {
			return Duplicate, nil
		}
		return 0, fmt.Errorf("%w: %q", ErrCommitConflict, participantID)
	}

	if err := r.canCommit(); err != nil {
		return 0, err
	}

	now := e.clock.Now()
	if !now.Before(r.commitDeadline) {
		return 0, fmt.Errorf("%w: commitment deadline %s", ErrDeadlinePassed, r.commitDeadline.Format(time.RFC3339Nano))
	}

	record := journalRecord{
		Version:     journalVersion,
		Kind:        recordCommit,
		Round:       roundID,
		Participant: participantID,
		Data:        append([]byte(nil), commitment[:]...),
		At:          now.UnixNano(),
	}
	if err := e.appendLocked(record); err != nil {
		return 0, err
	}

	r.commits[participantID] = commitment
	if len(r.commits) == len(r.participants) {
		r.phase = PhaseReveal
		r.revealDeadline = now.Add(r.revealTimeout)
	}
	return Accepted, nil
}

// Reveal durably records and verifies a participant's nonce. An identical
// retry is reported as Duplicate; a different second reveal is rejected.
func (e *Engine) Reveal(roundID, participantID string, nonce Nonce) (Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, err := e.roundLocked(roundID)
	if err != nil {
		return 0, err
	}
	if _, ok := r.participantSet[participantID]; !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownParticipant, participantID)
	}

	if previous, ok := r.reveals[participantID]; ok {
		if previous == nonce {
			return Duplicate, nil
		}
		return 0, fmt.Errorf("%w: %q", ErrRevealConflict, participantID)
	}

	if err := r.canReveal(); err != nil {
		return 0, err
	}

	now := e.clock.Now()
	if !now.Before(r.revealDeadline) {
		return 0, fmt.Errorf("%w: reveal deadline %s", ErrDeadlinePassed, r.revealDeadline.Format(time.RFC3339Nano))
	}

	commitment, ok := r.commits[participantID]
	if !ok {
		return 0, fmt.Errorf("%w: no commitment for %q", ErrRevealNotOpen, participantID)
	}
	if !VerifyCommitment(commitment, roundID, participantID, nonce) {
		return 0, fmt.Errorf("%w: %q", ErrRevealMismatch, participantID)
	}

	record := journalRecord{
		Version:     journalVersion,
		Kind:        recordReveal,
		Round:       roundID,
		Participant: participantID,
		Data:        append([]byte(nil), nonce[:]...),
		At:          now.UnixNano(),
	}
	if err := e.appendLocked(record); err != nil {
		return 0, err
	}

	r.reveals[participantID] = nonce
	if len(r.reveals) == len(r.participants) {
		r.result = r.computeResult()
		r.hasResult = true
		r.phase = PhaseFinalized
	}
	return Accepted, nil
}

// Advance durably aborts every active round whose current deadline is at or
// before now. It returns the aborted round IDs. The host should call Advance
// (or Tick) from a timer driven by its monotonic clock. If the journal write
// fails, no round in this call changes state.
func (e *Engine) Advance(now time.Time) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var due []string
	for id, r := range e.rounds {
		if r.deadlineReached(now) {
			due = append(due, id)
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	sort.Strings(due)

	record := journalRecord{
		Version: journalVersion,
		Kind:    recordExpire,
		Rounds:  due,
		At:      now.UnixNano(),
	}
	if err := e.appendLocked(record); err != nil {
		return nil, err
	}

	for _, id := range due {
		r := e.rounds[id]
		r.abortForDeadline()
	}
	return append([]string(nil), due...), nil
}

// Tick applies timeouts using the host clock. A host timer should call Tick;
// tests and event loops may call Advance with an explicit monotonic reading.
func (e *Engine) Tick() ([]string, error) {
	return e.Advance(e.clock.Now())
}

// Round returns a read-only snapshot of one round.
func (e *Engine) Round(roundID string) (RoundView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, err := e.roundLocked(roundID)
	if err != nil {
		return RoundView{}, err
	}
	return r.view(), nil
}

// Rounds returns snapshots sorted by round ID.
func (e *Engine) Rounds() []RoundView {
	e.mu.Lock()
	defer e.mu.Unlock()

	ids := make([]string, 0, len(e.rounds))
	for id := range e.rounds {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	views := make([]RoundView, 0, len(ids))
	for _, id := range ids {
		views = append(views, e.rounds[id].view())
	}
	return views
}

func (e *Engine) roundLocked(roundID string) (*roundState, error) {
	r, ok := e.rounds[roundID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
	}
	return r, nil
}

func newRound(cfg RoundConfig, participants []string, now time.Time) *roundState {
	set := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		set[participant] = struct{}{}
	}
	return &roundState{
		id:             cfg.RoundID,
		participants:   append([]string(nil), participants...),
		participantSet: set,
		phase:          PhaseCommit,
		commits:        make(map[string]Commitment, len(participants)),
		reveals:        make(map[string]Nonce, len(participants)),
		startedAt:      now,
		commitDeadline: now.Add(cfg.CommitTimeout),
		commitTimeout:  cfg.CommitTimeout,
		revealTimeout:  cfg.RevealTimeout,
	}
}

func (r *roundState) view() RoundView {
	return RoundView{
		RoundID:        r.id,
		Participants:   append([]string(nil), r.participants...),
		Phase:          r.phase,
		CommitCount:    len(r.commits),
		RevealCount:    len(r.reveals),
		StartedAt:      r.startedAt,
		CommitDeadline: r.commitDeadline,
		RevealDeadline: r.revealDeadline,
		Result:         r.result,
		HasResult:      r.hasResult,
		AbortReason:    r.abortReason,
	}
}

func (r *roundState) canCommit() error {
	switch r.phase {
	case PhaseCommit:
		return nil
	case PhaseReveal:
		return fmt.Errorf("%w: commitment phase is closed", ErrWrongPhase)
	case PhaseFinalized:
		return fmt.Errorf("%w: %q", ErrRoundFinalized, r.id)
	case PhaseAborted:
		return fmt.Errorf("%w: %q (%s)", ErrRoundAborted, r.id, r.abortReason)
	default:
		return fmt.Errorf("%w: unknown phase", ErrWrongPhase)
	}
}

func (r *roundState) canReveal() error {
	switch r.phase {
	case PhaseCommit:
		return fmt.Errorf("%w: %q", ErrRevealNotOpen, r.id)
	case PhaseReveal:
		return nil
	case PhaseFinalized:
		return fmt.Errorf("%w: %q", ErrRoundFinalized, r.id)
	case PhaseAborted:
		return fmt.Errorf("%w: %q (%s)", ErrRoundAborted, r.id, r.abortReason)
	default:
		return fmt.Errorf("%w: unknown phase", ErrWrongPhase)
	}
}

func (r *roundState) deadlineReached(now time.Time) bool {
	switch r.phase {
	case PhaseCommit:
		return !now.Before(r.commitDeadline)
	case PhaseReveal:
		return !now.Before(r.revealDeadline)
	default:
		return false
	}
}

func (r *roundState) abortForDeadline() {
	switch r.phase {
	case PhaseCommit:
		r.phase = PhaseAborted
		r.abortReason = AbortCommitTimeout
	case PhaseReveal:
		r.phase = PhaseAborted
		r.abortReason = AbortRevealTimeout
	}
	r.result = Result{}
	r.hasResult = false
}

func (r *roundState) computeResult() Result {
	h := sha256.New()
	for _, participant := range r.participants {
		nonce := r.reveals[participant]
		_, _ = h.Write(nonce[:])
	}
	var result Result
	copy(result[:], h.Sum(nil))
	return result
}

func validateConfig(cfg RoundConfig) ([]string, error) {
	if cfg.RoundID == "" {
		return nil, fmt.Errorf("%w: empty round ID", ErrInvalidConfig)
	}
	if cfg.CommitTimeout <= 0 {
		return nil, fmt.Errorf("%w: commit timeout must be positive", ErrInvalidConfig)
	}
	if cfg.RevealTimeout <= 0 {
		return nil, fmt.Errorf("%w: reveal timeout must be positive", ErrInvalidConfig)
	}
	if len(cfg.Participants) == 0 {
		return nil, fmt.Errorf("%w: participant set is empty", ErrInvalidConfig)
	}
	if len(cfg.Participants) > MaxParticipants {
		return nil, fmt.Errorf("%w: %d participants exceeds maximum %d", ErrInvalidConfig, len(cfg.Participants), MaxParticipants)
	}

	participants := append([]string(nil), cfg.Participants...)
	seen := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		if participant == "" {
			return nil, fmt.Errorf("%w: empty participant ID", ErrInvalidConfig)
		}
		if _, ok := seen[participant]; ok {
			return nil, fmt.Errorf("%w: duplicate participant %q", ErrInvalidConfig, participant)
		}
		seen[participant] = struct{}{}
	}
	sort.Strings(participants)
	return participants, nil
}

func sameConfig(r *roundState, cfg RoundConfig, participants []string) bool {
	if r.commitTimeout != cfg.CommitTimeout || r.revealTimeout != cfg.RevealTimeout {
		return false
	}
	if len(r.participants) != len(participants) {
		return false
	}
	for i := range participants {
		if r.participants[i] != participants[i] {
			return false
		}
	}
	return true
}

const (
	journalVersion = 1
	recordStart    = "start"
	recordCommit   = "commit"
	recordReveal   = "reveal"
	recordExpire   = "expire"
)

type journalRecord struct {
	Version       int      `json:"v"`
	Kind          string   `json:"kind"`
	Round         string   `json:"round,omitempty"`
	Participant   string   `json:"participant,omitempty"`
	Data          []byte   `json:"data,omitempty"`
	At            int64    `json:"at,omitempty"`
	Participants  []string `json:"participants,omitempty"`
	CommitTimeout int64    `json:"commit_timeout,omitempty"`
	RevealTimeout int64    `json:"reveal_timeout,omitempty"`
	Rounds        []string `json:"rounds,omitempty"`
}

func (e *Engine) appendLocked(record journalRecord) error {
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("netsec: encode journal record: %w", err)
	}

	next := make([]byte, 0, len(e.journal)+len(line)+1)
	next = append(next, e.journal...)
	next = append(next, line...)
	next = append(next, '\n')

	if err := e.store.Save(next); err != nil {
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	e.journal = next
	return nil
}

func (e *Engine) replay(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}

		var record journalRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("netsec: replay line %d: %w", line, err)
		}
		if record.Version != journalVersion {
			return fmt.Errorf("netsec: replay line %d: unsupported journal version %d", line, record.Version)
		}
		if err := e.applyRecord(record); err != nil {
			return fmt.Errorf("netsec: replay line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("netsec: replay journal: %w", err)
	}
	return nil
}

func (e *Engine) applyRecord(record journalRecord) error {
	switch record.Kind {
	case recordStart:
		return e.applyStart(record)
	case recordCommit:
		return e.applyCommit(record)
	case recordReveal:
		return e.applyReveal(record)
	case recordExpire:
		return e.applyExpire(record)
	default:
		return fmt.Errorf("unknown journal record kind %q", record.Kind)
	}
}

func (e *Engine) applyStart(record journalRecord) error {
	cfg := RoundConfig{
		RoundID:       record.Round,
		Participants:  append([]string(nil), record.Participants...),
		CommitTimeout: time.Duration(record.CommitTimeout),
		RevealTimeout: time.Duration(record.RevealTimeout),
	}
	participants, err := validateConfig(cfg)
	if err != nil {
		return err
	}

	if existing, ok := e.rounds[record.Round]; ok {
		if sameConfig(existing, cfg, participants) {
			return nil
		}
		return fmt.Errorf("%w: conflicting start for %q", ErrRoundExists, record.Round)
	}

	now := time.Unix(0, record.At).UTC()
	e.rounds[record.Round] = newRound(cfg, participants, now)
	return nil
}

func (e *Engine) applyCommit(record journalRecord) error {
	r, ok := e.rounds[record.Round]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRoundNotFound, record.Round)
	}
	if _, ok := r.participantSet[record.Participant]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownParticipant, record.Participant)
	}
	if len(record.Data) != CommitmentSize {
		return fmt.Errorf("invalid commitment length %d", len(record.Data))
	}

	var commitment Commitment
	copy(commitment[:], record.Data)
	if previous, ok := r.commits[record.Participant]; ok {
		if previous == commitment {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrCommitConflict, record.Participant)
	}
	if r.phase != PhaseCommit {
		return fmt.Errorf("%w: commit in phase %s", ErrWrongPhase, r.phase)
	}

	now := time.Unix(0, record.At).UTC()
	if !now.Before(r.commitDeadline) {
		return fmt.Errorf("%w: persisted commit after deadline", ErrDeadlinePassed)
	}

	r.commits[record.Participant] = commitment
	if len(r.commits) == len(r.participants) {
		r.phase = PhaseReveal
		r.revealDeadline = now.Add(r.revealTimeout)
	}
	return nil
}

func (e *Engine) applyReveal(record journalRecord) error {
	r, ok := e.rounds[record.Round]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRoundNotFound, record.Round)
	}
	if _, ok := r.participantSet[record.Participant]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownParticipant, record.Participant)
	}
	if len(record.Data) != NonceSize {
		return fmt.Errorf("invalid nonce length %d", len(record.Data))
	}

	var nonce Nonce
	copy(nonce[:], record.Data)
	if previous, ok := r.reveals[record.Participant]; ok {
		if previous == nonce {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrRevealConflict, record.Participant)
	}
	if r.phase != PhaseReveal {
		return fmt.Errorf("%w: reveal in phase %s", ErrWrongPhase, r.phase)
	}

	now := time.Unix(0, record.At).UTC()
	if !now.Before(r.revealDeadline) {
		return fmt.Errorf("%w: persisted reveal after deadline", ErrDeadlinePassed)
	}

	commitment, ok := r.commits[record.Participant]
	if !ok {
		return fmt.Errorf("%w: no commitment for %q", ErrRevealNotOpen, record.Participant)
	}
	if !VerifyCommitment(commitment, record.Round, record.Participant, nonce) {
		return fmt.Errorf("%w: %q", ErrRevealMismatch, record.Participant)
	}

	r.reveals[record.Participant] = nonce
	if len(r.reveals) == len(r.participants) {
		r.result = r.computeResult()
		r.hasResult = true
		r.phase = PhaseFinalized
	}
	return nil
}

func (e *Engine) applyExpire(record journalRecord) error {
	for _, roundID := range record.Rounds {
		r, ok := e.rounds[roundID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrRoundNotFound, roundID)
		}
		switch r.phase {
		case PhaseAborted:
			continue
		case PhaseCommit, PhaseReveal:
			r.abortForDeadline()
		default:
			return fmt.Errorf("%w: expire in phase %s", ErrWrongPhase, r.phase)
		}
	}
	return nil
}
