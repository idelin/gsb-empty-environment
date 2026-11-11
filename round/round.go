// Package round implements one commit/reveal round that lets a fixed roster
// of members jointly derive a random seed without trusting the last member.
//
// The protocol is a classic commit/reveal scheme:
//
//   - Every member first publishes a SHA-256 commitment over its secret. The
//     hash is domain separated and binds the round ID, member ID and secret
//     with length-prefixed fields, so commitments cannot be moved between
//     rounds or members.
//   - Members later reveal their secrets. A reveal is accepted only when it
//     hashes to the member's recorded commitment. The first message for a
//     member wins; a different value is refused and the conflicting payload is
//     retained as evidence, so the last member cannot silently substitute its
//     secret.
//   - Once every roster member has a matching commit/reveal pair, the seed is
//     SHA-256 over a domain-separated, length-prefixed framing of the secrets
//     ordered by member ID.
//
// Absence is never converted into a zero value: if any member never produces a
// matching pair the round stays open and yields no seed. The host must abort
// the round (or start a new one). The package therefore explicitly does not
// guarantee a seed when a member is absent or malicious.
//
// Every state transition is written as an immutable event to an EventStore
// before the in-memory state changes, so a Round can be reconstructed by
// replaying the log. Identical messages are idempotent; conflicting messages
// are refused but persisted as evidence.
package round

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
)

const (
	commitDomainV1 = "netsec-round/v1/commit"
	seedDomainV1   = "netsec-round/v1/seed"

	// DigestSize is the size in bytes of a SHA-256 digest.
	DigestSize = sha256.Size

	// SecretSize is the size of a secret produced by NewSecret.
	SecretSize = 32
)

// Sentinel errors returned by the package.
var (
	// ErrInvalidConfig is returned when a Config is malformed.
	ErrInvalidConfig = errors.New("round: invalid configuration")
	// ErrLogNotEmpty is returned when Start is called on a non-empty store.
	ErrLogNotEmpty = errors.New("round: event store is not empty")
	// ErrEmptyLog is returned when Replay finds no events.
	ErrEmptyLog = errors.New("round: event store is empty")
	// ErrCorruptLog is returned when persisted events fail verification.
	ErrCorruptLog = errors.New("round: corrupt event log")
	// ErrUnknownMember is returned for a message from a non-roster member.
	ErrUnknownMember = errors.New("round: unknown member")
	// ErrForeignRound is returned when a message names another round.
	ErrForeignRound = errors.New("round: message belongs to another round")
	// ErrClosed is returned when a round has already completed or aborted.
	ErrClosed = errors.New("round: round is closed")
	// ErrZeroSecret is returned when a reveal carries an empty/all-zero secret.
	ErrZeroSecret = errors.New("round: zero-value secret is not a valid reveal")
	// ErrNoSeed is returned when the round cannot derive a seed yet.
	ErrNoSeed = errors.New("round: seed is not available")
	// ErrCompleted is returned when an abort is attempted after completion.
	ErrCompleted = errors.New("round: round already completed")
	// ErrStoreBroken is returned after the underlying store failed durably.
	ErrStoreBroken = errors.New("round: event store is broken")
)

// MemberID identifies one fixed roster member.
type MemberID string

// Digest is a SHA-256 commitment or seed.
type Digest [DigestSize]byte

// Status is the lifecycle state of a Round.
type Status int

const (
	// StatusOpen means the round is still collecting messages.
	StatusOpen Status = iota
	// StatusCompleted means every member revealed and a seed exists.
	StatusCompleted
	// StatusAborted means the host gave up, for example because of absence.
	StatusAborted
)

func (s Status) String() string {
	switch s {
	case StatusOpen:
		return "open"
	case StatusCompleted:
		return "completed"
	case StatusAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// Config fixes the round ID and the immutable member roster.
type Config struct {
	// ID identifies the round and is bound into every commitment and seed.
	ID string
	// Members is the fixed roster. It must contain at least two unique,
	// non-empty member IDs and cannot change once the round exists.
	Members []MemberID
}

func (c Config) validate() ([]MemberID, error) {
	if c.ID == "" {
		return nil, fmt.Errorf("%w: empty round ID", ErrInvalidConfig)
	}
	if len(c.Members) < 2 {
		return nil, fmt.Errorf("%w: need at least two members", ErrInvalidConfig)
	}
	seen := make(map[MemberID]struct{}, len(c.Members))
	sorted := make([]MemberID, len(c.Members))
	copy(sorted, c.Members)
	for _, member := range sorted {
		if member == "" {
			return nil, fmt.Errorf("%w: empty member ID", ErrInvalidConfig)
		}
		if _, dup := seen[member]; dup {
			return nil, fmt.Errorf("%w: duplicate member %q", ErrInvalidConfig, member)
		}
		seen[member] = struct{}{}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted, nil
}

// CommitMessage is a persisted commitment submitted by one member.
type CommitMessage struct {
	RoundID string   `json:"round_id"`
	Member  MemberID `json:"member"`
	Commit  Digest   `json:"commit"`
}

// RevealMessage is a persisted secret reveal submitted by one member.
type RevealMessage struct {
	RoundID string   `json:"round_id"`
	Member  MemberID `json:"member"`
	Secret  []byte   `json:"secret"`
}

// Receipt reports how a submitted message was handled.
type Receipt struct {
	// Accepted is true when the message advanced the round state.
	Accepted bool
	// Duplicate is true when the exact same message was already recorded.
	Duplicate bool
	// EventSeq is the sequence number of the persisted event.
	EventSeq int64
	// Reason explains a non-accepted, non-structural outcome.
	Reason string
}

// Conflict is an immutable record of a member message that was refused.
type Conflict struct {
	Member   MemberID `json:"member"`
	Kind     string   `json:"kind"`
	Reason   string   `json:"reason"`
	EventSeq int64    `json:"event_seq"`
}

// Snapshot is a point-in-time, defensive copy of the round state.
type Snapshot struct {
	RoundID   string
	Status    Status
	Members   []MemberID
	Committed []MemberID
	Matched   []MemberID
	Missing   []MemberID
	Conflicts []Conflict
	// Seed is nil unless Status is StatusCompleted. It is never populated
	// with zero bytes for absent members.
	Seed []byte
}

// Round is a single commit/reveal round. It is safe for concurrent use.
type Round struct {
	mu sync.Mutex

	cfg    Config
	sorted []MemberID
	store  EventStore

	seq       int64
	status    Status
	states    map[MemberID]*memberState
	conflicts []Conflict
	seed      Digest
	hasSeed   bool
	broken    bool
}

type memberState struct {
	commit []byte
	reveal []byte
	// matched is true only when commit and reveal satisfy Commitment.
	matched bool
}

// NewSecret generates a cryptographically random 32-byte reveal secret.
func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func writeLengthPrefixed(w io.Writer, data []byte) error {
	if len(data) > math.MaxUint32 {
		return fmt.Errorf("round: field too long: %d bytes", len(data))
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// Commitment computes the domain-separated SHA-256 commitment for a secret.
//
// The framed input is the commit domain label, a NUL terminator, and the
// length-prefixed round ID, member ID and secret. Length prefixes make the
// encoding unambiguous, and binding roundID and member means a commitment or
// reveal cannot be replayed against another round or another member.
func Commitment(roundID string, member MemberID, secret []byte) Digest {
	h := sha256.New()
	h.Write([]byte(commitDomainV1))
	h.Write([]byte{0})
	_ = writeLengthPrefixed(h, []byte(roundID))
	_ = writeLengthPrefixed(h, []byte(member))
	_ = writeLengthPrefixed(h, secret)
	var digest Digest
	copy(digest[:], h.Sum(nil))
	return digest
}

// deriveSeed hashes the domain-separated, length-prefixed framing of every
// secret in member-ID order. A missing or empty secret is an error: absence
// is never substituted with a zero value.
func deriveSeed(roundID string, orderedMembers []MemberID, secrets map[MemberID][]byte) (Digest, error) {
	h := sha256.New()
	h.Write([]byte(seedDomainV1))
	h.Write([]byte{0})
	if err := writeLengthPrefixed(h, []byte(roundID)); err != nil {
		return Digest{}, err
	}
	for _, member := range orderedMembers {
		secret, ok := secrets[member]
		if !ok || len(secret) == 0 {
			return Digest{}, fmt.Errorf("%w: member %q has no reveal", ErrNoSeed, member)
		}
		if err := writeLengthPrefixed(h, []byte(member)); err != nil {
			return Digest{}, err
		}
		if err := writeLengthPrefixed(h, secret); err != nil {
			return Digest{}, err
		}
	}
	var digest Digest
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func isZeroSecret(secret []byte) bool {
	if len(secret) == 0 {
		return true
	}
	for _, b := range secret {
		if b != 0 {
			return false
		}
	}
	return true
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// Start creates a new round with a fixed roster and appends its creation
// event. The store must be empty; use Replay to resume an existing round.
func Start(ctx context.Context, cfg Config, store EventStore) (*Round, error) {
	sorted, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	existing, err := store.Read(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) != 0 {
		return nil, ErrLogNotEmpty
	}
	r := newRound(cfg, sorted, store)
	if _, err := r.emit(ctx, &Event{
		Kind:    EvRoundCreated,
		Members: append([]MemberID(nil), cfg.Members...),
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func newRound(cfg Config, sorted []MemberID, store EventStore) *Round {
	states := make(map[MemberID]*memberState, len(sorted))
	for _, member := range sorted {
		states[member] = &memberState{}
	}
	return &Round{
		cfg:    cfg,
		sorted: sorted,
		store:  store,
		states: states,
	}
}

// Status returns the current lifecycle status.
func (r *Round) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Seed returns a copy of the derived seed. It returns ErrNoSeed while the
// round is open or aborted; an absent member is never represented as zero.
func (r *Round) Seed() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasSeed {
		return nil, ErrNoSeed
	}
	return cloneBytes(r.seed[:]), nil
}

// Snapshot returns a defensive copy of the full round state.
func (r *Round) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *Round) snapshotLocked() Snapshot {
	snap := Snapshot{
		RoundID:   r.cfg.ID,
		Status:    r.status,
		Members:   append([]MemberID(nil), r.sorted...),
		Conflicts: append([]Conflict(nil), r.conflicts...),
	}
	for _, member := range r.sorted {
		state := r.states[member]
		if len(state.commit) != 0 {
			snap.Committed = append(snap.Committed, member)
		}
		if state.matched {
			snap.Matched = append(snap.Matched, member)
		} else {
			snap.Missing = append(snap.Missing, member)
		}
	}
	if r.hasSeed {
		snap.Seed = cloneBytes(r.seed[:])
	}
	return snap
}

// Abort terminates an open round, typically because a member is absent. It is
// idempotent for an already aborted round and returns ErrCompleted once the
// seed has been produced.
func (r *Round) Abort(ctx context.Context, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.status {
	case StatusAborted:
		return nil
	case StatusCompleted:
		return ErrCompleted
	}
	_, err := r.emit(ctx, &Event{Kind: EvRoundAborted, Reason: reason})
	return err
}

// checkEnvelope validates routing-level rules and records rejected foreign or
// unknown messages as evidence. The caller must hold r.mu.
func (r *Round) checkEnvelope(ctx context.Context, roundID string, member MemberID) error {
	if r.broken {
		return ErrStoreBroken
	}
	if roundID != r.cfg.ID {
		_, _ = r.emit(ctx, &Event{
			Kind:   EvRejected,
			Member: member,
			Reason: ErrForeignRound.Error(),
			Evidence: &Evidence{
				RawHash: hashCanonical(struct {
					GotRoundID string   `json:"got_round_id"`
					Member     MemberID `json:"member"`
				}{GotRoundID: roundID, Member: member}),
			},
		})
		return ErrForeignRound
	}
	if r.status != StatusOpen {
		_, _ = r.emit(ctx, &Event{
			Kind:   EvRejected,
			Member: member,
			Reason: ErrClosed.Error(),
			Evidence: &Evidence{
				RawHash: hashCanonical(struct {
					RoundID string   `json:"round_id"`
					Status  Status   `json:"status"`
					Member  MemberID `json:"member"`
				}{RoundID: roundID, Status: r.status, Member: member}),
			},
		})
		return ErrClosed
	}
	if _, ok := r.states[member]; !ok {
		_, _ = r.emit(ctx, &Event{
			Kind:   EvRejected,
			Member: member,
			Reason: ErrUnknownMember.Error(),
			Evidence: &Evidence{
				RawHash: hashCanonical(struct {
					RoundID string   `json:"round_id"`
					Member  MemberID `json:"member"`
				}{RoundID: roundID, Member: member}),
			},
		})
		return ErrUnknownMember
	}
	return nil
}

// SubmitCommit records a commitment. Identical repeats are idempotent; a
// different commitment for the same member is refused and retained as
// evidence. The first accepted commitment is the one that binds the member.
func (r *Round) SubmitCommit(ctx context.Context, msg CommitMessage) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkEnvelope(ctx, msg.RoundID, msg.Member); err != nil {
		return Receipt{}, err
	}
	state := r.states[msg.Member]
	incoming := cloneBytes(msg.Commit[:])

	if len(state.commit) != 0 {
		if equalBytes(state.commit, incoming) {
			event, err := r.emit(ctx, &Event{
				Kind:   EvCommitDuplicate,
				Member: msg.Member,
				Commit: msg.Commit,
			})
			if err != nil {
				return Receipt{}, err
			}
			return Receipt{Duplicate: true, EventSeq: event.Seq}, nil
		}
		var existing Digest
		copy(existing[:], state.commit)
		event, err := r.emit(ctx, &Event{
			Kind:   EvCommitConflict,
			Member: msg.Member,
			Commit: msg.Commit,
			Evidence: &Evidence{
				ExistingDigest: existing,
				Offending:      mustCanonical(msg),
			},
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{
			EventSeq: event.Seq,
			Reason:   "commit conflicts with the first accepted commitment",
		}, nil
	}

	event, err := r.emit(ctx, &Event{
		Kind:   EvCommitAccepted,
		Member: msg.Member,
		Commit: msg.Commit,
	})
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Accepted: true, EventSeq: event.Seq}, nil
}

// SubmitReveal records a secret reveal. It is accepted only when the member
// has a commitment and the revealed secret reproduces it; otherwise the
// offending message is refused and retained as evidence. A reveal that
// arrives before its commitment is stored and waits for a matching commitment.
func (r *Round) SubmitReveal(ctx context.Context, msg RevealMessage) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkEnvelope(ctx, msg.RoundID, msg.Member); err != nil {
		return Receipt{}, err
	}
	if isZeroSecret(msg.Secret) {
		event, err := r.emit(ctx, &Event{
			Kind:     EvRejected,
			Member:   msg.Member,
			Reason:   ErrZeroSecret.Error(),
			Evidence: &Evidence{Offending: mustCanonical(msg)},
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{EventSeq: event.Seq, Reason: ErrZeroSecret.Error()}, ErrZeroSecret
	}

	state := r.states[msg.Member]
	incoming := cloneBytes(msg.Secret)

	if state.matched {
		if equalBytes(state.reveal, incoming) {
			event, err := r.emit(ctx, &Event{
				Kind:     EvRevealDuplicate,
				Member:   msg.Member,
				Secret:   cloneBytes(msg.Secret),
				SecretOK: true,
			})
			if err != nil {
				return Receipt{}, err
			}
			return Receipt{Duplicate: true, EventSeq: event.Seq}, nil
		}
		event, err := r.emit(ctx, &Event{
			Kind:     EvRevealConflict,
			Member:   msg.Member,
			Secret:   cloneBytes(msg.Secret),
			Evidence: &Evidence{Offending: mustCanonical(msg)},
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{
			EventSeq: event.Seq,
			Reason:   "member already matched with a different reveal",
		}, nil
	}

	if len(state.commit) != 0 {
		// A commitment is recorded: the reveal is accepted or refused solely
		// against the commitment, so an earlier wrong reveal cannot lock a
		// member out and all rejected bytes stay on disk as evidence.
		var committed Digest
		copy(committed[:], state.commit)
		if Commitment(r.cfg.ID, msg.Member, msg.Secret) != committed {
			event, err := r.emit(ctx, &Event{
				Kind:     EvRevealMismatch,
				Member:   msg.Member,
				Secret:   cloneBytes(msg.Secret),
				Evidence: &Evidence{ExistingDigest: committed, Offending: mustCanonical(msg)},
			})
			if err != nil {
				return Receipt{}, err
			}
			return Receipt{
				EventSeq: event.Seq,
				Reason:   "reveal does not match the recorded commitment",
			}, nil
		}
		_, err := r.emit(ctx, &Event{
			Kind:   EvRevealRecorded,
			Member: msg.Member,
			Secret: cloneBytes(msg.Secret),
		})
		if err != nil {
			return Receipt{}, err
		}
		matched, err := r.emit(ctx, &Event{
			Kind:     EvRevealMatched,
			Member:   msg.Member,
			Secret:   cloneBytes(msg.Secret),
			SecretOK: true,
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{Accepted: true, EventSeq: matched.Seq}, nil
	}

	if len(state.reveal) != 0 {
		if equalBytes(state.reveal, incoming) {
			event, err := r.emit(ctx, &Event{
				Kind:     EvRevealDuplicate,
				Member:   msg.Member,
				Secret:   cloneBytes(msg.Secret),
				SecretOK: true,
			})
			if err != nil {
				return Receipt{}, err
			}
			return Receipt{Duplicate: true, EventSeq: event.Seq}, nil
		}
		event, err := r.emit(ctx, &Event{
			Kind:     EvRevealConflict,
			Member:   msg.Member,
			Secret:   cloneBytes(msg.Secret),
			Evidence: &Evidence{Offending: mustCanonical(msg)},
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{
			EventSeq: event.Seq,
			Reason:   "reveal conflicts with the first recorded reveal",
		}, nil
	}

	// First reveal for this member: persist it first, then verify against the
	// commitment. Persisting pending reveals keeps replay and crash behavior
	// identical to live processing.
	event, err := r.emit(ctx, &Event{
		Kind:   EvRevealRecorded,
		Member: msg.Member,
		Secret: cloneBytes(msg.Secret),
	})
	if err != nil {
		return Receipt{}, err
	}

	if len(state.commit) == 0 {
		return Receipt{
			EventSeq: event.Seq,
			Reason:   "reveal recorded; waiting for the matching commitment",
		}, nil
	}
	return Receipt{Accepted: true, EventSeq: event.Seq}, nil
}

// maybeComplete derives and persists the seed once every roster member has a
// matching pair. It never substitutes anything for a missing member: without
// a full set it simply leaves the round open. The caller must hold r.mu.
func (r *Round) maybeComplete(ctx context.Context) error {
	if r.status != StatusOpen {
		return nil
	}
	secrets := make(map[MemberID][]byte, len(r.sorted))
	for _, member := range r.sorted {
		state := r.states[member]
		if !state.matched || len(state.reveal) == 0 {
			return nil
		}
		secrets[member] = state.reveal
	}
	seed, err := deriveSeed(r.cfg.ID, r.sorted, secrets)
	if err != nil {
		return err
	}
	_, err = r.emit(ctx, &Event{Kind: EvRoundCompleted, Seed: seed})
	return err
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
