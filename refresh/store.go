package refresh

import (
	"errors"
	"sync"
)

// Storage-level sentinel errors.
var (
	// ErrTokenNotFound is returned when the presented refresh token is unknown.
	ErrTokenNotFound = errors.New("refresh: token not found")
	// ErrFamilyNotFound is returned when a family operation targets an unknown family.
	ErrFamilyNotFound = errors.New("refresh: token family not found")
	// ErrStoreCrashed is returned by a store that has crashed and must be reopened.
	ErrStoreCrashed = errors.New("refresh: storage has crashed")
	// ErrSimulatedCrash is raised by fault injection to emulate a process crash.
	ErrSimulatedCrash = errors.New("refresh: simulated process crash")
)

// TokenStatus is the lifecycle state of a refresh token within its family.
type TokenStatus int

const (
	TokenUnused TokenStatus = iota
	TokenConsumed
	TokenRevoked
)

func (s TokenStatus) String() string {
	switch s {
	case TokenUnused:
		return "unused"
	case TokenConsumed:
		return "consumed"
	default:
		return "revoked"
	}
}

// EventKind classifies an entry in the durable, append-only family event log.
type EventKind int

const (
	EventIssued EventKind = iota
	EventRotation
	EventRevocation
)

// Event is one durable mutation. Sequence numbers are allocated inside the
// transaction that produces the event, so a committed log is gap-free and
// defines the single total order of refresh and revocation actions.
type Event struct {
	Seq            int64
	Kind           EventKind
	FamilyID       string
	OldTokenID     string
	NewTokenID     string
	IdempotencyKey string
	Reason         string
}

type familyRecord struct {
	id      string
	revoked bool
	version int64
}

type tokenRecord struct {
	id         string
	familyID   string
	status     TokenStatus
	rotationID string
}

type rotationRecord struct {
	id             string
	familyID       string
	oldTokenID     string
	idempotencyKey string
	newTokenID     string
	result         RefreshResult
	seq            int64
}

// diskData is the durable database image. All mutations happen on a clone;
// the clone only replaces the live pointer once the transaction commits, so a
// crash before commit leaves no partial state behind.
type diskData struct {
	families  map[string]familyRecord
	tokens    map[string]tokenRecord
	rotations map[string]rotationRecord
	bindings  map[string]string // oldTokenID + NUL + idempotencyKey -> rotationID
	keyOwners map[string]string // idempotencyKey -> oldTokenID
	events    []Event
	seq       int64
}

func newDiskData() *diskData {
	return &diskData{
		families:  map[string]familyRecord{},
		tokens:    map[string]tokenRecord{},
		rotations: map[string]rotationRecord{},
		bindings:  map[string]string{},
		keyOwners: map[string]string{},
	}
}

func (d *diskData) clone() *diskData {
	cp := &diskData{
		families:  make(map[string]familyRecord, len(d.families)),
		tokens:    make(map[string]tokenRecord, len(d.tokens)),
		rotations: make(map[string]rotationRecord, len(d.rotations)),
		bindings:  make(map[string]string, len(d.bindings)),
		keyOwners: make(map[string]string, len(d.keyOwners)),
		events:    make([]Event, len(d.events)),
		seq:       d.seq,
	}
	for k, v := range d.families {
		cp.families[k] = v
	}
	for k, v := range d.tokens {
		cp.tokens[k] = v
	}
	for k, v := range d.rotations {
		cp.rotations[k] = v
	}
	for k, v := range d.bindings {
		cp.bindings[k] = v
	}
	for k, v := range d.keyOwners {
		cp.keyOwners[k] = v
	}
	copy(cp.events, d.events)
	return cp
}

func bindingKey(oldTokenID, idempotencyKey string) string {
	return oldTokenID + "\x00" + idempotencyKey
}

// Disk is the durable medium. It outlives individual store instances, so a
// crashed store can be reopened against the same Disk and observe every
// committed write.
type Disk struct {
	data *diskData
}

// NewDisk creates an empty durable medium.
func NewDisk() *Disk {
	return &Disk{data: newDiskData()}
}

// CrashableStore is a copy-on-write in-memory store that emulates a durable
// database with atomic commit and crash semantics.
type CrashableStore struct {
	mu      sync.Mutex
	disk    *Disk
	crashed bool
	// beforeCommit, when non-nil, is evaluated after the transaction body
	// succeeds but before the working set becomes durable. Returning true
	// simulates a pre-commit crash: no state is published.
	beforeCommit func() bool
	// afterCommit, when non-nil, is evaluated right after the working set has
	// been published durably. Returning true simulates a crash between durable
	// commit and response delivery.
	afterCommit func() bool
}

// NewCrashableStore opens a store over the given durable medium.
func NewCrashableStore(disk *Disk) *CrashableStore {
	return &CrashableStore{disk: disk}
}

// WithCrashBeforeCommit installs a one-shot pre-commit crash hook.
func (s *CrashableStore) WithCrashBeforeCommit(fn func() bool) *CrashableStore {
	s.beforeCommit = fn
	return s
}

// WithCrashAfterCommit installs a one-shot post-commit crash hook.
func (s *CrashableStore) WithCrashAfterCommit(fn func() bool) *CrashableStore {
	s.afterCommit = fn
	return s
}

// Reopen opens a fresh, healthy store over the same durable medium. All
// committed data is visible; the crash flag and uncommitted faults are reset.
func (s *CrashableStore) Reopen() *CrashableStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &CrashableStore{disk: s.disk}
}

// TokenView is an immutable snapshot of a token row.
type TokenView struct {
	ID         string
	FamilyID   string
	Status     TokenStatus
	RotationID string
}

// FamilyView is an immutable snapshot of a token family row.
type FamilyView struct {
	ID      string
	Revoked bool
	Version int64
}

// RotationView is an immutable snapshot of a committed rotation, including the
// exact response that was returned to the first caller.
type RotationView struct {
	ID             string
	FamilyID       string
	OldTokenID     string
	IdempotencyKey string
	NewTokenID     string
	Result         RefreshResult
	Seq            int64
}

// Store is the persistence contract used by the refresh service. A real
// backend only needs to provide one consistent read and one atomic
// read-modify-write transaction.
type Store interface {
	// LoadToken returns the token and its family in one consistent read.
	LoadToken(tokenID string) (TokenView, FamilyView, error)
	// Apply runs fn against a private working copy. When fn returns nil the
	// working copy is published atomically; any other error aborts the
	// transaction and leaves durable state untouched.
	Apply(fn func(tx *Tx) error) error
}

// LoadToken implements Store.
func (s *CrashableStore) LoadToken(tokenID string) (TokenView, FamilyView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return TokenView{}, FamilyView{}, ErrStoreCrashed
	}
	tok, ok := s.disk.data.tokens[tokenID]
	if !ok {
		return TokenView{}, FamilyView{}, ErrTokenNotFound
	}
	fam := s.disk.data.families[tok.familyID]
	return TokenView{
			ID:         tok.id,
			FamilyID:   tok.familyID,
			Status:     tok.status,
			RotationID: tok.rotationID,
		}, FamilyView{
			ID:      fam.id,
			Revoked: fam.revoked,
			Version: fam.version,
		}, nil
}

// Apply implements Store.
func (s *CrashableStore) Apply(fn func(tx *Tx) error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrStoreCrashed
	}
	working := s.disk.data.clone()
	tx := &Tx{data: working}
	if err := fn(tx); err != nil {
		return err // abort: the working copy is discarded
	}
	if s.beforeCommit != nil && s.beforeCommit() {
		s.crashed = true
		s.beforeCommit = nil
		panic(ErrSimulatedCrash) // durable pointer is unchanged: nothing was committed
	}
	s.disk.data = working
	if s.afterCommit != nil && s.afterCommit() {
		s.crashed = true
		s.afterCommit = nil
		panic(ErrSimulatedCrash) // committed durably, but the response is lost
	}
	return tx.callerErr
}

// snapshot returns a deep copy of the durable image for assertions and
// post-crash inspection.
func (s *CrashableStore) snapshot() diskData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.disk.data.clone()
}

// Tx mutates a private working copy inside an Apply transaction.
type Tx struct {
	data *diskData
	// callerErr carries an error that the transaction wants delivered to the
	// caller even though the working copy is committed. It models "persist the
	// security action, then reject the request" (for example family reuse).
	callerErr error
}

// RejectAfterCommit marks the transaction for a normal commit while arranging
// for Apply to return err to the caller. Durable mutations made in this
// transaction are never rolled back.
func (tx *Tx) RejectAfterCommit(err error) {
	tx.callerErr = err
}

func (tx *Tx) token(id string) (tokenRecord, bool) {
	t, ok := tx.data.tokens[id]
	return t, ok
}

func (tx *Tx) family(id string) (familyRecord, bool) {
	f, ok := tx.data.families[id]
	return f, ok
}

func (tx *Tx) rotationByBinding(oldTokenID, key string) (rotationRecord, bool) {
	rotationID, ok := tx.data.bindings[bindingKey(oldTokenID, key)]
	if !ok {
		return rotationRecord{}, false
	}
	return tx.data.rotations[rotationID], true
}

func (tx *Tx) keyOwner(key string) (string, bool) {
	owner, ok := tx.data.keyOwners[key]
	return owner, ok
}

func (tx *Tx) putToken(t tokenRecord) {
	tx.data.tokens[t.id] = t
}

func (tx *Tx) putFamily(f familyRecord) {
	tx.data.families[f.id] = f
}

func (tx *Tx) putRotation(r rotationRecord) {
	tx.data.rotations[r.id] = r
	tx.data.bindings[bindingKey(r.oldTokenID, r.idempotencyKey)] = r.id
	tx.data.keyOwners[r.idempotencyKey] = r.oldTokenID
}

func (tx *Tx) appendEvent(kind EventKind, familyID, oldTokenID, newTokenID, key, reason string) {
	tx.data.seq++
	tx.data.events = append(tx.data.events, Event{
		Seq:            tx.data.seq,
		Kind:           kind,
		FamilyID:       familyID,
		OldTokenID:     oldTokenID,
		NewTokenID:     newTokenID,
		IdempotencyKey: key,
		Reason:         reason,
	})
}

// revokeFamily marks the family revoked, bumps its version, revokes every
// token that belongs to it and logs the revocation. Re-revoking a family is a
// no-op so the event log keeps at most one revocation per family.
func (tx *Tx) revokeFamily(familyID, reason string) {
	fam, ok := tx.data.families[familyID]
	if !ok || fam.revoked {
		return
	}
	fam.revoked = true
	fam.version++
	tx.data.families[familyID] = fam
	for id, tok := range tx.data.tokens {
		if tok.familyID == familyID && tok.status != TokenRevoked {
			tok.status = TokenRevoked
			tx.data.tokens[id] = tok
		}
	}
	tx.appendEvent(EventRevocation, familyID, "", "", "", reason)
}
