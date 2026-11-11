package receiver

import (
	"encoding/json"
	"errors"
	"fmt"

	"example.org/offlinecli/internal/checkpoint"
	"example.org/offlinecli/internal/tlog"
)

// persistedState is the on-disk JSON representation of the accepted head.
type persistedState struct {
	Version int       `json:"version"`
	Size    int64     `json:"size"`
	Hash    tlog.Hash `json:"hash"`
	Origin  string    `json:"origin"`
	Note    []byte    `json:"signed_note"`
	Frozen  bool      `json:"frozen"`
}

// conflictRecord is the tamper evidence written when roots equivocate.
type conflictRecord struct {
	Reason      string `json:"reason"`
	Size        int64  `json:"size"`
	Accepted    []byte `json:"accepted_signed_note"`
	Conflicting []byte `json:"conflicting_signed_note"`
}

// Observe validates and possibly accepts a signed checkpoint. proof is only
// consulted when the offered head extends an already accepted one.
//
//   - accepted is true when the on-disk head moved to a new, larger tree;
//   - accepted is false with nil error for an idempotent re-delivery;
//   - a non-nil error means the offer was rejected and the head did not move.
func (r *Receiver) Observe(signedNote []byte, proof []tlog.Hash) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	env, err := r.verifier.Parse(signedNote)
	if err != nil {
		return false, fmt.Errorf("receiver: verify signature: %w", err)
	}

	state, err := r.readState()
	if err != nil {
		return false, err
	}

	// Already frozen: refuse everything, surfacing the stored conflict.
	if state != nil && state.Frozen {
		return false, fmt.Errorf("%w: head remains at size %d", ErrFrozen, state.Size)
	}

	// First head ever seen: trust the signed root and persist it.
	if state == nil {
		if err := r.commitHead(env, false); err != nil {
			return false, err
		}
		return true, nil
	}

	switch {
	case env.Size < state.Size:
		// A mirror rewound. Never move the accepted root backwards.
		return false, fmt.Errorf("%w: offered size %d < accepted size %d",
			ErrStale, env.Size, state.Size)

	case env.Size == state.Size:
		if env.Hash == state.Hash {
			// Idempotent re-delivery of the exact same head.
			return false, nil
		}
		// Equivocation: same size, different root. Freeze and keep evidence.
		if ferr := r.freeze(env); ferr != nil {
			return false, ferr
		}
		return false, fmt.Errorf("%w: size %d", ErrConflict, env.Size)

	default:
		// Larger tree: the consistency proof must bind it to the trusted head.
		if cerr := tlog.CheckConsistency(state.Size, env.Size, state.Hash, env.Hash, proof); cerr != nil {
			return false, fmt.Errorf("receiver: consistency proof rejected: %w", cerr)
		}
		if err := r.commitHead(env, false); err != nil {
			return false, err
		}
		return true, nil
	}
}

// ObserveFrom fetches the freshest checkpoint from mirror and, when needed, the
// matching consistency proof, then feeds them to Observe. It is the entry point
// a download thread uses.
func (r *Receiver) ObserveFrom(mirror Fetcher) (bool, error) {
	signedNote, err := mirror.Latest()
	if err != nil {
		return false, fmt.Errorf("receiver: fetch checkpoint: %w", err)
	}

	env, err := r.verifier.Parse(signedNote)
	if err != nil {
		return false, fmt.Errorf("receiver: verify signature: %w", err)
	}

	// Determine whether a consistency proof is required before taking the lock
	// in Observe. Observe re-validates everything, so this read is advisory.
	r.mu.Lock()
	state, err := r.readState()
	r.mu.Unlock()
	if err != nil {
		return false, err
	}

	var proof []tlog.Hash
	if state != nil && !state.Frozen && env.Size > state.Size {
		proof, err = mirror.Consistency(state.Size, env.Size)
		if err != nil {
			return false, fmt.Errorf("receiver: fetch proof: %w", err)
		}
	}

	return r.Observe(signedNote, proof)
}

// commitHead durably and atomically stores the accepted envelope as new state.
func (r *Receiver) commitHead(env *checkpoint.Envelope, frozen bool) error {
	state := persistedState{
		Version: stateVersion,
		Size:    env.Size,
		Hash:    env.Hash,
		Origin:  string(env.Origin),
		Note:    append([]byte{}, env.Text...),
		Frozen:  frozen,
	}
	return r.writeState(state)
}

// freeze writes conflict evidence and marks the existing head as frozen. The
// accepted head itself is left unchanged.
func (r *Receiver) freeze(offered *checkpoint.Envelope) error {
	state, err := r.readState()
	if err != nil {
		return err
	}

	evidence := conflictRecord{
		Reason:      "two distinct signed roots observed at the same tree size",
		Size:        offered.Size,
		Accepted:    append([]byte{}, state.Note...),
		Conflicting: append([]byte{}, offered.Text...),
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}

	// Mark the state frozen first so that an interruption during evidence
	// writing still leaves the receiver frozen (fail-closed). The evidence file
	// has a deterministic, size-derived name, so it can be (re)written on any
	// later equivocation attempt at the same size.
	state.Frozen = true
	if err := r.writeState(*state); err != nil {
		return err
	}

	evidencePath := conflictPath(r.dir, offered.Size)
	if err := writeFileAtomic(r.dir, evidencePath, data, r.crashHook); err != nil {
		return fmt.Errorf("receiver: persist conflict evidence: %w", err)
	}
	return nil
}

// readState loads and integrity-checks the persisted state. A missing file
// yields (nil, nil).
func (r *Receiver) readState() (*persistedState, error) {
	path := statePath(r.dir)
	data, err := readFile(path)
	if errors.Is(err, errFileNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("receiver: corrupt state file: %w", err)
	}
	if state.Version != stateVersion {
		return nil, fmt.Errorf("receiver: unsupported state version %d", state.Version)
	}
	if state.Size < 1 {
		return nil, errors.New("receiver: corrupt state: non-positive tree size")
	}
	if checkpoint.Origin(state.Origin) != r.verifier.Origin() {
		return nil, errors.New("receiver: state origin does not match pinned origin")
	}

	// Re-verify the stored signed note so tampering with the JSON is detected.
	env, err := r.verifier.Parse(state.Note)
	if err != nil {
		return nil, fmt.Errorf("receiver: stored note no longer verifies: %w", err)
	}
	if env.Size != state.Size || env.Hash != state.Hash {
		return nil, errors.New("receiver: state fields do not match signed note")
	}
	return &state, nil
}

// writeState serializes state and atomically replaces the state file.
func (r *Receiver) writeState(state persistedState) error {
	state.Version = stateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(r.dir, statePath(r.dir), data, r.crashHook); err != nil {
		return err
	}
	return nil
}
