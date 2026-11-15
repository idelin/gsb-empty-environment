package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type testKeyHandle struct {
	mu    sync.Mutex
	keys  map[Generation][]byte
	calls []Generation
}

func newTestKeys(generations ...Generation) *testKeyHandle {
	keys := make(map[Generation][]byte, len(generations))
	for _, generation := range generations {
		sum := sha256.Sum256([]byte("test-key-" + strconv.FormatUint(uint64(generation), 10)))
		keys[generation] = append([]byte(nil), sum[:]...)
	}
	return &testKeyHandle{keys: keys}
}

func (k *testKeyHandle) Key(ctx context.Context, generation Generation) ([]byte, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, generation)
	key, ok := k.keys[generation]
	if !ok {
		return nil, fmt.Errorf("missing test key for generation %d", generation)
	}
	return append([]byte(nil), key...), nil
}

func (k *testKeyHandle) callCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.calls)
}

func mustIndexer(t *testing.T, generations ...Generation) (*BlindIndexer, *MemoryStore, *testKeyHandle) {
	t.Helper()
	store := NewMemoryStore()
	keys := newTestKeys(generations...)
	indexer, err := NewWithStore(keys, 1, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	return indexer, store, keys
}

func mustPut(t *testing.T, indexer *BlindIndexer, id string, scope Scope, value []byte) Index {
	t.Helper()
	index, err := indexer.PutRecord(context.Background(), id, scope, value)
	if err != nil {
		t.Fatalf("PutRecord(%q): %v", id, err)
	}
	return index
}

func mustRecord(t *testing.T, indexer *BlindIndexer, id string) StoredRecord {
	t.Helper()
	record, err := indexer.Record(context.Background(), id)
	if err != nil {
		t.Fatalf("Record(%q): %v", id, err)
	}
	return record
}

func TestBlindIndexSeparatesScopeAndSupportsEmptyValue(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1)
	ctx := context.Background()
	value := []byte("shared plaintext")

	base := Scope{Tenant: "tenant-a", Collection: "users", Field: "email"}
	baseIndexes, err := indexer.Query(ctx, base, value)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(baseIndexes) != 1 {
		t.Fatalf("got %d indexes, want 1", len(baseIndexes))
	}

	sameIndexes, err := indexer.Query(ctx, base, value)
	if err != nil {
		t.Fatalf("Query same: %v", err)
	}
	if !baseIndexes[0].Equal(sameIndexes[0]) {
		t.Fatal("same scope and value produced different indexes")
	}

	variants := []struct {
		name  string
		scope Scope
	}{
		{name: "tenant", scope: Scope{Tenant: "tenant-b", Collection: "users", Field: "email"}},
		{name: "collection", scope: Scope{Tenant: "tenant-a", Collection: "admins", Field: "email"}},
		{name: "field", scope: Scope{Tenant: "tenant-a", Collection: "users", Field: "phone"}},
	}
	for _, test := range variants {
		t.Run(test.name, func(t *testing.T) {
			indexes, err := indexer.Query(ctx, test.scope, value)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if indexes[0].Equal(baseIndexes[0]) {
				t.Fatal("plaintext became correlatable across a scope boundary")
			}
		})
	}

	emptyIndexes, err := indexer.Query(ctx, base, nil)
	if err != nil {
		t.Fatalf("Query empty value: %v", err)
	}
	emptyAgain, err := indexer.Query(ctx, base, []byte{})
	if err != nil {
		t.Fatalf("Query empty byte slice: %v", err)
	}
	if !emptyIndexes[0].Equal(emptyAgain[0]) {
		t.Fatal("nil and empty values should encode identically")
	}
	if emptyIndexes[0].Equal(baseIndexes[0]) {
		t.Fatal("empty and non-empty values must differ")
	}
}

func TestEmptyDomainsRejected(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1)
	ctx := context.Background()

	scopes := []struct {
		name  string
		scope Scope
		want  error
	}{
		{name: "tenant", scope: Scope{Collection: "c", Field: "f"}, want: ErrEmptyTenant},
		{name: "collection", scope: Scope{Tenant: "t", Field: "f"}, want: ErrEmptyCollection},
		{name: "field", scope: Scope{Tenant: "t", Collection: "c"}, want: ErrEmptyField},
	}
	for _, test := range scopes {
		t.Run(test.name, func(t *testing.T) {
			if _, err := indexer.Query(ctx, test.scope, nil); !errors.Is(err, test.want) {
				t.Fatalf("Query error = %v, want %v", err, test.want)
			}
			if _, err := EncodeComponents(1, test.scope, nil); !errors.Is(err, test.want) {
				t.Fatalf("EncodeComponents error = %v, want %v", err, test.want)
			}
		})
	}

	valid := Scope{Tenant: "t", Collection: "c", Field: "f"}
	if _, err := EncodeComponents(0, valid, nil); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("generation zero error = %v", err)
	}
	if _, err := indexer.QueryGeneration(ctx, valid, nil, 0); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("QueryGeneration zero error = %v", err)
	}
	if _, err := New(KeyFunc(func(context.Context, Generation) ([]byte, error) { return nil, nil }), 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty key error = %v", err)
	}
}

func TestLengthPrefixesPreventEncodingCollisions(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1)
	ctx := context.Background()

	tests := []struct {
		name  string
		left  Scope
		right Scope
		lv    []byte
		rv    []byte
	}{
		{
			name:  "tenant_collection_boundary",
			left:  Scope{Tenant: "ab", Collection: "c", Field: "d"},
			right: Scope{Tenant: "a", Collection: "bc", Field: "d"},
			lv:    []byte("e"),
			rv:    []byte("e"),
		},
		{
			name:  "field_value_boundary",
			left:  Scope{Tenant: "t", Collection: "c", Field: "f"},
			right: Scope{Tenant: "t", Collection: "c", Field: "fo"},
			lv:    []byte("oo"),
			rv:    []byte("o"),
		},
		{
			name:  "collection_field_boundary",
			left:  Scope{Tenant: "t", Collection: "ab", Field: "c"},
			right: Scope{Tenant: "t", Collection: "a", Field: "bc"},
			lv:    nil,
			rv:    nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, err := EncodeComponents(1, test.left, test.lv)
			if err != nil {
				t.Fatalf("encode left: %v", err)
			}
			right, err := EncodeComponents(1, test.right, test.rv)
			if err != nil {
				t.Fatalf("encode right: %v", err)
			}
			if string(left) == string(right) {
				t.Fatal("length-prefixed encodings collided")
			}

			leftIndexes, err := indexer.Query(ctx, test.left, test.lv)
			if err != nil {
				t.Fatalf("left query: %v", err)
			}
			rightIndexes, err := indexer.Query(ctx, test.right, test.rv)
			if err != nil {
				t.Fatalf("right query: %v", err)
			}
			if leftIndexes[0].Equal(rightIndexes[0]) {
				t.Fatal("different component boundaries produced the same blind index")
			}
		})
	}
}

func TestGenerationIsBoundIntoHMACInput(t *testing.T) {
	key := sha256.Sum256([]byte("one key reused across generations"))
	keys := KeyFunc(func(context.Context, Generation) ([]byte, error) {
		return append([]byte(nil), key[:]...), nil
	})
	indexer, err := New(keys, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}
	value := []byte("value")

	first, err := indexer.Query(ctx, scope, value)
	if err != nil {
		t.Fatalf("Query generation 1: %v", err)
	}
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	dual, err := indexer.Query(ctx, scope, value)
	if err != nil {
		t.Fatalf("Query generation 2: %v", err)
	}
	if dual[0].Generation != 2 || dual[1].Generation != 1 {
		t.Fatalf("query generations = %d,%d", dual[0].Generation, dual[1].Generation)
	}
	if dual[0].Equal(dual[1]) || dual[0].Equal(first[0]) {
		t.Fatal("generation was not bound into the HMAC input")
	}
}

func TestRotationQueriesBothGenerationsAndWritesOnlyCurrent(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}
	value := []byte("same value")

	oldIndex := mustPut(t, indexer, "old", scope, value)
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}

	query, err := indexer.Query(ctx, scope, value)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(query) != 2 {
		t.Fatalf("got %d query indexes, want current and previous", len(query))
	}
	if query[0].Generation != 2 || query[1].Generation != 1 {
		t.Fatalf("query generations = %d,%d, want 2,1", query[0].Generation, query[1].Generation)
	}
	if !query[1].Equal(oldIndex) {
		t.Fatal("previous query key does not match the registered old index")
	}
	current, err := indexer.QueryGeneration(ctx, scope, value, 2)
	if err != nil {
		t.Fatalf("QueryGeneration current: %v", err)
	}
	if !query[0].Equal(current) {
		t.Fatal("current query key was not returned first")
	}

	newIndex := mustPut(t, indexer, "new", scope, value)
	if newIndex.Generation != 2 {
		t.Fatalf("new write used generation %d, want 2", newIndex.Generation)
	}
	newRecord := mustRecord(t, indexer, "new")
	if len(newRecord.Indexes) != 1 || !newRecord.Indexes[0].Equal(newIndex) {
		t.Fatal("new write was registered with a non-current index")
	}

	oldRecord := mustRecord(t, indexer, "old")
	if len(oldRecord.Indexes) != 1 || !oldRecord.Indexes[0].Equal(oldIndex) {
		t.Fatal("old record should still await migration")
	}
}

func TestMigrationAndRetirementLifecycle(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}
	secret := []byte("migration-secret")

	mustPut(t, indexer, "record", scope, secret)
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}

	entry, err := indexer.PrepareMigration(ctx, "record", secret)
	if err != nil {
		t.Fatalf("PrepareMigration: %v", err)
	}
	if entry.From != 1 || entry.To != 2 {
		t.Fatalf("witness generations = %d->%d", entry.From, entry.To)
	}
	if err := indexer.VerifyMigration(ctx, entry); err != nil {
		t.Fatalf("VerifyMigration: %v", err)
	}
	if err := indexer.CommitMigration(ctx, entry); err != nil {
		t.Fatalf("CommitMigration: %v", err)
	}

	migrated := mustRecord(t, indexer, "record")
	if len(migrated.Indexes) != 2 {
		t.Fatalf("migrated record has %d indexes, want old and current", len(migrated.Indexes))
	}
	if _, ok := migrated.IndexFor(2); !ok {
		t.Fatal("migrated record lacks current index")
	}

	if err := indexer.RetirePrevious(ctx); err != nil {
		t.Fatalf("RetirePrevious: %v", err)
	}
	retired := mustRecord(t, indexer, "record")
	if len(retired.Indexes) != 1 || retired.Indexes[0].Generation != 2 {
		t.Fatal("retirement did not prune the previous-generation index")
	}
	if _, err := indexer.QueryGeneration(ctx, scope, secret, 1); !errors.Is(err, ErrRetiredGeneration) {
		t.Fatalf("old query key error = %v, want ErrRetiredGeneration", err)
	}
	query, err := indexer.Query(ctx, scope, secret)
	if err != nil {
		t.Fatalf("Query after retirement: %v", err)
	}
	if len(query) != 1 || query[0].Generation != 2 {
		t.Fatal("query returned a retired previous-generation key")
	}
	if err := indexer.CommitMigration(ctx, entry); !errors.Is(err, ErrRetiredGeneration) {
		t.Fatalf("retired migration entry error = %v, want ErrRetiredGeneration", err)
	}
	if err := indexer.RetirePrevious(ctx); !errors.Is(err, ErrNoPreviousGeneration) {
		t.Fatalf("second retirement error = %v", err)
	}
}

func TestRetirementRequiresEveryRecordCurrent(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}

	mustPut(t, indexer, "one", scope, []byte("one"))
	mustPut(t, indexer, "two", scope, []byte("two"))
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if _, err := indexer.MigrateRecord(ctx, "one", []byte("one")); err != nil {
		t.Fatalf("MigrateRecord: %v", err)
	}
	if err := indexer.RetirePrevious(ctx); !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("RetirePrevious error = %v, want ErrMigrationPending", err)
	}
	if indexer.PreviousGeneration() != 1 {
		t.Fatal("failed retirement changed the active generation set")
	}
	if _, err := indexer.QueryGeneration(ctx, scope, []byte("two"), 1); err != nil {
		t.Fatalf("previous generation should remain queryable: %v", err)
	}

	if _, err := indexer.MigrateRecord(ctx, "two", []byte("two")); err != nil {
		t.Fatalf("MigrateRecord(two): %v", err)
	}
	if err := indexer.RetirePrevious(ctx); err != nil {
		t.Fatalf("RetirePrevious after migration: %v", err)
	}
}

func TestForgedOrTamperedMigrationWitnessRejected(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}
	secret := []byte("witness-secret")

	mustPut(t, indexer, "record", scope, secret)
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	valid, err := indexer.PrepareMigration(ctx, "record", secret)
	if err != nil {
		t.Fatalf("PrepareMigration: %v", err)
	}

	forged := MigrationEntry{
		RecordID: valid.RecordID,
		Scope:    valid.Scope,
		From:     valid.From,
		To:       valid.To,
		OldIndex: valid.OldIndex,
		NewIndex: valid.NewIndex,
	}
	if err := indexer.CommitMigration(ctx, forged); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("self-asserted migration error = %v, want ErrInvalidWitness", err)
	}
	if err := indexer.CommitMigration(ctx, MigrationEntry{}); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("zero migration error = %v, want ErrInvalidWitness", err)
	}

	tamperedID := valid
	tamperedID.RecordID = "other-record"
	if err := indexer.CommitMigration(ctx, tamperedID); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("tampered record ID error = %v, want ErrInvalidWitness", err)
	}
	tamperedIndex := valid
	tamperedIndex.NewIndex.Digest[0] ^= 0xff
	if err := indexer.CommitMigration(ctx, tamperedIndex); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("tampered index error = %v, want ErrInvalidWitness", err)
	}
	tamperedScope := valid
	tamperedScope.Scope.Field = "phone"
	if err := indexer.CommitMigration(ctx, tamperedScope); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("tampered scope error = %v, want ErrInvalidWitness", err)
	}

	record := mustRecord(t, indexer, "record")
	if len(record.Indexes) != 1 || record.Indexes[0].Generation != 1 {
		t.Fatal("a rejected witness modified the registered record")
	}
	if err := indexer.CommitMigration(ctx, valid); err != nil {
		t.Fatalf("valid witness after forged attempts: %v", err)
	}
}

func TestMigrationAndNewWriteDuringRetirementRace(t *testing.T) {
	indexer, store, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}

	mustPut(t, indexer, "old", scope, []byte("old-value"))
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	entry, err := indexer.PrepareMigration(ctx, "old", []byte("old-value"))
	if err != nil {
		t.Fatalf("PrepareMigration: %v", err)
	}

	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	retireStarted := make(chan struct{})
	var commitOnce, retireOnce sync.Once
	indexer.hooks = &testHooks{
		beforeMigrationCommit: func() {
			commitOnce.Do(func() { close(commitEntered) })
			<-releaseCommit
		},
		retireAttempt: func() {
			retireOnce.Do(func() { close(retireStarted) })
		},
	}

	commitDone := make(chan error, 1)
	go func() {
		commitDone <- indexer.CommitMigration(ctx, entry)
	}()
	<-commitEntered

	// This write has to finish while the migration holds only the lifecycle
	// read lock. It must be registered with generation 2 before retirement.
	newIndex := mustPut(t, indexer, "new", scope, []byte("new-value"))
	if newIndex.Generation != 2 {
		t.Fatalf("concurrent write generation = %d, want 2", newIndex.Generation)
	}

	retireDone := make(chan error, 1)
	go func() {
		retireDone <- indexer.RetirePrevious(ctx)
	}()
	<-retireStarted

	close(releaseCommit)
	if err := <-commitDone; err != nil {
		t.Fatalf("concurrent migration: %v", err)
	}
	if err := <-retireDone; err != nil {
		t.Fatalf("concurrent retirement: %v", err)
	}
	if indexer.PreviousGeneration() != 0 {
		t.Fatal("retirement did not clear the previous generation")
	}

	records := mustList(t, store)
	if len(records) != 2 {
		t.Fatalf("store has %d records, want 2", len(records))
	}
	for _, record := range records {
		if len(record.Indexes) != 1 || record.Indexes[0].Generation != 2 {
			t.Fatalf("record %q was not fully registered at generation 2", record.ID)
		}
	}
}

func TestRetirementWaitsForInFlightNewWrite(t *testing.T) {
	indexer, _, _ := mustIndexer(t, 1, 2)
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}

	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	putEntered := make(chan struct{})
	releasePut := make(chan struct{})
	retireStarted := make(chan struct{})
	var putOnce, retireOnce sync.Once
	indexer.hooks = &testHooks{
		beforePutStore: func() {
			putOnce.Do(func() { close(putEntered) })
			<-releasePut
		},
		retireAttempt: func() {
			retireOnce.Do(func() { close(retireStarted) })
		},
	}

	putDone := make(chan error, 1)
	go func() {
		_, err := indexer.PutRecord(ctx, "in-flight", scope, []byte("value"))
		putDone <- err
	}()
	<-putEntered

	retireDone := make(chan error, 1)
	go func() {
		retireDone <- indexer.RetirePrevious(ctx)
	}()
	<-retireStarted

	close(releasePut)
	if err := <-putDone; err != nil {
		t.Fatalf("in-flight write: %v", err)
	}
	if err := <-retireDone; err != nil {
		t.Fatalf("retirement after in-flight write: %v", err)
	}

	record := mustRecord(t, indexer, "in-flight")
	if len(record.Indexes) != 1 || record.Indexes[0].Generation != 2 {
		t.Fatal("retirement raced past an in-flight write")
	}
}

type spyStore struct {
	*MemoryStore
	mu       sync.Mutex
	captured []StoredRecord
}

func newSpyStore() *spyStore {
	return &spyStore{MemoryStore: NewMemoryStore()}
}

func (s *spyStore) Store(ctx context.Context, record StoredRecord) error {
	s.mu.Lock()
	s.captured = append(s.captured, record.clone())
	s.mu.Unlock()
	return s.MemoryStore.Store(ctx, record)
}

func (s *spyStore) records() []StoredRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]StoredRecord, len(s.captured))
	copy(records, s.captured)
	return records
}

func TestStorageCallbacksNeverReceivePlaintext(t *testing.T) {
	const plaintext = "callback-must-not-see-this-value"
	store := newSpyStore()
	keys := newTestKeys(1, 2)
	indexer, err := NewWithStore(keys, 1, store)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	ctx := context.Background()
	scope := Scope{Tenant: "tenant", Collection: "users", Field: "email"}

	if _, err := indexer.PutRecord(ctx, "record", scope, []byte(plaintext)); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}
	if err := indexer.BeginRotation(ctx, 2); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	entry, err := indexer.MigrateRecord(ctx, "record", []byte(plaintext))
	if err != nil {
		t.Fatalf("MigrateRecord: %v", err)
	}
	if err := indexer.RetirePrevious(ctx); err != nil {
		t.Fatalf("RetirePrevious: %v", err)
	}

	captured := store.records()
	if len(captured) == 0 {
		t.Fatal("storage callback was not exercised")
	}
	for _, record := range captured {
		rendered := fmt.Sprintf("%#v", record)
		if strings.Contains(rendered, plaintext) {
			t.Fatalf("storage callback received plaintext: %s", rendered)
		}
	}
	renderedEntry := fmt.Sprintf("%#v", entry)
	if strings.Contains(renderedEntry, plaintext) {
		t.Fatalf("migration entry contains plaintext: %s", renderedEntry)
	}
}

func TestMemoryStoreReturnsDefensiveCopies(t *testing.T) {
	store := NewMemoryStore()
	record := StoredRecord{
		ID:      "record",
		Scope:   Scope{Tenant: "tenant", Collection: "collection", Field: "field"},
		Indexes: []Index{{Generation: 1, Digest: [32]byte{1, 2, 3}}},
	}
	if err := store.Store(context.Background(), record); err != nil {
		t.Fatalf("Store: %v", err)
	}
	record.Indexes[0].Digest[0] = 99

	loaded, ok, err := store.Load(context.Background(), "record")
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	loaded.Indexes[0].Digest[1] = 99

	again, ok, err := store.Load(context.Background(), "record")
	if err != nil || !ok {
		t.Fatalf("Load again: ok=%v err=%v", ok, err)
	}
	if again.Indexes[0].Digest[0] != 1 || again.Indexes[0].Digest[1] != 2 {
		t.Fatal("store exposed a mutable internal record")
	}
}

func mustList(t *testing.T, store *MemoryStore) []StoredRecord {
	t.Helper()
	records, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return records
}
