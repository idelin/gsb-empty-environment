package memsnap

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
)

func mustGet(t *testing.T, snap *Snapshot, key, want string) {
	t.Helper()
	got, err := snap.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) on v%d: unexpected error %v", key, snap.Version(), err)
	}
	if got != want {
		t.Fatalf("Get(%q) on v%d = %q, want %q", key, snap.Version(), got, want)
	}
}

func mustNotFound(t *testing.T, snap *Snapshot, key string) {
	t.Helper()
	if _, err := snap.Get(key); err != ErrNotFound {
		t.Fatalf("Get(%q) on v%d: err = %v, want ErrNotFound", key, snap.Version(), err)
	}
}

// TestSnapshotIsolation: a reader sees only its own version, never a mix
// of pre- and post-commit data.
func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	if err := s.WriteBatch([]Op{Put("a", "1"), Put("b", "1")}); err != nil {
		t.Fatal(err)
	}
	old := s.BeginRead()
	if err := s.WriteBatch([]Op{Put("a", "2"), Put("b", "2")}); err != nil {
		t.Fatal(err)
	}
	mustGet(t, old, "a", "1")
	mustGet(t, old, "b", "1")

	fresh := s.BeginRead()
	mustGet(t, fresh, "a", "2")
	mustGet(t, fresh, "b", "2")

	// Half-committed state must be impossible: a batch touching two keys is
	// observed by any snapshot either fully or not at all.
	if err := s.WriteBatch([]Op{Put("a", "3"), Put("b", "3")}); err != nil {
		t.Fatal(err)
	}
	mustGet(t, fresh, "a", "2")
	mustGet(t, fresh, "b", "2")
	mustGet(t, s.BeginRead(), "a", "3")
}

// TestDeleteVisibilityAcrossVersions: a deleted key is invisible in the new
// version; an old reader still reads the old value.
func TestDeleteVisibilityAcrossVersions(t *testing.T) {
	s := NewStore()
	if err := s.WriteBatch([]Op{Put("k", "v1")}); err != nil {
		t.Fatal(err)
	}
	old := s.BeginRead()
	if err := s.WriteBatch([]Op{Del("k")}); err != nil {
		t.Fatal(err)
	}
	mustGet(t, old, "k", "v1")          // old reader still sees old value
	mustNotFound(t, s.BeginRead(), "k") // new version: key invisible
}

// TestClosedHandleAndIdempotentEndRead: reads after close fail; double
// close is a no-op.
func TestClosedHandleAndIdempotentEndRead(t *testing.T) {
	s := NewStore()
	if err := s.WriteBatch([]Op{Put("k", "v")}); err != nil {
		t.Fatal(err)
	}
	snap := s.BeginRead()
	if err := snap.EndRead(); err != nil {
		t.Fatalf("first EndRead: %v", err)
	}
	if err := snap.EndRead(); err != nil {
		t.Fatalf("second EndRead must be idempotent: %v", err)
	}
	if _, err := snap.Get("k"); err != ErrSnapshotClosed {
		t.Fatalf("Get after EndRead: err = %v, want ErrSnapshotClosed", err)
	}
}

// TestFailedBatchPublishesNothing: an invalid batch must not publish a
// partial result, not even bump the version.
func TestFailedBatchPublishesNothing(t *testing.T) {
	s := NewStore()
	if err := s.WriteBatch([]Op{Put("stable", "x")}); err != nil {
		t.Fatal(err)
	}
	before := s.Stats()

	err := s.WriteBatch([]Op{Put("partial", "bad"), {Key: "", Value: "invalid"}})
	if err != ErrInvalidOp {
		t.Fatalf("WriteBatch err = %v, want ErrInvalidOp", err)
	}
	after := s.Stats()
	if after.Version != before.Version {
		t.Fatalf("failed batch bumped version: %d -> %d", before.Version, after.Version)
	}
	if after.Retained != before.Retained {
		t.Fatalf("failed batch changed retained nodes: %d -> %d", before.Retained, after.Retained)
	}
	snap := s.BeginRead()
	mustNotFound(t, snap, "partial") // no half-batch visible
	mustGet(t, snap, "stable", "x")
}

// TestControlledConcurrencyBarrier orchestrates, with channel barriers, the
// exact interleaving: an old reader holds v1 while the writer overwrites
// and then deletes the same key; a second reader observes the new version;
// reclamation is only allowed to drop the old nodes once the old reader
// closes.
func TestControlledConcurrencyBarrier(t *testing.T) {
	s := NewStore()
	if err := s.WriteBatch([]Op{Put("k", "v1")}); err != nil { // v1
		t.Fatal(err)
	}

	oldReader := s.BeginRead() // pins v1

	writerCommitted := make(chan struct{})
	oldReaderChecked := make(chan struct{})

	// Writer: overwrite then delete the same key, in two serial commits.
	go func() {
		defer close(writerCommitted)
		if err := s.WriteBatch([]Op{Put("k", "v2")}); err != nil { // v2
			t.Errorf("overwrite: %v", err)
		}
		if err := s.WriteBatch([]Op{Del("k")}); err != nil { // v3
			t.Errorf("delete: %v", err)
		}
	}()

	// Old reader performs its read concurrently, after the writer finished
	// both commits (barrier), and must still observe v1.
	go func() {
		defer close(oldReaderChecked)
		<-writerCommitted
		mustGet(t, oldReader, "k", "v1")
	}()
	<-oldReaderChecked

	// A second reader now sees the newest version: key deleted.
	newReader := s.BeginRead()
	mustNotFound(t, newReader, "k")

	// While the old reader is alive, all three version nodes must be
	// retained: v1 value (for oldReader), v2 value (shadowed but still
	// newest-before-delete... it is reclaimable only if no snapshot needs
	// it; oldReader needs v1, future readers need v3 tombstone), v3 tombstone.
	if got := s.Stats().Retained; got != 3 {
		t.Fatalf("retained with old reader alive = %d, want 3", got)
	}

	// Reclaim is triggered by the old reader closing; only then may the
	// v1/v2 nodes be dropped.
	if err := oldReader.EndRead(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Retained; got != 1 {
		t.Fatalf("retained after old reader closed = %d, want 1 (only v3 tombstone)", got)
	}

	// New reader still resolves correctly after reclamation.
	mustNotFound(t, newReader, "k")
	if err := newReader.EndRead(); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Retained; got != 1 {
		t.Fatalf("retained after all readers closed = %d, want 1 (latest tombstone kept)", got)
	}
}

// TestRandomHistoryAgainstSerialModel replays a random short history of
// batches / reads / closes against a serial version model and requires
// every Get to match. It reports the peak number of retained version nodes
// and the count after final reclamation.
func TestRandomHistoryAgainstSerialModel(t *testing.T) {
	const (
		steps   = 4000
		numKeys = 6
	)
	seed := int64(20261005)
	if v, err := strconv.ParseInt(os.Getenv("MEMSNAP_SEED"), 10, 64); err == nil {
		seed = v
	}
	rng := rand.New(rand.NewSource(seed))
	s := NewStore()

	// Serial model: full key->value map per committed version.
	model := map[uint64]map[string]string{0: {}}
	var version uint64
	touched := map[string]bool{} // keys with at least one version node ever

	type liveSnap struct {
		snap *Snapshot
		data map[string]string
	}
	var live []liveSnap

	keys := func() []string {
		ks := make([]string, numKeys)
		for i := range ks {
			ks[i] = fmt.Sprintf("key-%d", i)
		}
		return ks
	}()

	for step := 0; step < steps; step++ {
		switch rng.Intn(6) {
		case 0, 1: // commit a batch of 1-3 ops, occasionally invalid
			n := 1 + rng.Intn(3)
			ops := make([]Op, n)
			for i := range ops {
				k := keys[rng.Intn(numKeys)]
				if rng.Intn(4) == 0 {
					ops[i] = Del(k)
				} else {
					ops[i] = Put(k, fmt.Sprintf("s%d-op%d", step, i))
				}
			}
			invalid := rng.Intn(10) == 0
			if invalid {
				ops = append(ops, Op{Key: ""})
			}
			err := s.WriteBatch(ops)
			if invalid {
				if err != ErrInvalidOp {
					t.Fatalf("step %d: invalid batch err = %v", step, err)
				}
				break // model unchanged: nothing published
			}
			if err != nil {
				t.Fatalf("step %d: valid batch failed: %v", step, err)
			}
			version++
			cur := make(map[string]string, len(model[version-1]))
			for k, v := range model[version-1] {
				cur[k] = v
			}
			for _, op := range ops {
				if op.Delete {
					delete(cur, op.Key)
				} else {
					cur[op.Key] = op.Value
				}
				touched[op.Key] = true
			}
			model[version] = cur

		case 2: // open a snapshot
			snap := s.BeginRead()
			live = append(live, liveSnap{snap, model[snap.Version()]})

		case 3: // close a random live snapshot (sometimes twice: idempotency)
			if len(live) == 0 {
				break
			}
			i := rng.Intn(len(live))
			if err := live[i].snap.EndRead(); err != nil {
				t.Fatalf("step %d: EndRead: %v", step, err)
			}
			if rng.Intn(4) == 0 {
				if err := live[i].snap.EndRead(); err != nil {
					t.Fatalf("step %d: second EndRead not idempotent: %v", step, err)
				}
			}
			live = append(live[:i], live[i+1:]...)

		case 4, 5: // read a random key from a random live snapshot
			if len(live) == 0 {
				break
			}
			ls := live[rng.Intn(len(live))]
			k := keys[rng.Intn(numKeys)]
			got, err := ls.snap.Get(k)
			want, ok := ls.data[k]
			if !ok {
				if err != ErrNotFound {
					t.Fatalf("step %d: Get(%q) @v%d err = %v, want ErrNotFound",
						step, k, ls.snap.Version(), err)
				}
				break
			}
			if err != nil || got != want {
				t.Fatalf("step %d: Get(%q) @v%d = %q,%v; model wants %q",
					step, k, ls.snap.Version(), got, err, want)
			}
		}
	}

	peak := s.Stats().Peak
	for _, ls := range live {
		if err := ls.snap.EndRead(); err != nil {
			t.Fatal(err)
		}
	}
	final := s.Stats()
	// After the last reader closes, reclamation must leave exactly one node
	// per touched key: the newest node (a value, or a tombstone for a
	// deleted key, which must be kept so future readers see "not found").
	chains := len(touched)
	if final.Retained != int64(chains) {
		t.Fatalf("after all readers closed: retained = %d, want %d (one node per touched key)",
			final.Retained, chains)
	}
	t.Logf("random history: %d steps, %d committed versions; retained nodes peak=%d, after final reclaim=%d (touched keys=%d, live keys=%d)",
		steps, version, peak, final.Retained, chains, len(model[version]))
}

// TestConcurrentRace hammers the store from many goroutines so that
// `go test -race` can validate the lock-free read path and reclamation.
func TestConcurrentRace(t *testing.T) {
	s := NewStore()
	const readers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Single writer: serial commits of put/delete batches.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			k := fmt.Sprintf("k%d", i%4)
			if i%3 == 2 {
				_ = s.WriteBatch([]Op{Del(k)})
			} else {
				_ = s.WriteBatch([]Op{Put(k, fmt.Sprintf("v%d", i))})
			}
		}
		close(stop)
	}()

	// Readers: pin a snapshot, read all keys, close (reclaim runs here).
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.BeginRead()
				for i := 0; i < 4; i++ {
					_, _ = snap.Get(fmt.Sprintf("k%d", i))
				}
				_ = snap.EndRead()
			}
		}()
	}
	wg.Wait()
}
