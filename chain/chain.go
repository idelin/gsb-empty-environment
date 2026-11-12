// Package chain validates did:plc operation logs submitted to the directory
// service. Cryptographic primitives are intentionally injected by the caller:
// signature verification and DAG-CBOR CID hashing are not performed here.
package chain

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RecoveryWindow bounds how long a competing operation may supersede an
// operation already linked at the same prev. After the window elapses the
// earlier operation is final and history cannot be rewritten.
const RecoveryWindow = 72 * time.Hour

// MinRotationKeys and MaxRotationKeys bound the rotation key list carried by
// every operation, including the genesis operation.
const (
	MinRotationKeys = 1
	MaxRotationKeys = 5
)

// Key is an opaque rotation key identifier. The package never interprets its
// contents; equality and list position are the only semantics it relies on.
type Key string

// CID is an opaque content identifier produced by the injected Hasher.
type CID string

// Operation is the minimal view of a did:plc operation the validator needs.
// Callers adapt their concrete (e.g. DAG-CBOR decoded) operation type to it.
type Operation interface {
	// Prev returns the CID of the preceding operation, or an empty CID for
	// the genesis operation of a log.
	Prev() CID
	// RotationKeys returns the ordered rotation keys. Lower index means
	// higher priority when competing operations are resolved.
	RotationKeys() []Key
	// SignedBytes returns the canonical bytes covered by the signature,
	// forwarded verbatim to the injected SignatureVerifier.
	SignedBytes() []byte
	// Signature returns the raw signature bytes over SignedBytes.
	Signature() []byte
}

// Hasher computes the CID of an operation. In production this is the
// DAG-CBOR multihash of the canonical operation encoding.
type Hasher interface {
	Hash(op Operation) (CID, error)
}

// SignatureVerifier checks a signature against a candidate rotation key.
// The validator invokes it for each key in the prev operation's rotation
// list and stops at the first key that validates, so the matching key's
// list position determines fork priority.
type SignatureVerifier interface {
	Verify(op Operation, key Key) (bool, error)
}

// Status of an operation record in the audit log.
type Status int

const (
	// StatusValid means the operation is part of the current active log.
	StatusValid Status = iota + 1
	// StatusInvalid means the operation (and its branch) was superseded by
	// a higher-priority competing operation. It remains in the audit log.
	StatusInvalid
)

// Record is an audit entry. SupersededBy is empty while the record is valid
// and holds the CID of the competing operation that invalidated it.
type Record struct {
	CID          CID
	Op           Operation
	Status       Status
	SigningKey   Key
	CreatedAt    time.Time
	SupersededBy CID
}

// Sentinel errors returned by Submit. Use errors.Is to match them.
var (
	ErrNilOperation        = errors.New("chain: operation is nil")
	ErrRotationKeysRange   = errors.New("chain: rotationKeys count must be between 1 and 5")
	ErrDuplicateCID        = errors.New("chain: operation CID already in log")
	ErrGenesisExists       = errors.New("chain: genesis operation already exists")
	ErrMissingPrev         = errors.New("chain: non-genesis operation requires a prev CID")
	ErrOrphanPrev          = errors.New("chain: prev CID does not reference an existing operation")
	ErrPrevInvalidated     = errors.New("chain: prev references an invalidated operation")
	ErrUnauthorizedKey     = errors.New("chain: signature not valid under any prev rotation key")
	ErrSamePriorityFork    = errors.New("chain: competing key is not strictly higher priority")
	ErrRecoveryWindowClose = errors.New("chain: superseded operation is older than the 72h recovery window")
)

// Validator serializes operation submissions and retains the full audit log,
// including branches invalidated by higher-priority forks. It is safe for
// concurrent use.
type Validator struct {
	hasher   Hasher
	verifier SignatureVerifier
	now      func() time.Time

	mu     sync.Mutex
	order  []CID
	record map[CID]*Record
	// children maps a prev CID to the CIDs that were submitted against it,
	// in submission order.
	children map[CID][]CID
	head     CID
}

// Option customizes a Validator.
type Option func(*Validator)

// WithClock injects a time source, primarily for exercising the recovery
// window boundary in tests. The default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(v *Validator) { v.now = now }
}

// NewValidator builds a Validator with the injected hashing and signature
// primitives.
func NewValidator(hasher Hasher, verifier SignatureVerifier, opts ...Option) *Validator {
	v := &Validator{
		hasher:   hasher,
		verifier: verifier,
		now:      time.Now,
		record:   make(map[CID]*Record),
		children: make(map[CID][]CID),
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Submit validates op against the current log and, on success, links it.
//
// A genesis operation has an empty prev. Any other operation must point at
// an existing, still-valid operation and must be signed by one of that
// operation's rotation keys. At the same prev, a competing operation is only
// accepted when its signing key sits strictly earlier in the prev's rotation
// list and the operation it replaces is at most RecoveryWindow old; the
// replaced branch is marked invalid but retained for audit. Everything else
// is rejected with a wrapped sentinel error and leaves the log untouched.
func (v *Validator) Submit(op Operation) (CID, error) {
	if op == nil {
		return "", ErrNilOperation
	}
	if n := len(op.RotationKeys()); n < MinRotationKeys || n > MaxRotationKeys {
		return "", ErrRotationKeysRange
	}

	id, err := v.hasher.Hash(op)
	if err != nil {
		return "", fmt.Errorf("chain: hash operation: %w", err)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, exists := v.record[id]; exists {
		return "", ErrDuplicateCID
	}

	now := v.now()
	prev := op.Prev()
	if prev == "" {
		if v.head != "" {
			return "", ErrGenesisExists
		}
		signingKey, err := v.authorize(op, op.RotationKeys())
		if err != nil {
			return "", err
		}
		v.store(id, op, "", signingKey, now)
		v.head = id
		return id, nil
	}

	parent, ok := v.record[prev]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrOrphanPrev, prev)
	}
	if parent.Status != StatusValid {
		return "", fmt.Errorf("%w: %s", ErrPrevInvalidated, prev)
	}

	signingKey, err := v.authorize(op, parent.Op.RotationKeys())
	if err != nil {
		return "", err
	}

	if old := v.validChild(prev); old != nil {
		oldIndex, err := keyIndex(parent.Op.RotationKeys(), old.SigningKey)
		if err != nil {
			return "", err
		}
		newIndex, err := keyIndex(parent.Op.RotationKeys(), signingKey)
		if err != nil {
			return "", err
		}
		if newIndex >= oldIndex {
			return "", ErrSamePriorityFork
		}
		if now.Sub(old.CreatedAt) > RecoveryWindow {
			return "", ErrRecoveryWindowClose
		}
		v.invalidateBranch(old.CID, id)
	}

	v.store(id, op, prev, signingKey, now)
	headRec, ok := v.record[v.head]
	if !ok || v.head == prev || headRec.Status != StatusValid {
		v.head = id
	}
	return id, nil
}

// authorize returns the highest-priority rotation key under which op's
// signature verifies. Keys are tried in list order and the first match wins.
func (v *Validator) authorize(op Operation, keys []Key) (Key, error) {
	for _, key := range keys {
		ok, err := v.verifier.Verify(op, key)
		if err != nil {
			return "", fmt.Errorf("chain: verify signature: %w", err)
		}
		if ok {
			return key, nil
		}
	}
	return "", ErrUnauthorizedKey
}

// validChild returns the currently-valid operation linked at prev, if any.
func (v *Validator) validChild(prev CID) *Record {
	for _, id := range v.children[prev] {
		if rec := v.record[id]; rec != nil && rec.Status == StatusValid {
			return rec
		}
	}
	return nil
}

// invalidateBranch marks oldCID and every operation descended from it as
// invalid, recording the superseding CID for the audit trail.
func (v *Validator) invalidateBranch(oldCID, replacedBy CID) {
	stack := []CID{oldCID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rec := v.record[id]
		if rec == nil || rec.Status == StatusInvalid {
			continue
		}
		rec.Status = StatusInvalid
		rec.SupersededBy = replacedBy
		stack = append(stack, v.children[id]...)
	}
}

// store records a newly accepted operation.
func (v *Validator) store(id CID, op Operation, prev CID, signingKey Key, at time.Time) {
	v.record[id] = &Record{
		CID:        id,
		Op:         op,
		Status:     StatusValid,
		SigningKey: signingKey,
		CreatedAt:  at,
	}
	v.order = append(v.order, id)
	if prev != "" {
		v.children[prev] = append(v.children[prev], id)
	}
}

// Audit returns copies of all records in submission order, including
// invalidated branches retained for the audit trail.
func (v *Validator) Audit() []Record {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Record, 0, len(v.order))
	for _, id := range v.order {
		out = append(out, *v.record[id])
	}
	return out
}

// Lookup returns a copy of the record for id, or false when it is unknown.
func (v *Validator) Lookup(id CID) (Record, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	rec, ok := v.record[id]
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

// Head returns the CID of the current tip of the active log, or an empty CID
// before any operation has been accepted.
func (v *Validator) Head() CID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.head
}

// keyIndex returns the position of key in keys.
func keyIndex(keys []Key, key Key) (int, error) {
	for i, candidate := range keys {
		if candidate == key {
			return i, nil
		}
	}
	return -1, fmt.Errorf("chain: signing key %q absent from prev rotation keys", key)
}
