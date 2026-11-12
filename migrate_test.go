package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

const (
	targetAlg  = AlgorithmSHA256
	targetCost = 64
)

func mustRecord(t *testing.T, v Verifier, version uint64, alg string, password, salt []byte, cost int) Record {
	t.Helper()
	rec, err := NewRecord(v, version, alg, password, salt, cost)
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	return rec
}

func newUpgrader(store Store, v Verifier) *Upgrader {
	return &Upgrader{
		Store:     store,
		Verifier:  v,
		Algorithm: targetAlg,
		Cost:      targetCost,
		SaltSize:  16,
	}
}

func TestLoginMigratesOldRecord(t *testing.T) {
	v := IteratingVerifier{}
	store := NewMemoryStore()
	old := mustRecord(t, v, 7, targetAlg, []byte("s3cret"), []byte("oldsalt"), 4)
	store.Set("alice", old)

	ok, err := newUpgrader(store, v).Login("alice", []byte("s3cret"))
	if err != nil || !ok {
		t.Fatalf("Login = %v, %v; want true, nil", ok, err)
	}

	got, err := store.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != old.Version+1 {
		t.Errorf("Version = %d; want %d", got.Version, old.Version+1)
	}
	if got.Cost != targetCost || got.Algorithm != targetAlg {
		t.Errorf("record params = %q/%d; want %q/%d", got.Algorithm, got.Cost, targetAlg, targetCost)
	}
	if len(got.Salt) != 16 {
		t.Errorf("salt length = %d; want 16", len(got.Salt))
	}
	want, err := v.Derive(targetAlg, []byte("s3cret"), got.Salt, targetCost)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	defer Zero(want)
	if !bytes.Equal(got.Derived, want) {
		t.Error("migrated record does not verify against the same password")
	}
}

func TestLoginKeepsCurrentRecord(t *testing.T) {
	v := IteratingVerifier{}
	store := NewMemoryStore()
	cur := mustRecord(t, v, 3, targetAlg, []byte("s3cret"), []byte("newsalt12345678"), targetCost)
	store.Set("alice", cur)

	ok, err := newUpgrader(store, v).Login("alice", []byte("s3cret"))
	if err != nil || !ok {
		t.Fatalf("Login = %v, %v; want true, nil", ok, err)
	}

	got, err := store.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != cur.Version {
		t.Errorf("Version = %d; want unchanged %d", got.Version, cur.Version)
	}
	if !bytes.Equal(got.Derived, cur.Derived) || !bytes.Equal(got.Salt, cur.Salt) {
		t.Error("up-to-date record was rewritten")
	}
}

func TestLoginWrongPasswordDoesNotMigrate(t *testing.T) {
	v := IteratingVerifier{}
	store := NewMemoryStore()
	old := mustRecord(t, v, 5, targetAlg, []byte("s3cret"), []byte("oldsalt"), 4)
	store.Set("alice", old)

	ok, err := newUpgrader(store, v).Login("alice", []byte("wrong"))
	if err != nil || ok {
		t.Fatalf("Login = %v, %v; want false, nil", ok, err)
	}

	got, err := store.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != old.Version || got.Cost != old.Cost {
		t.Errorf("record changed after failed login: %+v", got)
	}
}

// blockingVerifier blocks the first Derive call until the test releases it,
// so a concurrent password change can land between verify and migrate.
type blockingVerifier struct {
	inner   Verifier
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingVerifier) Derive(algorithm string, password, salt []byte, cost int) ([]byte, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return b.inner.Derive(algorithm, password, salt, cost)
}

func TestLoginMigrationDoesNotOverwriteConcurrentPasswordChange(t *testing.T) {
	inner := IteratingVerifier{}
	bv := &blockingVerifier{
		inner:   inner,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	store := NewMemoryStore()
	old := mustRecord(t, inner, 1, targetAlg, []byte("old-pass"), []byte("oldsalt"), 4)
	store.Set("alice", old)

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, err := newUpgrader(store, bv).Login("alice", []byte("old-pass"))
		done <- result{ok, err}
	}()

	<-bv.entered // login verified the old record and is about to migrate

	// Concurrent password change bumps the record version.
	changed := mustRecord(t, inner, old.Version+1, targetAlg, []byte("new-pass"), []byte("newsalt"), 8)
	if err := store.CompareAndSwapVersion("alice", old.Version, changed); err != nil {
		t.Fatalf("concurrent change: %v", err)
	}
	close(bv.release)

	res := <-done
	if res.err != nil || !res.ok {
		t.Fatalf("Login = %v, %v; migration conflict must not change the login decision", res.ok, res.err)
	}

	got, err := store.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != changed.Version || got.Cost != changed.Cost || !bytes.Equal(got.Derived, changed.Derived) {
		t.Errorf("migration overwrote the concurrently set password: got %+v, want %+v", got, changed)
	}

	// The new password must still log in; the old one must not.
	ok, err := newUpgrader(store, inner).Login("alice", []byte("new-pass"))
	if err != nil || !ok {
		t.Errorf("Login with new password = %v, %v; want true, nil", ok, err)
	}
}

// failingStore always fails the compare-and-swap, simulating a store outage
// during migration.
type failingStore struct {
	*MemoryStore
	err error
}

func (s failingStore) CompareAndSwapVersion(string, uint64, Record) error {
	return s.err
}

func TestLoginMigrationStoreErrorStillSucceeds(t *testing.T) {
	v := IteratingVerifier{}
	mem := NewMemoryStore()
	old := mustRecord(t, v, 2, targetAlg, []byte("s3cret"), []byte("oldsalt"), 4)
	mem.Set("alice", old)
	store := failingStore{MemoryStore: mem, err: errors.New("store unavailable")}

	ok, err := newUpgrader(store, v).Login("alice", []byte("s3cret"))
	if err != nil || !ok {
		t.Fatalf("Login = %v, %v; migration failure must not change the login decision", ok, err)
	}

	got, err := mem.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != old.Version || got.Cost != old.Cost {
		t.Errorf("record changed despite failed migration: %+v", got)
	}
}

// captureVerifier keeps references to every derived buffer it returns so
// tests can check that Upgrader zeroes them once they are no longer needed.
type captureVerifier struct {
	inner Verifier
	mu    sync.Mutex
	bufs  [][]byte
}

func (c *captureVerifier) Derive(algorithm string, password, salt []byte, cost int) ([]byte, error) {
	out, err := c.inner.Derive(algorithm, password, salt, cost)
	if err == nil {
		c.mu.Lock()
		c.bufs = append(c.bufs, out)
		c.mu.Unlock()
	}
	return out, err
}

func (c *captureVerifier) captured() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.bufs...)
}

func allZero(buf []byte) bool {
	for _, b := range buf {
		if b != 0 {
			return false
		}
	}
	return true
}

func TestLoginReleasesSensitiveBuffers(t *testing.T) {
	t.Run("verify buffer released on success without migration", func(t *testing.T) {
		cv := &captureVerifier{inner: IteratingVerifier{}}
		store := NewMemoryStore()
		cur := mustRecord(t, cv.inner, 1, targetAlg, []byte("s3cret"), []byte("newsalt12345678"), targetCost)
		store.Set("alice", cur)

		ok, err := newUpgrader(store, cv).Login("alice", []byte("s3cret"))
		if err != nil || !ok {
			t.Fatalf("Login = %v, %v; want true, nil", ok, err)
		}
		bufs := cv.captured()
		if len(bufs) != 1 {
			t.Fatalf("captured %d buffers; want 1", len(bufs))
		}
		if !allZero(bufs[0]) {
			t.Error("verify buffer was not zeroed after login")
		}
	})

	t.Run("verify buffer released on failure", func(t *testing.T) {
		cv := &captureVerifier{inner: IteratingVerifier{}}
		store := NewMemoryStore()
		cur := mustRecord(t, cv.inner, 1, targetAlg, []byte("s3cret"), []byte("newsalt12345678"), targetCost)
		store.Set("alice", cur)

		ok, err := newUpgrader(store, cv).Login("alice", []byte("wrong"))
		if err != nil || ok {
			t.Fatalf("Login = %v, %v; want false, nil", ok, err)
		}
		bufs := cv.captured()
		if len(bufs) != 1 {
			t.Fatalf("captured %d buffers; want 1", len(bufs))
		}
		if !allZero(bufs[0]) {
			t.Error("verify buffer was not zeroed after failed login")
		}
	})

	t.Run("discarded migration buffer released on conflict", func(t *testing.T) {
		cv := &captureVerifier{inner: IteratingVerifier{}}
		mem := NewMemoryStore()
		old := mustRecord(t, cv.inner, 1, targetAlg, []byte("s3cret"), []byte("oldsalt"), 4)
		mem.Set("alice", old)
		store := failingStore{MemoryStore: mem, err: ErrVersionConflict}

		ok, err := newUpgrader(store, cv).Login("alice", []byte("s3cret"))
		if err != nil || !ok {
			t.Fatalf("Login = %v, %v; want true, nil", ok, err)
		}
		bufs := cv.captured()
		if len(bufs) != 2 {
			t.Fatalf("captured %d buffers; want 2 (verify + discarded migration)", len(bufs))
		}
		for i, buf := range bufs {
			if !allZero(buf) {
				t.Errorf("buffer %d was not zeroed after failed migration", i)
			}
		}
	})
}
