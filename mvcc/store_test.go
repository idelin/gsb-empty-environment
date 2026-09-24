package mvcc

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T, opts ...func(*Config)) *Store {
	t.Helper()
	now := int64(0)
	cfg := Config{Clock: func() int64 { return now }}
	for _, opt := range opts {
		opt(&cfg)
	}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustWrite(t *testing.T, store *Store, records ...Record) Commit {
	t.Helper()
	commit, err := store.Write(records)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return commit
}

func assertValue(t *testing.T, result ReadResult, key, want string) {
	t.Helper()
	got, ok := result.Values[key]
	if !ok {
		t.Fatalf("key %q missing; deleted=%v missing=%v", key, result.Deleted, result.Missing)
	}
	if string(got) != want {
		t.Fatalf("key %q = %q, want %q", key, got, want)
	}
}

func assertDeleted(t *testing.T, result ReadResult, key string) {
	t.Helper()
	for _, deleted := range result.Deleted {
		if deleted == key {
			return
		}
	}
	t.Fatalf("key %q not marked deleted: %+v", key, result)
}

func TestBatchAtomicAndSnapshotIsolation(t *testing.T) {
	store := newTestStore(t)
	mustWrite(t, store,
		Record{Key: "A", Value: []byte("A1")},
		Record{Key: "B", Value: []byte("B1")},
	)
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, store, Record{Key: "A", Value: []byte("A2")})
	mustWrite(t, store, Record{Key: "B", Value: []byte("B2")})

	old, err := snapshot.Read([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if old.Version != 1 {
		t.Fatalf("snapshot version = %d, want 1", old.Version)
	}
	assertValue(t, old, "A", "A1")
	assertValue(t, old, "B", "B1")

	current, err := store.Read([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 3 {
		t.Fatalf("current version = %d, want 3", current.Version)
	}
	assertValue(t, current, "A", "A2")
	assertValue(t, current, "B", "B2")
}

func TestDeleteRemainsVisibleInOlderSnapshot(t *testing.T) {
	store := newTestStore(t)
	mustWrite(t, store, Record{Key: "K", Value: []byte("v1")})
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store, Record{Key: "K", Delete: true})

	old, err := snapshot.Read([]string{"K"})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, old, "K", "v1")

	current, err := store.Read([]string{"K"})
	if err != nil {
		t.Fatal(err)
	}
	assertDeleted(t, current, "K")
}

func TestWriteRejectsInvalidInputBeforePublish(t *testing.T) {
	store := newTestStore(t)
	tests := []struct {
		name    string
		records []Record
		want    error
	}{
		{name: "empty", records: nil, want: ErrEmptyBatch},
		{name: "empty key", records: []Record{{Key: "", Value: []byte("v")}}, want: ErrEmptyKey},
		{name: "duplicate", records: []Record{{Key: "K", Value: []byte("a")}, {Key: "K", Value: []byte("b")}}, want: ErrDuplicateKey},
		{name: "revision", records: []Record{{Key: "K", Value: []byte("v"), Revision: 1}}, want: ErrInvalidRevision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Write(tt.records)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Write() error = %v, want %v", err, tt.want)
			}
		})
	}
	if store.version != 0 {
		t.Fatalf("version = %d after invalid writes, want 0", store.version)
	}
}

func TestFailureInjectionLeavesStoreUnchanged(t *testing.T) {
	store := newTestStore(t, func(cfg *Config) {
		cfg.Fail = func(stage Stage) error {
			if stage == StageWrite {
				return errors.New("injected write failure")
			}
			return nil
		}
	})
	_, err := store.Write([]Record{{Key: "K", Value: []byte("v")}})
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("Write() error = %v, want injected failure", err)
	}
	result, err := store.Read([]string{"K"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "K" {
		t.Fatalf("result = %+v, want K missing", result)
	}
	if store.version != 0 {
		t.Fatalf("version = %d after failed write, want 0", store.version)
	}
}

func TestReadReturnsDefensiveCopies(t *testing.T) {
	store := newTestStore(t)
	mustWrite(t, store, Record{Key: "K", Value: []byte("abc")})
	first, err := store.Read([]string{"K"})
	if err != nil {
		t.Fatal(err)
	}
	first.Values["K"][0] = 'X'
	second, err := store.Read([]string{"K"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.Values["K"], []byte("abc")) {
		t.Fatalf("stored value mutated through read result: %q", second.Values["K"])
	}
}

func TestSourceBatchPublishesAtomically(t *testing.T) {
	source := func(keys []string) (SourceBatch, error) {
		return SourceBatch{
			Revision: 10,
			Records: []Record{
				{Key: "A", Value: []byte("A-source"), Revision: 10},
				{Key: "B", Value: []byte("B-source"), Revision: 10},
			},
		}, nil
	}
	store := newTestStore(t, func(cfg *Config) { cfg.Source = source })
	commit, err := store.Load([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Version != 1 || commit.SourceRevision != 10 {
		t.Fatalf("commit = %+v, want version 1 source revision 10", commit)
	}
	result, err := store.Read([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, result, "A", "A-source")
	assertValue(t, result, "B", "B-source")
}

func TestSourceRejectsMixedOrStaleRevision(t *testing.T) {
	tests := []struct {
		name    string
		batch   SourceBatch
		initial int64
	}{
		{
			name: "mixed revision",
			batch: SourceBatch{Revision: 10, Records: []Record{
				{Key: "A", Value: []byte("a"), Revision: 10},
				{Key: "B", Value: []byte("b"), Revision: 9},
			}},
		},
		{
			name:  "missing record revision",
			batch: SourceBatch{Revision: 10, Records: []Record{{Key: "A", Value: []byte("a")}}},
		},
		{name: "zero revision", batch: SourceBatch{Revision: 0, Records: []Record{{Key: "A", Value: []byte("a")}}}},
		{name: "stale revision", initial: 10, batch: SourceBatch{Revision: 10, Records: []Record{{Key: "A", Value: []byte("a")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			store := newTestStore(t, func(cfg *Config) {
				cfg.Source = func([]string) (SourceBatch, error) {
					called = true
					return tt.batch, nil
				}
			})
			store.sourceRevision = tt.initial
			_, err := store.Load([]string{"A", "B"})
			if !errors.Is(err, ErrInvalidRevision) {
				t.Fatalf("Load() error = %v, want ErrInvalidRevision", err)
			}
			if !called && tt.name != "zero revision" {
				t.Fatal("source function was not called")
			}
			if store.version != 0 {
				t.Fatalf("version = %d after rejected load, want 0", store.version)
			}
		})
	}
}

func TestSourceCannotReturnUnexpectedOrDuplicateKeys(t *testing.T) {
	tests := []struct {
		name  string
		batch SourceBatch
		want  error
	}{
		{name: "unexpected", batch: SourceBatch{Revision: 1, Records: []Record{{Key: "C", Value: []byte("c")}}}, want: ErrMissingKey},
		{name: "duplicate", batch: SourceBatch{Revision: 1, Records: []Record{
			{Key: "A", Value: []byte("a1"), Revision: 1},
			{Key: "A", Value: []byte("a2"), Revision: 1},
		}}, want: ErrDuplicateKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, func(cfg *Config) {
				cfg.Source = func([]string) (SourceBatch, error) { return tt.batch, nil }
			})
			_, err := store.Load([]string{"A", "B"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Load() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSnapshotTTLPolicy(t *testing.T) {
	now := int64(0)
	store, err := New(Config{
		Clock:       func() int64 { return now },
		SnapshotTTL: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mustWrite(t, store, Record{Key: "K", Value: []byte("v")})
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	now = 9
	if snapshot.Expired() {
		t.Fatal("snapshot expired before ttl")
	}
	if _, err := snapshot.Read([]string{"K"}); err != nil {
		t.Fatalf("read before ttl failed: %v", err)
	}

	now = 10
	if !snapshot.Expired() {
		t.Fatal("snapshot not expired at ttl boundary")
	}
	if _, err := snapshot.Read([]string{"K"}); !errors.Is(err, ErrSnapshotExpired) {
		t.Fatalf("Read() error = %v, want ErrSnapshotExpired", err)
	}

	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Read([]string{"K"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Read() after close error = %v, want ErrClosed", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
}

func TestExpiredSnapshotDoesNotPinOldVersions(t *testing.T) {
	now := int64(0)
	store, err := New(Config{
		Clock:       func() int64 { return now },
		SnapshotTTL: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mustWrite(t, store, Record{Key: "K", Value: []byte("v1")})
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	now = 10
	mustWrite(t, store, Record{Key: "K", Value: []byte("v2")})
	stats, err := store.GC()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveSnapshots != 0 || stats.Watermark != store.version {
		t.Fatalf("stats = %+v, expired snapshot should not pin versions", stats)
	}
	if len(store.keys["K"]) != 1 || string(store.keys["K"][0].value) != "v2" {
		t.Fatalf("chain = %+v, want only v2", store.keys["K"])
	}
	if _, err := snapshot.Read([]string{"K"}); !errors.Is(err, ErrSnapshotExpired) {
		t.Fatalf("Read() error = %v, want ErrSnapshotExpired", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGCSafelyRetainsVersionsForActiveSnapshot(t *testing.T) {
	store := newTestStore(t)
	mustWrite(t, store,
		Record{Key: "A", Value: []byte("A1")},
		Record{Key: "B", Value: []byte("B1")},
	)
	old, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store, Record{Key: "A", Value: []byte("A2")})
	mustWrite(t, store,
		Record{Key: "A", Value: []byte("A3")},
		Record{Key: "B", Value: []byte("B2")},
	)
	mustWrite(t, store, Record{Key: "B", Delete: true})

	stats, err := store.GC()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Watermark != 1 || stats.ActiveSnapshots != 1 {
		t.Fatalf("stats = %+v, want watermark 1 and one snapshot", stats)
	}
	if stats.EntriesRemoved != 0 {
		t.Fatalf("removed %d entries while snapshot needed them", stats.EntriesRemoved)
	}

	result, err := old.Read([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, result, "A", "A1")
	assertValue(t, result, "B", "B1")

	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	stats, err = store.GC()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Watermark != store.version {
		t.Fatalf("watermark = %d, want %d", stats.Watermark, store.version)
	}

	if chain := store.keys["A"]; len(chain) != 1 || chain[0].version != 3 {
		t.Fatalf("A chain = %+v, want only version 3", chain)
	}
	if chain := store.keys["B"]; len(chain) != 1 || !chain[0].delete || chain[0].version != 4 {
		t.Fatalf("B chain = %+v, want only latest tombstone", chain)
	}
}

func TestGCUsesOldestActiveSnapshotWatermark(t *testing.T) {
	store := newTestStore(t)
	mustWrite(t, store, Record{Key: "K", Value: []byte("v1")})
	oldest, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store, Record{Key: "K", Value: []byte("v2")})
	newer, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store, Record{Key: "K", Value: []byte("v3")})

	if _, err := store.GC(); err != nil {
		t.Fatal(err)
	}
	if len(store.keys["K"]) != 3 {
		t.Fatalf("chain length = %d, want 3 while oldest snapshot is active", len(store.keys["K"]))
	}
	if err := newer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GC(); err != nil {
		t.Fatal(err)
	}
	if len(store.keys["K"]) != 3 {
		t.Fatalf("chain length = %d, newest snapshot cannot advance oldest watermark", len(store.keys["K"]))
	}
	if err := oldest.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GC(); err != nil {
		t.Fatal(err)
	}
	if len(store.keys["K"]) != 1 || store.keys["K"][0].version != 3 {
		t.Fatalf("chain = %+v, want only current version", store.keys["K"])
	}
}

func TestCloseReleasesStoreAndSnapshotResources(t *testing.T) {
	now := int64(0)
	store, err := New(Config{Clock: func() int64 { return now }})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store, Record{Key: "K", Value: []byte("v")})
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if len(store.keys) != 0 || len(store.snapshots) != 0 {
		t.Fatalf("resources retained after close: keys=%d snapshots=%d", len(store.keys), len(store.snapshots))
	}
	if !snapshot.closed {
		t.Fatal("snapshot was not marked closed when store closed")
	}
	if _, err := store.Read([]string{"K"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("store Read() error = %v, want ErrClosed", err)
	}
	if _, err := snapshot.Read([]string{"K"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot Read() error = %v, want ErrClosed", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second store Close() error = %v", err)
	}
}

func TestConcurrentWritesPublishAllOrNothing(t *testing.T) {
	store := newTestStore(t)
	const writers = 50
	var wg sync.WaitGroup
	commits := make(chan Commit, writers)
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			commit, err := store.Write([]Record{
				{Key: "A", Value: []byte(fmt.Sprintf("A%d", i))},
				{Key: "B", Value: []byte(fmt.Sprintf("B%d", i))},
			})
			if err != nil {
				t.Errorf("Write() error: %v", err)
				return
			}
			commits <- commit
		}()
	}
	wg.Wait()
	close(commits)
	versions := make(map[int64]bool, writers)
	for commit := range commits {
		if commit.Version < 1 || commit.Version > writers {
			t.Fatalf("invalid committed version: %+v", commit)
		}
		versions[commit.Version] = true
	}
	if store.version != writers {
		t.Fatalf("version = %d, want %d", store.version, writers)
	}
	if len(versions) != writers {
		t.Fatalf("unique committed versions = %d, want %d", len(versions), writers)
	}
	result, err := store.Read([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	aSuffix := strings.TrimPrefix(string(result.Values["A"]), "A")
	bSuffix := strings.TrimPrefix(string(result.Values["B"]), "B")
	if aSuffix != bSuffix {
		t.Fatalf("latest batch mixed values: A=%q B=%q", result.Values["A"], result.Values["B"])
	}
}
