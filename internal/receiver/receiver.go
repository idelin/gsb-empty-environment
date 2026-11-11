// Package receiver implements a persistent checkpoint acceptor for a software
// update client that must keep trusting a transparent log even while offline.
//
// The receiver only advances its locally stored tree head after it has:
//
//  1. verified the checkpoint signature against a pinned key and origin,
//  2. verified a Merkle consistency proof against the previously accepted head,
//  3. atomically and durably committed the new head to disk.
//
// Security properties enforced here:
//
//   - Monotonicity: a checkpoint smaller than the accepted head is rejected as
//     stale; accepted roots can never move backwards.
//   - Equivocation freeze: distinct roots at the same tree size freeze all
//     further updates and persist both signed checkpoints as evidence.
//   - Crash safety: state is written to a temp file and atomically renamed over
//     the state file, with fsync at every step, so a power loss never exposes a
//     torn or older state.
//   - Concurrency safety: an in-process mutex plus an advisory file lock make
//     concurrent download threads (and separate processes) serialize, so no
//     thread can roll back an already accepted root.
package receiver

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"example.org/offlinecli/internal/checkpoint"
	"example.org/offlinecli/internal/tlog"
)

const (
	stateFileName    = "checkpoint-state.json"
	lockFileName     = "checkpoint.lock"
	tempStatePattern = ".checkpoint-state.tmp-*"
	conflictPrefix   = "conflict-"
	conflictSuffix   = ".json"
	stateVersion     = 1
)

// Sentinel errors. Inspect with errors.Is.
var (
	// ErrStale is returned when an offered checkpoint is older than the head
	// already accepted.
	ErrStale = errors.New("receiver: stale checkpoint (tree size moved backwards)")

	// ErrFrozen is wrapped once equivocation has been detected; while frozen the
	// receiver refuses every new checkpoint.
	ErrFrozen = errors.New("receiver: frozen due to conflicting roots")

	// ErrConflict is returned when two different roots appear at one size.
	ErrConflict = errors.New("receiver: conflicting roots at identical tree size")
)

// Fetcher abstracts a (possibly remote or malicious) mirror serving
// checkpoints and consistency proofs. Implementations must be concurrency safe.
type Fetcher interface {
	// Latest returns the freshest signed checkpoint the mirror offers.
	Latest() ([]byte, error)

	// Consistency returns a proof that the tree of size newSize extends the
	// previously trusted tree of size oldSize.
	Consistency(oldSize, newSize int64) ([]tlog.Hash, error)
}

// Receiver is a durable, concurrency-safe checkpoint acceptor.
type Receiver struct {
	mu       sync.Mutex
	dir      string
	verifier *checkpoint.Verifier
	lockFile *os.File
	gate     chan struct{}

	// crashHook, when non-nil, runs after the temp state file is fsynced but
	// before the rename, to simulate power loss in tests.
	crashHook func()
}

// Open opens (or initializes) a receiver rooted at dir, bound to one origin and
// one trusted Ed25519 public key. The directory is created if absent.
func Open(dir string, origin checkpoint.Origin, publicKey ed25519.PublicKey) (*Receiver, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("receiver: empty state directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("receiver: create state dir: %w", err)
	}

	verifier, err := checkpoint.NewVerifier("update-log", origin, publicKey)
	if err != nil {
		return nil, err
	}

	r := &Receiver{dir: dir, verifier: verifier}
	if err := r.acquireLock(); err != nil {
		return nil, err
	}
	if err := r.cleanupTempFiles(); err != nil {
		r.releaseLock()
		return nil, err
	}
	return r, nil
}

// Close releases the advisory lock without erasing any state.
func (r *Receiver) Close() error {
	return r.releaseLock()
}

// Current returns the most recently accepted checkpoint without any network
// access. The boolean is false until a first head is accepted; that offline
// availability is what lets a disconnected client keep using a seen root.
func (r *Receiver) Current() (checkpoint.Checkpoint, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, err := r.readState()
	if err != nil {
		return checkpoint.Checkpoint{}, false, err
	}
	if state == nil {
		return checkpoint.Checkpoint{}, false, nil
	}
	return checkpoint.Checkpoint{
		Origin: checkpoint.Origin(state.Origin),
		Size:   state.Size,
		Hash:   state.Hash,
	}, true, nil
}

// Frozen reports whether equivocation has put the receiver into its permanent
// freeze state.
func (r *Receiver) Frozen() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.readState()
	if err != nil {
		return false, err
	}
	return state != nil && state.Frozen, nil
}

// Dir exposes the state directory (used by tests and for evidence inspection).
func (r *Receiver) Dir() string { return r.dir }

// setCrashHook installs a power-loss simulation hook (test only).
func (r *Receiver) setCrashHook(hook func()) { r.crashHook = hook }
