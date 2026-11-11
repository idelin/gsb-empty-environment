package revocation

import (
	"errors"
	"fmt"
	"sync"
)

// Sentinel errors returned by the state-machine entry points. Callers may
// use errors.Is to branch without parsing messages.
var (
	// ErrInvalidEvent is returned when an event has a non-positive
	// generation/sequence or an empty credential identifier.
	ErrInvalidEvent = errors.New("revocation: invalid event")
	// ErrInvalidCheckpoint is returned for a non-positive generation or
	// negative sequence.
	ErrInvalidCheckpoint = errors.New("revocation: invalid checkpoint")
	// ErrInvalidSnapshot is returned for a non-positive generation or negative
	// last sequence.
	ErrInvalidSnapshot = errors.New("revocation: invalid snapshot")
	// ErrStaleSnapshot is returned when a snapshot answers an older generation
	// than the one currently waiting for repair. The gap state is retained.
	ErrStaleSnapshot = errors.New("revocation: stale snapshot")
	// ErrUnexpectedSnapshot is returned when a snapshot is delivered while no
	// snapshot is outstanding.
	ErrUnexpectedSnapshot = errors.New("revocation: unexpected snapshot")
)

// Subscriber consumes a revocation log and answers credential validity
// questions. It is safe for concurrent use.
//
// The zero value is not usable; construct one with [NewSubscriber].
type Subscriber struct {
	mu sync.RWMutex

	state      State
	generation int64
	nextSeq    int64

	revoked map[string]bool
	pending map[int64]Event

	applied Checkpoint
	latest  Checkpoint

	waitingGeneration int64
	snapshotQueued    bool

	store StateStore

	// snapshotReqs carries requested repair coordinates. Buffered at 1 and
	// coalesced so a request can never block the state machine.
	snapshotReqs chan Checkpoint
}

// NewSubscriber restores a subscriber from store. A nil store yields an
// in-memory subscriber that cannot survive restarts.
func NewSubscriber(store StateStore) (*Subscriber, error) {
	sub := &Subscriber{
		state:        StateSyncing,
		nextSeq:      1,
		revoked:      map[string]bool{},
		pending:      map[int64]Event{},
		snapshotReqs: make(chan Checkpoint, 1),
		store:        store,
	}
	if store == nil {
		sub.store = &MemoryStore{}
	}

	saved, ok, err := sub.store.Load()
	if err != nil {
		return nil, fmt.Errorf("revocation: load state: %w", err)
	}
	if !ok {
		return sub, nil
	}
	if saved.Version != 1 {
		return nil, fmt.Errorf("revocation: unsupported state version %d", saved.Version)
	}

	sub.state = saved.State
	sub.generation = saved.Generation
	sub.nextSeq = saved.NextSeq
	sub.revoked = cloneBoolMap(saved.Revoked)
	sub.pending = cloneEventMap(saved.Pending)
	sub.applied = saved.Applied
	sub.latest = saved.Latest
	sub.waitingGeneration = saved.WaitingGeneration

	// Restart is an untrusted moment: a previously synced view cannot claim
	// continuity until a fresh checkpoint proves the publisher has not moved.
	if sub.state == StateSynced {
		sub.state = StateSyncing
	}
	// Any repair that was in flight remains in flight after restart.
	if sub.state == StateGap {
		sub.enqueueSnapshotLocked()
	}
	return sub, nil
}

// ApplyEvent ingests one log event.
//
// Duplicate events are idempotent. Any event whose sequence does not equal
// the next expected dense position (including out-of-order delivery) opens a
// gap and requests a snapshot. Events arriving while a snapshot is in flight
// are buffered by sequence so the repair can be completed atomically.
func (s *Subscriber) ApplyEvent(event Event) error {
	if event.Generation <= 0 || event.Sequence <= 0 || event.CredentialID == "" {
		return ErrInvalidEvent
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if event.Generation > s.generation {
		s.adoptOrRolloverLocked(event.Generation)
	}

	switch {
	case event.Generation < s.generation:
		// Stale event from a retired generation: harmless and ignored.
		return nil
	case s.state == StateGap:
		// De-duplicate by sequence; every buffered event is re-validated once
		// the snapshot arrives.
		s.pending[event.Sequence] = event
	default:
		if event.Sequence < s.nextSeq {
			// Duplicate replay of an already applied event.
			return nil
		}
		if event.Sequence != s.nextSeq {
			s.enterGapLocked()
			s.pending[event.Sequence] = event
			break
		}
		s.applyOneLocked(event)
		s.recheckSyncedLocked()
	}
	return s.persistLocked()
}

// ApplyCheckpoint records the publisher's authoritative high-water mark.
//
// A checkpoint only upgrades the subscriber to synced when every event up to
// that coordinate has been applied contiguously. A checkpoint that jumps
// past the local position proves events are missing and opens a gap.
func (s *Subscriber) ApplyCheckpoint(cp Checkpoint) error {
	if cp.Generation <= 0 || cp.Sequence < 0 {
		return ErrInvalidCheckpoint
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if cp.Generation > s.generation {
		s.adoptOrRolloverLocked(cp.Generation)
	}
	if cp.Generation < s.generation {
		// Retired-generation checkpoint: never authoritative for the current
		// view.
		return nil
	}

	if cp.Generation > s.latest.Generation ||
		(cp.Generation == s.latest.Generation && cp.Sequence > s.latest.Sequence) {
		s.latest = cp
	}

	if s.state != StateGap {
		if s.nextSeq <= cp.Sequence {
			// The publisher is ahead: some events never reached us.
			s.enterGapLocked()
		} else {
			s.recheckSyncedLocked()
		}
	}
	return s.persistLocked()
}

// ApplySnapshot installs a full repair snapshot together with every event
// buffered during the repair, as a single atomic switch.
//
// Snapshots are accepted only while a gap is open and only when they answer
// the generation currently being repaired. A remaining discontinuity after
// installation keeps the subscriber in the denying gap state and requests
// another snapshot.
func (s *Subscriber) ApplySnapshot(snap Snapshot) error {
	if snap.Generation <= 0 || snap.LastSequence < 0 {
		return ErrInvalidSnapshot
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != StateGap {
		return ErrUnexpectedSnapshot
	}
	if snap.Generation != s.waitingGeneration {
		return fmt.Errorf("%w: want generation %d, got %d",
			ErrStaleSnapshot, s.waitingGeneration, snap.Generation)
	}

	// Build the post-repair world off-line so a failed validation never
	// exposes a half-rebuilt set and never discards buffered events.
	nextRevoked := cloneBoolMap(snap.Revoked)
	nextPending := map[int64]Event{}
	for seq, event := range s.pending {
		if event.Generation == snap.Generation && seq > snap.LastSequence {
			nextPending[seq] = event
		}
	}

	nextNextSeq := snap.LastSequence + 1
	for {
		event, ok := nextPending[nextNextSeq]
		if !ok {
			break
		}
		applyTo(nextRevoked, event)
		delete(nextPending, nextNextSeq)
		nextNextSeq++
	}

	if len(nextPending) > 0 {
		// Buffers are still discontinuous: keep the original buffer and gap
		// state untouched and request another snapshot.
		s.enqueueSnapshotLocked()
		return s.persistLocked()
	}

	// Atomic switch: all derived state changes become visible together.
	s.revoked = nextRevoked
	s.pending = nextPending
	s.generation = snap.Generation
	s.nextSeq = nextNextSeq
	s.applied = Checkpoint{Generation: snap.Generation, Sequence: nextNextSeq - 1}
	s.waitingGeneration = 0

	// The gap is repaired; whether the view is caught up depends on the
	// latest checkpoint.
	s.state = StateSyncing

	// Drain the consumed request and allow future repairs to be signaled.
	select {
	case <-s.snapshotReqs:
	default:
	}
	s.snapshotQueued = false

	s.recheckSyncedLocked()
	return s.persistLocked()
}

// SnapshotRequests returns the channel on which repair coordinates are
// announced. The channel is buffered and coalesced: at most one pending
// request exists at any time and it always carries the newest coordinate.
func (s *Subscriber) SnapshotRequests() <-chan Checkpoint {
	return s.snapshotReqs
}

// IsRevoked implements fail-closed judgment: a credential is reported revoked
// whenever the local view is not provably synced, as well as when it is
// present in the authoritative revocation set.
func (s *Subscriber) IsRevoked(credentialID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != StateSynced {
		return true
	}
	return s.revoked[credentialID]
}

// Valid is the inverse of [Subscriber.IsRevoked]; true means the credential
// is known-good against a trustworthy, caught-up view.
func (s *Subscriber) Valid(credentialID string) bool {
	return !s.IsRevoked(credentialID)
}

// Synced reports whether the subscriber has applied a contiguous run up to
// the latest known checkpoint. A stale subscriber (behind the checkpoint)
// always reports false.
func (s *Subscriber) Synced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == StateSynced
}

// CaughtUp is an explicit alias for [Subscriber.Synced] used by callers that
// think in checkpoint terms. Stale subscriptions can never report caught up.
func (s *Subscriber) CaughtUp() bool {
	return s.Synced()
}

// Status returns a consistent point-in-time snapshot of subscriber health.
func (s *Subscriber) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{
		State:            s.state,
		Generation:       s.generation,
		NextSequence:     s.nextSeq,
		Applied:          s.applied,
		Latest:           s.latest,
		SnapshotInFlight: s.state == StateGap,
	}
}

// Position returns the coordinate a stream should resume from.
func (s *Subscriber) Position() Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied
}

// --- internal helpers (callers hold s.mu) ---

// adoptOrRolloverLocked moves the subscriber to a newer generation. A fresh
// subscriber (generation 0) simply adopts the first generation it sees; an
// established subscriber treats the move as an explicit discontinuity and
// falls back to a full snapshot repair.
func (s *Subscriber) adoptOrRolloverLocked(generation int64) {
	if s.generation == 0 {
		s.generation = generation
		return
	}
	s.rolloverLocked(generation)
}

func (s *Subscriber) rolloverLocked(generation int64) {
	s.state = StateGap
	s.generation = generation
	s.nextSeq = 1
	s.revoked = map[string]bool{}
	s.pending = map[int64]Event{}
	s.applied = Checkpoint{}
	s.latest = Checkpoint{}
	s.enqueueSnapshotLocked()
}

func (s *Subscriber) enterGapLocked() {
	s.state = StateGap
	s.waitingGeneration = s.generation
	s.enqueueSnapshotLocked()
}

func (s *Subscriber) enqueueSnapshotLocked() {
	s.waitingGeneration = s.generation
	if s.snapshotQueued {
		// Refresh the coordinate without dropping the pending signal.
		select {
		case <-s.snapshotReqs:
		default:
		}
	}
	s.snapshotReqs <- Checkpoint{Generation: s.generation, Sequence: s.nextSeq - 1}
	s.snapshotQueued = true
}

func (s *Subscriber) applyOneLocked(event Event) {
	applyTo(s.revoked, event)
	s.generation = event.Generation
	s.nextSeq = event.Sequence + 1
	s.applied = Checkpoint{Generation: event.Generation, Sequence: event.Sequence}
}

// recheckSyncedLocked promotes to synced only when the applied run reaches
// the newest checkpoint for the current generation.
func (s *Subscriber) recheckSyncedLocked() {
	if s.state == StateGap {
		return
	}
	if s.latest.Generation == s.generation && s.nextSeq > s.latest.Sequence {
		s.state = StateSynced
		return
	}
	s.state = StateSyncing
}

func (s *Subscriber) persistLocked() error {
	state := PersistedState{
		Version:           1,
		State:             s.state,
		Generation:        s.generation,
		NextSeq:           s.nextSeq,
		Revoked:           cloneBoolMap(s.revoked),
		Pending:           cloneEventMap(s.pending),
		Applied:           s.applied,
		Latest:            s.latest,
		WaitingGeneration: s.waitingGeneration,
	}
	if err := s.store.Save(state); err != nil {
		return fmt.Errorf("revocation: persist state: %w", err)
	}
	return nil
}

func applyTo(set map[string]bool, event Event) {
	if event.Revoked {
		set[event.CredentialID] = true
		return
	}
	delete(set, event.CredentialID)
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneEventMap(in map[int64]Event) map[int64]Event {
	if len(in) == 0 {
		return map[int64]Event{}
	}
	out := make(map[int64]Event, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
