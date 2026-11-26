package kt

import (
	"crypto/ed25519"
	"sync"
)

// Verifier is the thread-safe, importable multi-epoch transparency client.
//
// It ingests a consecutive 1..8 epoch signed-root chain plus per-epoch
// migration logs, enforces the resurrection guard over a revocation graveyard
// and verifies cross-epoch append-only consistency and dual-auditor views.
type Verifier struct {
	mu sync.RWMutex

	dirKey     ed25519.PublicKey
	issuers    []ed25519.PublicKey
	labelDepth int

	roots      map[uint32]*SignedRoot
	migrations map[uint32]*MigrationLog
	trees      map[uint32]*Tree

	// graveyard holds every subtree hash ever revoked-and-migrated-out. Any
	// later reuse is a resurrection attempt and is rejected.
	graveyard map[Hash]struct{}

	// revokedPrefixes records prefixes once revoked, so their reappearance via
	// split/merge demands fresh authorization.
	revokedPrefixes map[string]struct{}

	// prefixHistory records all subtree hashes ever seen at a prefix, to
	// reject silent reuse of pre-revocation subtrees as "new" subtrees.
	prefixHistory map[string]map[Hash]struct{}

	// usedNonces / lastSerial defeat authorization replay.
	usedNonces map[string]struct{}
	lastSerial map[string]uint64

	locked      bool
	lockReason  error
	lastCounter uint64
	nextEpoch   uint32
}

// NewVerifier constructs a verifier pinned to a directory key, a set of
// authorized migration issuers and the fixed label depth (in bits).
func NewVerifier(dirKey ed25519.PublicKey, issuers []ed25519.PublicKey, labelDepth int) (*Verifier, error) {
	if len(dirKey) != ed25519.PublicKeySize {
		return nil, ErrBadKey
	}
	if labelDepth < 0 || labelDepth > MaxLabelBits {
		return nil, ErrLabelTooLong
	}
	iss := make([]ed25519.PublicKey, len(issuers))
	for i, k := range issuers {
		if len(k) != ed25519.PublicKeySize {
			return nil, ErrBadKey
		}
		iss[i] = append(ed25519.PublicKey(nil), k...)
	}
	return &Verifier{
		dirKey:          append(ed25519.PublicKey(nil), dirKey...),
		issuers:         iss,
		labelDepth:      labelDepth,
		roots:           map[uint32]*SignedRoot{},
		migrations:      map[uint32]*MigrationLog{},
		trees:           map[uint32]*Tree{},
		graveyard:       map[Hash]struct{}{},
		revokedPrefixes: map[string]struct{}{},
		prefixHistory:   map[string]map[Hash]struct{}{},
		usedNonces:      map[string]struct{}{},
		lastSerial:      map[string]uint64{},
		nextEpoch:       FirstEpoch,
	}, nil
}

// Locked reports whether the verifier has hard-locked (e.g. after a fork).
func (v *Verifier) Locked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.locked
}

// LockReason returns the error that caused the lock, if any.
func (v *Verifier) LockReason() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lockReason
}

func (v *Verifier) failLocked(err error) error {
	v.locked = true
	v.lockReason = err
	return err
}

// IngestEpoch atomically verifies and stores one epoch. The signed root's
// signature, previous-root link and counter are checked; the migration log is
// fully validated against oldTree (previous epoch) and newTree (current
// epoch). When present the tree roots must match the signed roots.
func (v *Verifier) IngestEpoch(sr *SignedRoot, log *MigrationLog, oldTree, newTree *Tree) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return ErrVerifierLocked
	}
	if sr == nil || log == nil {
		return v.failLocked(ErrNilEntry)
	}
	if err := sr.VerifySignature(v.dirKey); err != nil {
		return v.failLocked(err)
	}
	body := sr.Body
	if body.Epoch != v.nextEpoch {
		return v.failLocked(ErrEpochSequence)
	}
	if body.Epoch > FirstEpoch+MaxEpochs-1 {
		return v.failLocked(ErrEpochRange)
	}
	if body.RevocCounter < v.lastCounter {
		return v.failLocked(ErrCounterNotMonotonic)
	}
	if body.Epoch != FirstEpoch {
		prev, ok := v.roots[body.Epoch-1]
		if !ok || body.PrevRoot != prev.Body.Hash() {
			return v.failLocked(ErrPrevRoot)
		}
	} else if body.PrevRoot != (Hash{}) {
		return v.failLocked(ErrPrevRoot)
	}
	if log.Epoch != body.Epoch {
		return v.failLocked(ErrMigrationEpoch)
	}
	if log.Hash() != body.MigrationHash {
		return v.failLocked(ErrMigrationHash)
	}
	if newTree != nil && (newTree.Epoch() != body.Epoch || newTree.RootHash() != body.TreeRoot) {
		return v.failLocked(ErrRootMismatch)
	}
	if oldTree != nil {
		if oldTree.Epoch() != body.Epoch-1 {
			return v.failLocked(ErrRootMismatch)
		}
		if prev, ok := v.roots[body.Epoch-1]; ok && oldTree.RootHash() != prev.Body.TreeRoot {
			return v.failLocked(ErrRootMismatch)
		}
	}
	if err := v.validateMigrations(body.Epoch, log, oldTree, newTree, body.RevocCounter); err != nil {
		return v.failLocked(err)
	}
	v.roots[body.Epoch] = sr
	v.migrations[body.Epoch] = log
	if newTree != nil {
		v.trees[body.Epoch] = newTree
	}
	v.lastCounter = body.RevocCounter
	v.nextEpoch = body.Epoch + 1
	return nil
}

// EpochRoot returns the stored signed root for an epoch, or nil.
func (v *Verifier) EpochRoot(epoch uint32) *SignedRoot {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.roots[epoch]
}

// RevocCounter returns the latest accepted revocation counter.
func (v *Verifier) RevocCounter() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastCounter
}
