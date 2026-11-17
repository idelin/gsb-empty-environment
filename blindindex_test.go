package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	versionOne Version = "v1"
	versionTwo Version = "v2"
)

func TestIndexSeparatesTenantCollectionFieldAndValue(t *testing.T) {
	keys := testKeys()
	indexer, err := NewIndexer(keys)
	if err != nil {
		t.Fatal(err)
	}

	const (
		tenant     = "tenant-a"
		collection = "collection-a"
		field      = "email"
		value      = "person@example.test"
	)
	base := mustIndex(t, indexer, versionOne, tenant, collection, field, value)
	same := mustIndex(t, indexer, versionOne, tenant, collection, field, value)
	if !base.Equal(same) {
		t.Fatal("identical inputs produced different indexes")
	}

	variants := []Index{
		mustIndex(t, indexer, versionOne, "tenant-b", collection, field, value),
		mustIndex(t, indexer, versionOne, tenant, "collection-b", field, value),
		mustIndex(t, indexer, versionOne, tenant, collection, "phone", value),
		mustIndex(t, indexer, versionOne, tenant, collection, field, value+"x"),
	}
	for i, variant := range variants {
		if base.Equal(variant) {
			t.Fatalf("variant %d is linkable to the base index", i)
		}
	}

	emptyA := mustIndex(t, indexer, versionOne, tenant, collection, field, "")
	emptyB := mustIndex(t, indexer, versionOne, "tenant-b", collection, field, "")
	if emptyA.Equal(emptyB) {
		t.Fatal("empty values in different tenants produced a linkable index")
	}
}

func TestIndexRejectsEmptyDomainsAndEmptyKey(t *testing.T) {
	keys := testKeys()
	indexer, _ := NewIndexer(keys)

	tests := []struct {
		name                             string
		version                          Version
		tenant, collection, field, value string
	}{
		{"version", "", "tenant", "collection", "field", "value"},
		{"tenant", versionOne, "", "collection", "field", "value"},
		{"collection", versionOne, "tenant", "", "field", "value"},
		{"field", versionOne, "tenant", "collection", "", "value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := indexer.Index(tt.version, tt.tenant, tt.collection, tt.field, tt.value)
			if !errors.Is(err, ErrEmptyDomain) {
				t.Fatalf("got %v, want ErrEmptyDomain", err)
			}
		})
	}

	emptyKeyIndexer, err := NewIndexer(KeyFunc(func(Version) ([]byte, error) { return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = emptyKeyIndexer.Index(versionOne, "tenant", "collection", "field", "")
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("got %v, want ErrInvalidKey", err)
	}
}

func TestLengthPrefixEncodingHasNoBoundaryCollisions(t *testing.T) {
	keys := testKeys()
	indexer, _ := NewIndexer(keys)

	pairs := [][2][4]string{
		{
			{"ab", "c", "field", ""},
			{"a", "bc", "field", ""},
		},
		{
			{"tenant", "ab", "c", ""},
			{"tenant", "a", "bc", ""},
		},
		{
			{"tenant", "collection", "ab", "c"},
			{"tenant", "collection", "a", "bc"},
		},
	}
	for i, pair := range pairs {
		a := mustIndex(t, indexer, versionOne, pair[0][0], pair[0][1], pair[0][2], pair[0][3])
		b := mustIndex(t, indexer, versionOne, pair[1][0], pair[1][1], pair[1][2], pair[1][3])
		if a.Equal(b) {
			t.Fatalf("length-boundary pair %d collided", i)
		}
	}

	key, err := keys.Key(versionOne)
	if err != nil {
		t.Fatal(err)
	}
	got := mustIndex(t, indexer, versionOne, "tenant", "ab", "c", "")
	want := referenceIndex(key, versionOne, "tenant", "ab", "c", "")
	if !got.Equal(want) {
		t.Fatalf("index does not match independent HMAC encoding: got %s want %s", got, want)
	}
}

func TestRotationQueryMigrationAndRetirement(t *testing.T) {
	store := NewMemoryStore()
	manager := mustManager(t, store, versionOne)
	oldRecord := Record{
		Tenant:     "tenant",
		Collection: "people",
		Field:      "email",
		Value:      "old@example.test",
	}
	oldIndex, err := manager.Write("old-record", oldRecord)
	if err != nil {
		t.Fatal(err)
	}
	if oldIndex.Version != versionOne {
		t.Fatalf("write used %q, want %q", oldIndex.Version, versionOne)
	}

	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}
	if got := manager.Current(); got != versionTwo {
		t.Fatalf("current generation is %q, want %q", got, versionTwo)
	}
	previous, ok := manager.Previous()
	if !ok || previous != versionOne {
		t.Fatalf("previous generation is (%q, %v), want (%q, true)", previous, ok, versionOne)
	}

	query, err := manager.Query(oldRecord.Tenant, oldRecord.Collection, oldRecord.Field, oldRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	if query.Current.Version != versionTwo {
		t.Fatalf("query current version is %q, want %q", query.Current.Version, versionTwo)
	}
	if query.Previous == nil || query.Previous.Version != versionOne {
		t.Fatalf("query did not return previous generation: %#v", query)
	}
	if !query.Previous.Equal(oldIndex) {
		t.Fatal("query previous index differs from the stored old index")
	}
	if len(query.Indexes()) != 2 {
		t.Fatalf("query returned %d indexes, want 2", len(query.Indexes()))
	}
	if previousIndex, err := manager.QueryVersion(versionOne, oldRecord.Tenant, oldRecord.Collection, oldRecord.Field, oldRecord.Value); err != nil {
		t.Fatal(err)
	} else if !previousIndex.Equal(oldIndex) {
		t.Fatal("previous-generation QueryVersion differs from the stored old index")
	}

	newRecord := Record{
		Tenant:     "tenant",
		Collection: "people",
		Field:      "phone",
		Value:      "new@example.test",
	}
	newIndex, err := manager.Write("new-record", newRecord)
	if err != nil {
		t.Fatal(err)
	}
	if newIndex.Version != versionTwo {
		t.Fatalf("write during rotation used %q, want current %q", newIndex.Version, versionTwo)
	}

	pending := manager.PendingMigrations()
	if len(pending) != 1 || pending[0].RecordID != "old-record" {
		t.Fatalf("pending migrations = %#v, want only old-record", pending)
	}
	entry := pending[0]

	if err := manager.RetirePrevious(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("retirement before migration returned %v, want ErrMigrationIncomplete", err)
	}
	if _, ok := manager.Previous(); !ok {
		t.Fatal("failed retirement discarded the previous generation")
	}

	migrated, err := manager.Migrate(entry, oldRecord)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Version != versionTwo {
		t.Fatalf("migration wrote %q, want %q", migrated.Version, versionTwo)
	}
	if len(manager.PendingMigrations()) != 0 {
		t.Fatal("migration entry remained pending after successful migration")
	}
	if _, err := manager.Migrate(entry, oldRecord); !errors.Is(err, ErrNotPendingMigration) {
		t.Fatalf("replayed migration returned %v, want ErrNotPendingMigration", err)
	}

	if err := manager.RetirePrevious(); err != nil {
		t.Fatal(err)
	}
	if manager.Retired(versionOne) != true {
		t.Fatal("old generation not marked retired")
	}
	if _, ok := manager.Previous(); ok {
		t.Fatal("previous generation still active after retirement")
	}

	postRetirement, err := manager.Query(oldRecord.Tenant, oldRecord.Collection, oldRecord.Field, oldRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	if postRetirement.Previous != nil || len(postRetirement.Indexes()) != 1 {
		t.Fatalf("post-retirement query still returned old index: %#v", postRetirement)
	}
	if _, err := manager.QueryVersion(versionOne, oldRecord.Tenant, oldRecord.Collection, oldRecord.Field, oldRecord.Value); !errors.Is(err, ErrRetiredGeneration) {
		t.Fatalf("old-generation query returned %v, want ErrRetiredGeneration", err)
	}
	if _, err := manager.QueryVersion("v3", oldRecord.Tenant, oldRecord.Collection, oldRecord.Field, oldRecord.Value); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("unknown-generation query returned %v, want ErrUnknownGeneration", err)
	}
	if err := manager.StartRotation(versionOne); !errors.Is(err, ErrGenerationReuse) {
		t.Fatalf("retired generation reuse returned %v, want ErrGenerationReuse", err)
	}

	if versions := store.Versions("old-record"); !equalVersions(versions, []Version{versionTwo}) {
		t.Fatalf("old-record versions = %v, want [%s]", versions, versionTwo)
	}
	if versions := store.Versions("new-record"); !equalVersions(versions, []Version{versionTwo}) {
		t.Fatalf("new-record versions = %v, want [%s]", versions, versionTwo)
	}
}

func TestMigrationRejectsForgeryAndWrongSourceData(t *testing.T) {
	manager := mustManager(t, NewMemoryStore(), versionOne)
	first := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "first@example.test"}
	second := Record{Tenant: "tenant", Collection: "people", Field: "phone", Value: "+10000000000"}
	if _, err := manager.Write("first", first); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Write("second", second); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}

	pending := manager.PendingMigrations()
	if len(pending) != 2 {
		t.Fatalf("got %d pending migrations, want 2", len(pending))
	}
	entry := pending[0]
	other := pending[1]

	if _, err := manager.Migrate(MigrationEntry{
		RecordID: entry.RecordID,
		From:     entry.From,
		To:       entry.To,
	}, first); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("zero witness returned %v, want ErrInvalidWitness", err)
	}

	wrongGeneration := entry
	wrongGeneration.From = "v0"
	if _, err := manager.Migrate(wrongGeneration, first); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("wrong-version witness returned %v, want ErrInvalidWitness", err)
	}

	swappedWitness := entry
	swappedWitness.Witness = other.Witness
	if _, err := manager.Migrate(swappedWitness, first); !errors.Is(err, ErrInvalidWitness) {
		t.Fatalf("cross-record witness returned %v, want ErrInvalidWitness", err)
	}

	wrongValue := first
	wrongValue.Value = "attacker@example.test"
	if _, err := manager.Migrate(entry, wrongValue); !errors.Is(err, ErrMigrationProof) {
		t.Fatalf("wrong source data returned %v, want ErrMigrationProof", err)
	}
	if len(manager.PendingMigrations()) != 2 {
		t.Fatal("failed migration removed a pending entry")
	}
	if err := manager.RetirePrevious(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("retirement after failed migration returned %v, want ErrMigrationIncomplete", err)
	}
}

func TestMigrationAndNewWriteShareRegistration(t *testing.T) {
	manager := mustManager(t, NewMemoryStore(), versionOne)
	oldRecord := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "old@example.test"}
	if _, err := manager.Write("old", oldRecord); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}

	during := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "during@example.test"}
	duringIndex, err := manager.Write("during", during)
	if err != nil {
		t.Fatal(err)
	}
	if duringIndex.Version != versionTwo {
		t.Fatalf("new write used %q, want %q", duringIndex.Version, versionTwo)
	}
	for _, entry := range manager.PendingMigrations() {
		if entry.RecordID == "during" {
			t.Fatal("new current-generation write was incorrectly queued for migration")
		}
	}

	pending := manager.PendingMigrations()
	if len(pending) != 1 || pending[0].RecordID != "old" {
		t.Fatalf("pending migrations = %#v, want only old", pending)
	}
	if _, err := manager.Migrate(pending[0], oldRecord); err != nil {
		t.Fatal(err)
	}
	if err := manager.RetirePrevious(); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementVerifiesCommittedStoreIndex(t *testing.T) {
	store := NewMemoryStore()
	manager := mustManager(t, store, versionOne)
	record := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "old@example.test"}
	if _, err := manager.Write("old", record); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}
	entry := manager.PendingMigrations()[0]
	if _, err := manager.Migrate(entry, record); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteVersion(versionTwo); err != nil {
		t.Fatal(err)
	}
	if err := manager.RetirePrevious(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("retirement with missing store index returned %v, want ErrMigrationIncomplete", err)
	}
	if _, ok := manager.Previous(); !ok {
		t.Fatal("failed store verification retired the previous generation")
	}
	if _, err := manager.QueryVersion(versionOne, record.Tenant, record.Collection, record.Field, record.Value); err != nil {
		t.Fatalf("previous generation became unavailable after failed retirement: %v", err)
	}
}

func TestConcurrentWriteBeforeRotationIsMigrated(t *testing.T) {
	store := &gatedStore{
		base:          NewMemoryStore(),
		blockVersion:  versionOne,
		blockRecordID: "racing",
		putStarted:    make(chan struct{}),
		releasePut:    make(chan struct{}),
	}
	manager := mustManager(t, store, versionOne)
	record := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "race@example.test"}

	writeDone := make(chan error, 1)
	go func() {
		_, err := manager.Write("racing", record)
		writeDone <- err
	}()
	<-store.putStarted

	rotationDone := make(chan error, 1)
	go func() {
		rotationDone <- manager.StartRotation(versionTwo)
	}()
	waitForAttempt(t)
	close(store.releasePut)

	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-rotationDone; err != nil {
		t.Fatal(err)
	}
	pending := manager.PendingMigrations()
	if len(pending) != 1 || pending[0].RecordID != "racing" {
		t.Fatalf("write that linearized before rotation was not registered: %#v", pending)
	}
	if _, err := manager.Migrate(pending[0], record); err != nil {
		t.Fatal(err)
	}
	if err := manager.RetirePrevious(); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementWaitsForInFlightMigration(t *testing.T) {
	store := &gatedStore{
		base:          NewMemoryStore(),
		blockVersion:  versionTwo,
		blockRecordID: "old",
		putStarted:    make(chan struct{}),
		releasePut:    make(chan struct{}),
	}
	manager := mustManager(t, store, versionOne)
	record := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: "old@example.test"}
	if _, err := manager.Write("old", record); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}
	pending := manager.PendingMigrations()
	if len(pending) != 1 {
		t.Fatalf("got %d pending migrations, want 1", len(pending))
	}

	migrateDone := make(chan error, 1)
	go func() {
		_, err := manager.Migrate(pending[0], record)
		migrateDone <- err
	}()
	<-store.putStarted

	retireDone := make(chan error, 1)
	go func() {
		retireDone <- manager.RetirePrevious()
	}()
	waitForAttempt(t)
	close(store.releasePut)

	if err := <-migrateDone; err != nil {
		t.Fatal(err)
	}
	if err := <-retireDone; err != nil {
		t.Fatalf("retirement lost the migration race: %v", err)
	}
	if _, err := manager.QueryVersion(versionOne, record.Tenant, record.Collection, record.Field, record.Value); !errors.Is(err, ErrRetiredGeneration) {
		t.Fatalf("old query after retirement returned %v, want ErrRetiredGeneration", err)
	}
}

func TestCallbacksDoNotObservePlaintext(t *testing.T) {
	store := &observingStore{base: NewMemoryStore()}
	var observedVersions []Version
	var keyMu sync.Mutex
	keys := KeyFunc(func(version Version) ([]byte, error) {
		keyMu.Lock()
		observedVersions = append(observedVersions, version)
		keyMu.Unlock()
		return testKey(version)
	})
	manager, err := NewManager(keys, store, versionOne)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "sensitive-plaintext@example.test"
	record := Record{Tenant: "tenant", Collection: "people", Field: "email", Value: secret}
	if _, err := manager.Write("record", record); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Query(record.Tenant, record.Collection, record.Field, secret); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartRotation(versionTwo); err != nil {
		t.Fatal(err)
	}
	pending := manager.PendingMigrations()
	if _, err := manager.Migrate(pending[0], record); err != nil {
		t.Fatal(err)
	}
	if err := manager.RetirePrevious(); err != nil {
		t.Fatal(err)
	}

	repr := fmt.Sprintf("%#v %#v %#v", store.calls(), pending, observedVersions)
	if strings.Contains(repr, secret) {
		t.Fatalf("callback-observable state contains plaintext: %s", repr)
	}
	for _, call := range store.calls() {
		if call.RecordID == secret {
			t.Fatal("store callback observed plaintext as a record ID")
		}
	}
}

func TestMemoryStoreSnapshotIsDeterministic(t *testing.T) {
	store := NewMemoryStore()
	mustPut := func(id string, version Version) {
		t.Helper()
		index := mustIndex(t, mustTestIndexer(t), version, "tenant", "collection", "field", id)
		if err := store.Put(id, index); err != nil {
			t.Fatal(err)
		}
	}
	mustPut("b", versionOne)
	mustPut("a", versionTwo)
	mustPut("a", versionOne)

	got := store.List()
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	want := []struct {
		id      string
		version Version
	}{
		{"a", versionOne},
		{"a", versionTwo},
		{"b", versionOne},
	}
	for i := range want {
		if got[i].RecordID != want[i].id || got[i].Index.Version != want[i].version {
			t.Fatalf("entry %d = (%q, %q), want (%q, %q)", i, got[i].RecordID, got[i].Index.Version, want[i].id, want[i].version)
		}
	}
}

type gatedStore struct {
	base          *MemoryStore
	blockVersion  Version
	blockRecordID string
	putStarted    chan struct{}
	releasePut    chan struct{}
	startOnce     sync.Once
}

func (s *gatedStore) Put(recordID string, index Index) error {
	if index.Version == s.blockVersion &&
		(s.blockRecordID == "" || recordID == s.blockRecordID) {
		s.startOnce.Do(func() { close(s.putStarted) })
		<-s.releasePut
	}
	return s.base.Put(recordID, index)
}

func (s *gatedStore) Get(recordID string, version Version) (Index, bool, error) {
	return s.base.Get(recordID, version)
}

func (s *gatedStore) DeleteVersion(version Version) error {
	return s.base.DeleteVersion(version)
}

type storeCall struct {
	RecordID string
	Index    Index
}

type observingStore struct {
	base *MemoryStore
	mu   sync.Mutex
	log  []storeCall
}

func (s *observingStore) Put(recordID string, index Index) error {
	s.mu.Lock()
	s.log = append(s.log, storeCall{RecordID: recordID, Index: index})
	s.mu.Unlock()
	return s.base.Put(recordID, index)
}

func (s *observingStore) Get(recordID string, version Version) (Index, bool, error) {
	return s.base.Get(recordID, version)
}

func (s *observingStore) DeleteVersion(version Version) error {
	return s.base.DeleteVersion(version)
}

func (s *observingStore) calls() []storeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storeCall(nil), s.log...)
}

func testKeys() KeyFunc {
	return KeyFunc(testKey)
}

func testKey(version Version) ([]byte, error) {
	switch version {
	case versionOne:
		return bytes.Repeat([]byte{0x11}, sha256.Size), nil
	case versionTwo:
		return bytes.Repeat([]byte{0x22}, sha256.Size), nil
	default:
		return nil, fmt.Errorf("test key %q not found", version)
	}
}

func mustManager(t *testing.T, store Store, initial Version) *Manager {
	t.Helper()
	manager, err := NewManager(testKeys(), store, initial)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func mustTestIndexer(t *testing.T) *Indexer {
	t.Helper()
	indexer, err := NewIndexer(testKeys())
	if err != nil {
		t.Fatal(err)
	}
	return indexer
}

func mustIndex(t *testing.T, indexer *Indexer, version Version, tenant, collection, field, value string) Index {
	t.Helper()
	index, err := indexer.Index(version, tenant, collection, field, value)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func referenceIndex(key []byte, version Version, tenant, collection, field, value string) Index {
	var encoded bytes.Buffer
	for _, component := range []string{string(version), tenant, collection, field, value} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(component)))
		encoded.Write(size[:])
		encoded.WriteString(component)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(encoded.Bytes())
	var index Index
	index.Version = version
	copy(index.Digest[:], mac.Sum(nil))
	return index
}

func equalVersions(got, want []Version) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func waitForAttempt(t *testing.T) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
}
