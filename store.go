package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"unicode"
	"unicode/utf8"
)

const maxObjectIDBytes = 128

// Token identifies one staged creation transaction. Every mutation of a staged
// object must present the token returned by CreateObject, so a failed or
// concurrent creator cannot abort another creator's object.
type Token string

// ObjectMeta is the non-label part of a staged resource.
type ObjectMeta struct {
	ID       string
	Kind     string
	ParentID string
	Data     []byte
}

// ObjectRecord is a committed object together with its final authorization
// labels. It is only returned by Store.Get after the object has been published.
type ObjectRecord struct {
	ObjectMeta
	Labels      Labels
	LabelDigest LabelDigest
}

// Store stages a resource, persists its final labels, and only then publishes
// the object to readers.
//
// The split between staging and Publish is deliberate: an implementation must
// never expose a staged object through Get. A store that cannot express this
// atomic visibility transition is not suitable as a resource host backing
// store.
type Store interface {
	// CreateObject creates an invisible staged object and returns the token
	// that owns it.
	CreateObject(ctx context.Context, meta ObjectMeta) (Token, error)
	// PersistLabels attaches the already validated final label set and its
	// digest to the staged object identified by token.
	PersistLabels(ctx context.Context, token Token, labels Labels, digest LabelDigest) error
	// Publish atomically makes a fully labelled staged object visible.
	Publish(ctx context.Context, token Token) error
	// Abort discards a staged object. It must not affect committed objects or
	// staged objects owned by another token.
	Abort(ctx context.Context, token Token) error
	// Get returns only committed, fully labelled objects.
	Get(ctx context.Context, id string) (ObjectRecord, error)
}

// Operation names a store operation that can be made to fail in tests.
type Operation string

const (
	OpCreateObject  Operation = "create-object"
	OpPersistLabels Operation = "persist-labels"
	OpPublish       Operation = "publish"
	OpAbort         Operation = "abort"
)

// Faults injects errors at store operation boundaries. A fault is returned
// before the operation mutates state. Tests use this to model failures after an
// earlier creation step has succeeded (for example, a failure while persisting
// labels after the object shell exists).
type Faults struct {
	CreateObject  error
	PersistLabels error
	Publish       error
	Abort         error
}

func (f Faults) forOperation(op Operation) error {
	switch op {
	case OpCreateObject:
		return f.CreateObject
	case OpPersistLabels:
		return f.PersistLabels
	case OpPublish:
		return f.Publish
	case OpAbort:
		return f.Abort
	default:
		return nil
	}
}

type pendingRecord struct {
	meta      ObjectMeta
	token     Token
	labels    Labels
	digest    LabelDigest
	labelsSet bool
}

// StoreStats is a snapshot of staging and committed object counts.
type StoreStats struct {
	Pending   int
	Committed int
}

// MemoryStore is an in-memory Store with fault injection.
//
// Staged objects live in a separate map from committed objects. Get only
// consults the committed map, so readers cannot observe an object while its
// labels are missing, being persisted, or awaiting publication.
type MemoryStore struct {
	mu sync.RWMutex

	pendingByID    map[string]*pendingRecord
	pendingByToken map[Token]*pendingRecord
	committed      map[string]ObjectRecord

	faults     Faults
	nextFaults map[Operation]error
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty, concurrency-safe in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		pendingByID:    make(map[string]*pendingRecord),
		pendingByToken: make(map[Token]*pendingRecord),
		committed:      make(map[string]ObjectRecord),
		nextFaults:     make(map[Operation]error),
	}
}

// SetFaults installs persistent fault injection. A nil fault disables injection
// for that operation.
func (s *MemoryStore) SetFaults(f Faults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = f
}

// ClearFaults disables all persistent and one-shot fault injection.
func (s *MemoryStore) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = Faults{}
	s.nextFaults = make(map[Operation]error)
}

// FailNext makes the next invocation of op fail with err. The fault is consumed
// by that invocation.
func (s *MemoryStore) FailNext(op Operation, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.nextFaults, op)
		return
	}
	s.nextFaults[op] = err
}

// Stats reports the number of staged and committed objects.
func (s *MemoryStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StoreStats{Pending: len(s.pendingByID), Committed: len(s.committed)}
}

// HasPending reports whether an identifier currently has an invisible staged
// object. It is intended for diagnostics and tests, not for external readers.
func (s *MemoryStore) HasPending(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.pendingByID[id]
	return ok
}

func (s *MemoryStore) faultLocked(op Operation) error {
	if err, ok := s.nextFaults[op]; ok {
		delete(s.nextFaults, op)
		return err
	}
	return s.faults.forOperation(op)
}

// CreateObject implements Store.
func (s *MemoryStore) CreateObject(ctx context.Context, meta ObjectMeta) (Token, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateObjectMeta(meta); err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.faultLocked(OpCreateObject); err != nil {
		return "", err
	}
	if _, ok := s.committed[meta.ID]; ok {
		return "", fmt.Errorf("%w: %q", ErrObjectExists, meta.ID)
	}
	if _, ok := s.pendingByID[meta.ID]; ok {
		return "", fmt.Errorf("%w: %q", ErrObjectExists, meta.ID)
	}

	pending := &pendingRecord{
		meta: ObjectMeta{
			ID:       meta.ID,
			Kind:     meta.Kind,
			ParentID: meta.ParentID,
			Data:     cloneBytes(meta.Data),
		},
		token: token,
	}
	s.pendingByID[meta.ID] = pending
	s.pendingByToken[token] = pending
	return token, nil
}

// PersistLabels implements Store.
func (s *MemoryStore) PersistLabels(ctx context.Context, token Token, labels Labels, digest LabelDigest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := labels.Validate(); err != nil {
		return err
	}
	if want := labels.Digest(); digest != want {
		return fmt.Errorf("%w: supplied digest does not match labels", ErrDigestMismatch)
	}
	labels = labels.Clone()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.faultLocked(OpPersistLabels); err != nil {
		return err
	}
	pending, ok := s.pendingByToken[token]
	if !ok {
		return fmt.Errorf("%w: unknown or completed transaction", ErrTransactionMismatch)
	}
	if pending.labelsSet {
		return fmt.Errorf("%w: labels already persisted", ErrTransactionMismatch)
	}
	pending.labels = labels
	pending.digest = digest
	pending.labelsSet = true
	return nil
}

// Publish implements Store.
func (s *MemoryStore) Publish(ctx context.Context, token Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.faultLocked(OpPublish); err != nil {
		return err
	}
	pending, ok := s.pendingByToken[token]
	if !ok {
		return fmt.Errorf("%w: unknown or completed transaction", ErrTransactionMismatch)
	}
	if !pending.labelsSet {
		return fmt.Errorf("%w: refusing to publish %q without labels", ErrIncompleteRecord, pending.meta.ID)
	}
	if want := pending.labels.Digest(); pending.digest != want {
		return fmt.Errorf("%w: refusing to publish %q", ErrDigestMismatch, pending.meta.ID)
	}
	if _, exists := s.committed[pending.meta.ID]; exists {
		return fmt.Errorf("%w: %q", ErrObjectExists, pending.meta.ID)
	}

	record := ObjectRecord{
		ObjectMeta:  pending.meta,
		Labels:      pending.labels.Clone(),
		LabelDigest: pending.digest,
	}
	record.Data = cloneBytes(record.Data)
	s.committed[record.ID] = record
	delete(s.pendingByID, pending.meta.ID)
	delete(s.pendingByToken, token)
	return nil
}

// Abort implements Store. Aborting a missing or already completed transaction
// is a no-op; it can never remove a committed object.
func (s *MemoryStore) Abort(ctx context.Context, token Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.faultLocked(OpAbort); err != nil {
		return err
	}
	pending, ok := s.pendingByToken[token]
	if !ok {
		return nil
	}
	delete(s.pendingByID, pending.meta.ID)
	delete(s.pendingByToken, token)
	return nil
}

// Get implements Store. Staged objects are deliberately reported as not found.
func (s *MemoryStore) Get(ctx context.Context, id string) (ObjectRecord, error) {
	if err := ctx.Err(); err != nil {
		return ObjectRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.committed[id]
	if !ok {
		return ObjectRecord{}, fmt.Errorf("%w: %q", ErrObjectNotFound, id)
	}
	if err := validateObjectMeta(record.ObjectMeta); err != nil {
		return ObjectRecord{}, err
	}
	if err := record.Labels.Validate(); err != nil {
		return ObjectRecord{}, err
	}
	if want := record.Labels.Digest(); record.LabelDigest != want {
		return ObjectRecord{}, fmt.Errorf("%w: committed object %q", ErrDigestMismatch, id)
	}
	record.Labels = record.Labels.Clone()
	record.Data = cloneBytes(record.Data)
	return record, nil
}

func newToken() (Token, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate transaction token: %w", err)
	}
	return Token(hex.EncodeToString(raw[:])), nil
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func validateObjectMeta(meta ObjectMeta) error {
	if meta.ID == "" {
		return fmt.Errorf("%w: empty object ID", ErrInvalidRequest)
	}
	if err := validateMetadataString("object ID", meta.ID); err != nil {
		return err
	}
	if meta.Kind == "" {
		return fmt.Errorf("%w: empty object kind", ErrInvalidRequest)
	}
	if err := validateMetadataString("object kind", meta.Kind); err != nil {
		return err
	}
	if meta.ParentID != "" {
		if err := validateMetadataString("parent ID", meta.ParentID); err != nil {
			return err
		}
	}
	if meta.ParentID == meta.ID {
		return fmt.Errorf("%w: object cannot inherit from itself", ErrInvalidRequest)
	}
	return nil
}

func validateMetadataString(field, value string) error {
	if len(value) > maxObjectIDBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidRequest, field, maxObjectIDBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidRequest, field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidRequest, field)
		}
	}
	return nil
}
