package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"strconv"
	"sync"
)

const (
	witnessKeyDomain = "netsec/migration-witness-key/v1"
	witnessDomain    = "netsec/migration-witness/v1"
)

// MigrationEntry is a package-issued proof that a record's old-generation
// index was checked against the supplied value and that the new-generation
// index was derived by this package. The witness is deliberately opaque:
// callers cannot set a completion flag or manufacture a valid entry.
//
// MigrationEntry contains no plaintext and is safe to pass to storage code.
type MigrationEntry struct {
	RecordID string
	Scope    Scope
	From     Generation
	To       Generation
	OldIndex Index
	NewIndex Index
	witness  [sha256.Size]byte
}

// BlindIndexer derives blind indexes and coordinates key rotation. Registered
// records contain only scopes and indexes; plaintext is not retained.
type BlindIndexer struct {
	keys  KeyHandle
	store RecordStore

	stateMu  sync.RWMutex
	current  Generation
	previous Generation
	retired  map[Generation]struct{}

	recordsMu sync.Mutex

	hooks *testHooks
}

// New creates an indexer backed by a new MemoryStore.
func New(keys KeyHandle, current Generation) (*BlindIndexer, error) {
	return NewWithStore(keys, current, NewMemoryStore())
}

// NewWithStore creates an indexer with a host-provided sanitized record store.
func NewWithStore(keys KeyHandle, current Generation, store RecordStore) (*BlindIndexer, error) {
	if keys == nil {
		return nil, ErrInvalidKey
	}
	if store == nil {
		return nil, ErrInvalidRecord
	}
	if current == 0 {
		return nil, ErrInvalidGeneration
	}
	if err := validateKey(keys, context.Background(), current); err != nil {
		return nil, err
	}
	return &BlindIndexer{
		keys:    keys,
		store:   store,
		current: current,
		retired: make(map[Generation]struct{}),
	}, nil
}

// CurrentGeneration returns the generation used for writes.
func (b *BlindIndexer) CurrentGeneration() Generation {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.current
}

// PreviousGeneration returns the previous generation, or zero when no
// rotation is active.
func (b *BlindIndexer) PreviousGeneration() Generation {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.previous
}

// Query returns the current-generation index followed by the previous-
// generation index while a rotation is active. After retirement it returns
// only the current index. The caller should use every returned index for a
// lookup during the migration window.
func (b *BlindIndexer) Query(ctx context.Context, scope Scope, value []byte) ([]Index, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	current, err := b.indexFor(ctx, b.current, scope, value)
	if err != nil {
		return nil, err
	}
	indexes := []Index{current}
	if b.previous != 0 {
		previous, err := b.indexFor(ctx, b.previous, scope, value)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, previous)
	}
	return indexes, nil
}

// QueryGeneration returns an index only when generation is active. In
// particular, once a previous generation retires, its query keys are rejected.
func (b *BlindIndexer) QueryGeneration(ctx context.Context, scope Scope, value []byte, generation Generation) (Index, error) {
	if err := scope.Validate(); err != nil {
		return Index{}, err
	}
	if generation == 0 {
		return Index{}, ErrInvalidGeneration
	}
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	switch generation {
	case b.current:
		return b.indexFor(ctx, generation, scope, value)
	case b.previous:
		if b.previous != 0 {
			return b.indexFor(ctx, generation, scope, value)
		}
		fallthrough
	default:
		if _, retired := b.retired[generation]; retired {
			return Index{}, ErrRetiredGeneration
		}
		return Index{}, ErrGenerationInactive
	}
}

// PutRecord computes and stores only the current-generation index. During a
// rotation a new or rewritten record is therefore immediately registered with
// the current generation and can never be stranded on the old one.
func (b *BlindIndexer) PutRecord(ctx context.Context, id string, scope Scope, value []byte) (Index, error) {
	if id == "" {
		return Index{}, ErrInvalidRecord
	}
	if err := scope.Validate(); err != nil {
		return Index{}, err
	}

	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	index, err := b.indexFor(ctx, b.current, scope, value)
	if err != nil {
		return Index{}, err
	}
	record := StoredRecord{
		ID:      id,
		Scope:   scope,
		Indexes: []Index{index},
	}
	if err := record.validate(); err != nil {
		return Index{}, err
	}

	if b.hooks != nil && b.hooks.beforePutStore != nil {
		b.hooks.beforePutStore()
	}
	b.recordsMu.Lock()
	err = b.store.Store(ctx, record)
	b.recordsMu.Unlock()
	if err != nil {
		return Index{}, err
	}
	return index, nil
}

// Record returns a registered record without plaintext.
func (b *BlindIndexer) Record(ctx context.Context, id string) (StoredRecord, error) {
	if id == "" {
		return StoredRecord{}, ErrInvalidRecord
	}
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	b.recordsMu.Lock()
	record, ok, err := b.store.Load(ctx, id)
	b.recordsMu.Unlock()
	if err != nil {
		return StoredRecord{}, err
	}
	if !ok {
		return StoredRecord{}, ErrRecordNotFound
	}
	return record.clone(), nil
}

// Records returns all registered sanitized records.
func (b *BlindIndexer) Records(ctx context.Context) ([]StoredRecord, error) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	b.recordsMu.Lock()
	records, err := b.store.List(ctx)
	b.recordsMu.Unlock()
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i] = records[i].clone()
	}
	return records, nil
}

// BeginRotation activates next for writes while retaining current for reads.
// Records registered before this point keep only their old index until they
// are migrated; records written after it receive only the new index.
func (b *BlindIndexer) BeginRotation(ctx context.Context, next Generation) error {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()

	if next <= b.current {
		return ErrGenerationOrder
	}
	if b.previous != 0 {
		return ErrRotationPending
	}
	if _, reused := b.retired[next]; reused {
		return ErrGenerationReused
	}
	if err := validateKey(b.keys, ctx, b.current); err != nil {
		return err
	}
	if err := validateKey(b.keys, ctx, next); err != nil {
		return err
	}
	b.previous = b.current
	b.current = next
	return nil
}

// PrepareMigration checks the record's old index against value and returns a
// package-issued witness for the current generation. It does not mark the
// migration complete. Call CommitMigration with the returned entry.
//
// The plaintext value is used only to derive indexes and is never passed to a
// RecordStore or stored in the returned entry.
func (b *BlindIndexer) PrepareMigration(ctx context.Context, id string, value []byte) (MigrationEntry, error) {
	if id == "" {
		return MigrationEntry{}, ErrInvalidRecord
	}

	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	if b.previous == 0 {
		return MigrationEntry{}, ErrNoPreviousGeneration
	}

	b.recordsMu.Lock()
	record, ok, err := b.store.Load(ctx, id)
	b.recordsMu.Unlock()
	if err != nil {
		return MigrationEntry{}, err
	}
	if !ok {
		return MigrationEntry{}, ErrRecordNotFound
	}
	if err := record.validate(); err != nil {
		return MigrationEntry{}, err
	}

	oldIndex, hasOld := record.IndexFor(b.previous)
	if !hasOld {
		return MigrationEntry{}, ErrAlreadyMigrated
	}
	oldKey, err := b.keyFor(ctx, b.previous)
	if err != nil {
		return MigrationEntry{}, err
	}
	computedOld, err := deriveIndex(oldKey, b.previous, record.Scope, value)
	if err != nil {
		return MigrationEntry{}, err
	}
	if !oldIndex.Equal(computedOld) {
		return MigrationEntry{}, ErrValueMismatch
	}

	newKey, err := b.keyFor(ctx, b.current)
	if err != nil {
		return MigrationEntry{}, err
	}
	newIndex, err := deriveIndex(newKey, b.current, record.Scope, value)
	if err != nil {
		return MigrationEntry{}, err
	}
	if existing, hasCurrent := record.IndexFor(b.current); hasCurrent && !existing.Equal(newIndex) {
		return MigrationEntry{}, ErrMigrationConflict
	}

	entry := MigrationEntry{
		RecordID: record.ID,
		Scope:    record.Scope,
		From:     b.previous,
		To:       b.current,
		OldIndex: oldIndex,
		NewIndex: newIndex,
	}
	if err := b.sealMigration(newKey, &entry); err != nil {
		return MigrationEntry{}, err
	}
	return entry, nil
}

// CommitMigration verifies the trusted witness and registers the current index.
// The witness binds the generation pair, record identity, scope, old index,
// and new index. A caller cannot assert completion without a valid entry.
func (b *BlindIndexer) CommitMigration(ctx context.Context, entry MigrationEntry) error {
	if entry.RecordID == "" {
		return ErrInvalidWitness
	}
	if err := entry.Scope.Validate(); err != nil {
		return ErrInvalidWitness
	}

	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	if b.previous != entry.From || b.current != entry.To {
		if _, retired := b.retired[entry.From]; retired {
			return ErrRetiredGeneration
		}
		return ErrStaleMigration
	}

	newKey, err := b.keyFor(ctx, entry.To)
	if err != nil {
		return err
	}
	if err := b.verifyMigration(newKey, entry); err != nil {
		return err
	}
	if b.hooks != nil && b.hooks.beforeMigrationCommit != nil {
		b.hooks.beforeMigrationCommit()
	}

	b.recordsMu.Lock()
	record, ok, err := b.store.Load(ctx, entry.RecordID)
	if err != nil {
		b.recordsMu.Unlock()
		return err
	}
	if !ok {
		b.recordsMu.Unlock()
		return ErrRecordNotFound
	}
	if record.Scope != entry.Scope {
		b.recordsMu.Unlock()
		return ErrStaleMigration
	}
	if _, hasOld := record.IndexFor(entry.From); !hasOld {
		b.recordsMu.Unlock()
		return ErrStaleMigration
	}
	if current, hasCurrent := record.IndexFor(entry.To); hasCurrent {
		b.recordsMu.Unlock()
		if current.Equal(entry.NewIndex) {
			return nil
		}
		return ErrMigrationConflict
	}

	record.Indexes = append(record.Indexes, entry.NewIndex)
	if err := record.validate(); err != nil {
		b.recordsMu.Unlock()
		return err
	}
	err = b.store.Store(ctx, record)
	b.recordsMu.Unlock()
	return err
}

// MigrateRecord prepares and commits a migration in one host-side operation.
// It still requires the exact registered plaintext to obtain a valid witness.
func (b *BlindIndexer) MigrateRecord(ctx context.Context, id string, value []byte) (MigrationEntry, error) {
	entry, err := b.PrepareMigration(ctx, id, value)
	if err != nil {
		return MigrationEntry{}, err
	}
	if err := b.CommitMigration(ctx, entry); err != nil {
		return MigrationEntry{}, err
	}
	return entry, nil
}

// VerifyMigration checks the witness and active rotation without modifying the
// store. It is useful to hosts that need to validate an entry before applying
// it in their own transaction.
func (b *BlindIndexer) VerifyMigration(ctx context.Context, entry MigrationEntry) error {
	if entry.RecordID == "" {
		return ErrInvalidWitness
	}
	if err := entry.Scope.Validate(); err != nil {
		return ErrInvalidWitness
	}

	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	if b.previous != entry.From || b.current != entry.To {
		if _, retired := b.retired[entry.From]; retired {
			return ErrRetiredGeneration
		}
		return ErrStaleMigration
	}
	newKey, err := b.keyFor(ctx, entry.To)
	if err != nil {
		return err
	}
	return b.verifyMigration(newKey, entry)
}

// RetirePrevious atomically verifies that every registered record already has
// the current index, removes old indexes, and makes old query keys invalid.
// Writes and commits in progress hold the state read lock, so retirement
// cannot race past an unregistered write or migration.
func (b *BlindIndexer) RetirePrevious(ctx context.Context) error {
	if b.hooks != nil && b.hooks.retireAttempt != nil {
		b.hooks.retireAttempt()
	}
	b.stateMu.Lock()
	defer b.stateMu.Unlock()

	if b.previous == 0 {
		return ErrNoPreviousGeneration
	}

	b.recordsMu.Lock()
	defer b.recordsMu.Unlock()

	records, err := b.store.List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := record.validate(); err != nil {
			return err
		}
		if _, ok := record.IndexFor(b.current); !ok {
			return ErrMigrationPending
		}
	}

	retiring := b.previous
	for _, record := range records {
		current, _ := record.IndexFor(b.current)
		record.Indexes = []Index{current}
		if err := b.store.Store(ctx, record); err != nil {
			return err
		}
	}

	b.retired[retiring] = struct{}{}
	b.previous = 0
	return nil
}

func (b *BlindIndexer) indexFor(ctx context.Context, generation Generation, scope Scope, value []byte) (Index, error) {
	key, err := b.keyFor(ctx, generation)
	if err != nil {
		return Index{}, err
	}
	return deriveIndex(key, generation, scope, value)
}

func (b *BlindIndexer) keyFor(ctx context.Context, generation Generation) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := b.keys.Key(ctx, generation)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}
	return key, nil
}

func validateKey(keys KeyHandle, ctx context.Context, generation Generation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := keys.Key(ctx, generation)
	if err != nil {
		return err
	}
	if len(key) == 0 {
		return ErrInvalidKey
	}
	return nil
}

func (b *BlindIndexer) sealMigration(newKey []byte, entry *MigrationEntry) error {
	mac, err := migrationMAC(newKey, *entry)
	if err != nil {
		return err
	}
	entry.witness = mac
	return nil
}

func (b *BlindIndexer) verifyMigration(newKey []byte, entry MigrationEntry) error {
	expected, err := migrationMAC(newKey, entry)
	if err != nil {
		return ErrInvalidWitness
	}
	if !hmac.Equal(expected[:], entry.witness[:]) {
		return ErrInvalidWitness
	}
	return nil
}

func migrationMAC(newKey []byte, entry MigrationEntry) ([sha256.Size]byte, error) {
	if entry.RecordID == "" || entry.From == 0 || entry.To == 0 ||
		entry.OldIndex.Generation != entry.From ||
		entry.NewIndex.Generation != entry.To {
		return [sha256.Size]byte{}, ErrInvalidWitness
	}
	if err := entry.Scope.Validate(); err != nil {
		return [sha256.Size]byte{}, ErrInvalidWitness
	}

	keyMAC := hmac.New(sha256.New, newKey)
	_, _ = keyMAC.Write([]byte(witnessKeyDomain))
	witnessKey := keyMAC.Sum(nil)

	message := make([]byte, 0, 256)
	message = appendLengthPrefixed(message, []byte(witnessDomain))
	message = appendLengthPrefixed(message, []byte(entry.RecordID))
	message = appendLengthPrefixed(message, []byte(strconv.FormatUint(uint64(entry.From), 10)))
	message = appendLengthPrefixed(message, []byte(strconv.FormatUint(uint64(entry.To), 10)))
	message = appendLengthPrefixed(message, []byte(entry.Scope.Tenant))
	message = appendLengthPrefixed(message, []byte(entry.Scope.Collection))
	message = appendLengthPrefixed(message, []byte(entry.Scope.Field))
	message = appendLengthPrefixed(message, entry.OldIndex.Digest[:])
	message = appendLengthPrefixed(message, entry.NewIndex.Digest[:])

	mac := hmac.New(sha256.New, witnessKey)
	_, _ = mac.Write(message)
	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}

// testHooks lets in-package tests deterministically interleave lifecycle
// operations. They are always nil in normal use.
type testHooks struct {
	beforePutStore        func()
	beforeMigrationCommit func()
	retireAttempt         func()
}
