package round

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Kind identifies one immutable round event.
type Kind string

const (
	// EvRoundCreated is the first event of every round and carries the roster.
	EvRoundCreated Kind = "round_created"
	// EvCommitAccepted is the first, binding commitment for a member.
	EvCommitAccepted Kind = "commit_accepted"
	// EvCommitDuplicate records an identical repeated commitment.
	EvCommitDuplicate Kind = "commit_duplicate"
	// EvCommitConflict records a different commitment refused for a member.
	EvCommitConflict Kind = "commit_conflict"
	// EvRevealRecorded stores a reveal, possibly before its commitment exists.
	EvRevealRecorded Kind = "reveal_recorded"
	// EvRevealDuplicate records an identical repeated reveal.
	EvRevealDuplicate Kind = "reveal_duplicate"
	// EvRevealConflict records a different reveal refused for a member.
	EvRevealConflict Kind = "reveal_conflict"
	// EvRevealMatched marks a reveal that reproduces the member's commitment.
	EvRevealMatched Kind = "reveal_matched"
	// EvRevealMismatch records a reveal that fails commitment verification.
	EvRevealMismatch Kind = "reveal_mismatch"
	// EvRejected records a structurally invalid or foreign message.
	EvRejected Kind = "message_rejected"
	// EvRoundCompleted carries the jointly derived seed.
	EvRoundCompleted Kind = "round_completed"
	// EvRoundAborted terminates a round that cannot complete.
	EvRoundAborted Kind = "round_aborted"
)

// Evidence retains proof of a refused or conflicting message.
type Evidence struct {
	// ExistingDigest is the first accepted commitment, when applicable.
	ExistingDigest Digest `json:"existing_digest,omitempty"`
	// Offending is the canonical JSON encoding of the refused message.
	Offending json.RawMessage `json:"offending,omitempty"`
	// RawHash summarizes a message whose full payload must not be retained,
	// for example one addressed to a different round.
	RawHash Digest `json:"raw_hash,omitempty"`
}

// Event is one durable, append-only state transition.
type Event struct {
	Seq      int64      `json:"seq"`
	At       time.Time  `json:"at"`
	Kind     Kind       `json:"kind"`
	RoundID  string     `json:"round_id"`
	Member   MemberID   `json:"member,omitempty"`
	Members  []MemberID `json:"members,omitempty"`
	Commit   Digest     `json:"commit,omitempty"`
	Secret   []byte     `json:"secret,omitempty"`
	SecretOK bool       `json:"secret_ok,omitempty"`
	Seed     Digest     `json:"seed,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	Evidence *Evidence  `json:"evidence,omitempty"`
}

func canonicalJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("round: canonical encoding failed: %w", err)
	}
	return payload, nil
}

func mustCanonical(value any) json.RawMessage {
	payload, err := canonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func hashCanonical(value any) Digest {
	payload, err := canonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return sha256.Sum256(payload)
}

// appendConflict records a refused message in the conflict ledger.
func (r *Round) appendConflict(ev *Event) {
	r.conflicts = append(r.conflicts, Conflict{
		Member:   ev.Member,
		Kind:     string(ev.Kind),
		Reason:   ev.Reason,
		EventSeq: ev.Seq,
	})
}

// applyLocked folds one event into the in-memory state without touching the
// store. It is used for both live events and replay, so replay reconstructs
// exactly the state a live round would have reached.
func (r *Round) applyLocked(ev *Event) error {
	switch ev.Kind {
	case EvRoundCreated:
		// Roster was installed by the constructor; nothing else to do.
	case EvCommitAccepted:
		r.states[ev.Member].commit = cloneBytes(ev.Commit[:])
	case EvCommitDuplicate:
		// Idempotent repeat: state is unchanged.
	case EvCommitConflict:
		r.appendConflict(ev)
	case EvRevealRecorded:
		r.states[ev.Member].reveal = cloneBytes(ev.Secret)
	case EvRevealDuplicate:
		// Idempotent repeat: state is unchanged.
	case EvRevealConflict:
		r.appendConflict(ev)
	case EvRevealMatched:
		state := r.states[ev.Member]
		if len(state.reveal) == 0 {
			state.reveal = cloneBytes(ev.Secret)
		}
		state.matched = true
	case EvRevealMismatch:
		r.appendConflict(ev)
	case EvRejected:
		r.appendConflict(ev)
	case EvRoundCompleted:
		secrets := make(map[MemberID][]byte, len(r.sorted))
		for _, member := range r.sorted {
			state := r.states[member]
			if !state.matched || len(state.reveal) == 0 {
				return fmt.Errorf("%w: completion event while %q has no match", ErrCorruptLog, member)
			}
			secrets[member] = state.reveal
		}
		expected, err := deriveSeed(r.cfg.ID, r.sorted, secrets)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorruptLog, err)
		}
		if expected != ev.Seed {
			return fmt.Errorf("%w: stored seed does not match recomputed seed", ErrCorruptLog)
		}
		r.status = StatusCompleted
		r.seed = ev.Seed
		r.hasSeed = true
	case EvRoundAborted:
		r.status = StatusAborted
	default:
		return fmt.Errorf("%w: unknown event kind %q", ErrCorruptLog, ev.Kind)
	}
	return nil
}

// emit durably appends an event and only then folds it into memory. On a
// store failure the round is marked broken: its in-memory view may be behind
// the durable log, so further writes are refused until Replay.
func (r *Round) emit(ctx context.Context, ev *Event) (*Event, error) {
	if r.broken {
		return nil, ErrStoreBroken
	}
	ev.Seq = r.seq + 1
	ev.RoundID = r.cfg.ID
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if err := r.store.Append(ctx, ev); err != nil {
		r.broken = true
		return nil, err
	}
	r.seq = ev.Seq
	if err := r.applyLocked(ev); err != nil {
		r.broken = true
		return nil, err
	}

	switch ev.Kind {
	case EvCommitAccepted:
		// A reveal may have arrived first and be waiting for this commitment.
		if err := r.resolvePendingReveal(ctx, ev.Member); err != nil {
			return nil, err
		}
	case EvRevealMatched:
		if err := r.maybeComplete(ctx); err != nil {
			return nil, err
		}
	}
	return ev, nil
}

// resolvePendingReveal verifies a stored reveal against a newly arrived
// commitment, emitting a matched or mismatch event with evidence.
func (r *Round) resolvePendingReveal(ctx context.Context, member MemberID) error {
	state := r.states[member]
	if len(state.commit) == 0 || len(state.reveal) == 0 || state.matched {
		return nil
	}
	var committed Digest
	copy(committed[:], state.commit)
	msg := RevealMessage{RoundID: r.cfg.ID, Member: member, Secret: cloneBytes(state.reveal)}
	if Commitment(r.cfg.ID, member, state.reveal) != committed {
		_, err := r.emit(ctx, &Event{
			Kind:     EvRevealMismatch,
			Member:   member,
			Secret:   cloneBytes(state.reveal),
			Evidence: &Evidence{ExistingDigest: committed, Offending: mustCanonical(msg)},
		})
		return err
	}
	_, err := r.emit(ctx, &Event{
		Kind:     EvRevealMatched,
		Member:   member,
		Secret:   cloneBytes(state.reveal),
		SecretOK: true,
	})
	return err
}

// Replay reconstructs a round from its durable event log and verifies every
// transition, including an independent recomputation of any stored seed. A
// torn tail write is tolerated by the EventStore implementation; any other
// inconsistency yields ErrCorruptLog.
func Replay(ctx context.Context, store EventStore) (*Round, error) {
	events, err := store.Read(ctx)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, ErrEmptyLog
	}
	first := events[0]
	if first.Kind != EvRoundCreated {
		return nil, fmt.Errorf("%w: log starts with %q instead of %q", ErrCorruptLog, first.Kind, EvRoundCreated)
	}
	cfg := Config{
		ID:      first.RoundID,
		Members: append([]MemberID(nil), first.Members...),
	}
	sorted, err := cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptLog, err)
	}

	r := newRound(cfg, sorted, store)
	for _, ev := range events {
		if ev.Seq != r.seq+1 {
			return nil, fmt.Errorf("%w: expected seq %d, got %d", ErrCorruptLog, r.seq+1, ev.Seq)
		}
		if ev.RoundID != cfg.ID {
			return nil, fmt.Errorf("%w: event %d names round %q", ErrCorruptLog, ev.Seq, ev.RoundID)
		}
		if ev.At.IsZero() {
			return nil, fmt.Errorf("%w: event %d has no timestamp", ErrCorruptLog, ev.Seq)
		}
		if err := r.applyLocked(&ev); err != nil {
			return nil, err
		}
		r.seq = ev.Seq
	}
	// A power cut may have happened after the final match was durable but
	// before the completion event's fsync. The seed depends only on the
	// durable matched secrets, so append the deterministic completion now.
	if r.status == StatusOpen {
		if err := r.maybeComplete(ctx); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Events returns a defensive copy of the durable events in sequence order.
func (r *Round) Events(ctx context.Context) ([]Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	events, err := r.store.Read(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(events))
	copy(out, events)
	return out, nil
}
