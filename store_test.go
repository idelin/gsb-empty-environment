package snapshotstore

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, s *Store, ops ...Op) {
	t.Helper()
	if err := s.WriteBatch(ops); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
}

func mustGet(t *testing.T, snap *Snapshot, key string) (string, bool) {
	t.Helper()
	v, ok, err := snap.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return v, ok
}

// A reader pinned to an old version keeps seeing it while new commits land;
// a new reader sees only the newest version. No reader mixes versions.
func TestSnapshotIsolation(t *testing.T) {
	s := New()
	mustWrite(t, s, Op{Key: "a", Value: "a1"}, Op{Key: "b", Value: "b1"}) // v1

	old := s.BeginRead()                                                  // pinned at v1
	mustWrite(t, s, Op{Key: "a", Value: "a2"})                            // v2
	mustWrite(t, s, Op{Key: "a", Value: "a3"}, Op{Key: "c", Value: "c3"}) // v3

	if v, ok := mustGet(t, old, "a"); !ok || v != "a1" {
		t.Fatalf("old snapshot: got (%q,%v), want (a1,true)", v, ok)
	}
	if _, ok := mustGet(t, old, "c"); ok {
		t.Fatal("old snapshot must not see key committed after it began")
	}

	cur := s.BeginRead() // pinned at v3
	if v, ok := mustGet(t, cur, "a"); !ok || v != "a3" {
		t.Fatalf("new snapshot: got (%q,%v), want (a3,true)", v, ok)
	}
	if v, ok := mustGet(t, cur, "b"); !ok || v != "b1" {
		t.Fatalf("new snapshot: got (%q,%v), want (b1,true)", v, ok)
	}
	old.EndRead()
	cur.EndRead()
}

// Deleted keys vanish in the new version but stay visible to old readers.
func TestDeleteVisibility(t *testing.T) {
	s := New()
	mustWrite(t, s, Op{Key: "k", Value: "v1"}) // v1
	old := s.BeginRead()

	mustWrite(t, s, Op{Key: "k", Delete: true}) // v2
	cur := s.BeginRead()

	if _, ok := mustGet(t, cur, "k"); ok {
		t.Fatal("deleted key must be invisible in the new version")
	}
	if v, ok := mustGet(t, old, "k"); !ok || v != "v1" {
		t.Fatalf("old reader must still see old value, got (%q,%v)", v, ok)
	}
	old.EndRead()
	cur.EndRead()
}

// Reads after close fail with ErrSnapshotClosed; closing twice is a no-op.
func TestClosedSnapshotSemantics(t *testing.T) {
	s := New()
	mustWrite(t, s, Op{Key: "k", Value: "v"})
	snap := s.BeginRead()

	snap.EndRead()
	snap.EndRead() // idempotent: must not panic or corrupt state
	snap.EndRead()

	if _, _, err := snap.Get("k"); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("Get after close: got %v, want ErrSnapshotClosed", err)
	}
	if got := s.Stats().LiveReaders; got != 0 {
		t.Fatalf("LiveReaders = %d, want 0 after idempotent closes", got)
	}
}

// A batch containing an invalid op is rejected wholesale: no new version and
// no partial key updates become visible.
func TestFailedBatchNotPublished(t *testing.T) {
	s := New()
	mustWrite(t, s, Op{Key: "x", Value: "1"}) // v1
	before := s.Stats()

	err := s.WriteBatch([]Op{
		{Key: "x", Value: "2"},
		{Key: "", Value: "bad"}, // invalid: rejects the whole batch
		{Key: "y", Value: "3"},
	})
	if !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("got %v, want ErrInvalidOp", err)
	}

	after := s.Stats()
	if after.Version != before.Version {
		t.Fatalf("failed batch bumped version %d -> %d", before.Version, after.Version)
	}
	snap := s.BeginRead()
	defer snap.EndRead()
	if v, ok := mustGet(t, snap, "x"); !ok || v != "1" {
		t.Fatalf("half batch leaked: x = (%q,%v), want (1,true)", v, ok)
	}
	if _, ok := mustGet(t, snap, "y"); ok {
		t.Fatal("half batch leaked: y must not exist")
	}
}

// Controlled barrier scenario: a reader holds the old version while the
// writer overwrites and then deletes the same key; a second reader observes
// the new version; reclamation is only allowed once the old reader closes.
func TestConcurrencyBarrier(t *testing.T) {
	s := New()
	mustWrite(t, s, Op{Key: "k", Value: "v1"}) // v1

	r1 := s.BeginRead() // pinned at v1, holds the old version

	overwritten := make(chan struct{})
	deleted := make(chan struct{})
	oldReadDone := make(chan struct{})

	// Writer: overwrite k, then delete k, in two consecutive commits.
	go func() {
		mustWrite(t, s, Op{Key: "k", Value: "v2"}) // v2
		close(overwritten)
		mustWrite(t, s, Op{Key: "k", Delete: true}) // v3
		close(deleted)
	}()

	// Old reader reads while the writer is between the two commits; being
	// pinned at v1 it must observe v1 no matter when it runs.
	go func() {
		<-overwritten
		if v, ok := mustGet(t, r1, "k"); !ok || v != "v1" {
			t.Errorf("r1 mid-write: got (%q,%v), want (v1,true)", v, ok)
		}
		close(oldReadDone)
	}()

	<-deleted
	<-oldReadDone

	// A second reader begins at v3: the key is gone for it.
	r2 := s.BeginRead()
	if _, ok := mustGet(t, r2, "k"); ok {
		t.Error("r2 must not see the deleted key")
	}
	// The old reader still sees the pre-delete value.
	if v, ok := mustGet(t, r1, "k"); !ok || v != "v1" {
		t.Errorf("r1 after delete: got (%q,%v), want (v1,true)", v, ok)
	}

	// All three versions of k (v1 value, v2 value, v3 tombstone) are retained
	// while r1 is alive.
	if got := s.Stats().RetainedNodes; got != 3 {
		t.Fatalf("RetainedNodes with old reader alive = %d, want 3", got)
	}
	if freed := s.Reclaim(); freed != 0 {
		t.Fatalf("Reclaim freed %d nodes while old reader still needs them", freed)
	}

	// Only after the old reader closes may reclamation collect the chain.
	r1.EndRead()
	r2.EndRead()
	freed := s.Reclaim()
	if freed != 3 {
		t.Fatalf("Reclaim after readers closed freed %d, want 3", freed)
	}
	if got := s.Stats().RetainedNodes; got != 0 {
		t.Fatalf("RetainedNodes after reclaim = %d, want 0", got)
	}
}

// Random short histories are checked against a serial version model: every
// snapshot read must match the model state at the snapshot's version, no
// matter when reclamation runs. Reports retained-node peak and the
// post-reclaim count.
func TestRandomHistoryVsSerialModel(t *testing.T) {
	const (
		steps   = 400
		numKeys = 8
		seeds   = 5
	)
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := New()

			// model[v] is the full key state at version v.
			model := []map[string]string{{}}
			type liveSnap struct {
				snap *Snapshot
				ver  int
			}
			var live []liveSnap

			keys := make([]string, numKeys)
			for i := range keys {
				keys[i] = fmt.Sprintf("k%d", i)
			}

			checkSnap := func(ls liveSnap) {
				t.Helper()
				for _, k := range keys {
					want, wantOK := model[ls.ver][k]
					got, ok := mustGet(t, ls.snap, k)
					if ok != wantOK || (ok && got != want) {
						t.Fatalf("snap@v%d key %s: got (%q,%v), want (%q,%v)",
							ls.ver, k, got, ok, want, wantOK)
					}
				}
			}

			for step := 0; step < steps; step++ {
				switch rng.Intn(6) {
				case 0, 1: // commit a batch (sometimes invalid)
					n := 1 + rng.Intn(3)
					ops := make([]Op, 0, n)
					for i := 0; i < n; i++ {
						k := keys[rng.Intn(numKeys)]
						if rng.Intn(4) == 0 {
							ops = append(ops, Op{Key: k, Delete: true})
						} else {
							ops = append(ops, Op{Key: k, Value: fmt.Sprintf("s%d-i%d", seed, step)})
						}
					}
					if rng.Intn(12) == 0 {
						ops = append(ops, Op{Key: ""}) // poison the batch
						verBefore := s.Stats().Version
						if err := s.WriteBatch(ops); !errors.Is(err, ErrInvalidOp) {
							t.Fatalf("got %v, want ErrInvalidOp", err)
						}
						if got := s.Stats().Version; got != verBefore {
							t.Fatalf("failed batch changed version %d -> %d", verBefore, got)
						}
						break
					}
					mustWrite(t, s, ops...)
					next := make(map[string]string, len(model[len(model)-1]))
					for k, v := range model[len(model)-1] {
						next[k] = v
					}
					for _, op := range ops {
						if op.Delete {
							delete(next, op.Key)
						} else {
							next[op.Key] = op.Value
						}
					}
					model = append(model, next)
				case 2: // begin a reader
					snap := s.BeginRead()
					if want := uint64(len(model) - 1); snap.Version() != want {
						t.Fatalf("BeginRead pinned v%d, model at v%d", snap.Version(), want)
					}
					live = append(live, liveSnap{snap, int(snap.Version())})
				case 3: // read through a random live snapshot
					if len(live) > 0 {
						checkSnap(live[rng.Intn(len(live))])
					}
				case 4: // close a random reader
					if len(live) > 0 {
						i := rng.Intn(len(live))
						live[i].snap.EndRead()
						live[i].snap.EndRead() // idempotent
						live = append(live[:i], live[i+1:]...)
					}
				case 5: // reclaim, then prove live snapshots are unaffected
					s.Reclaim()
					for _, ls := range live {
						checkSnap(ls)
					}
				}
			}

			st := s.Stats()
			t.Logf("versions=%d retained-peak=%d retained-before-final-reclaim=%d live-readers=%d",
				st.Version, st.PeakNodes, st.RetainedNodes, st.LiveReaders)

			// Drain all readers and reclaim: only the latest version's live
			// keys may remain, one node each.
			for _, ls := range live {
				checkSnap(ls) // still correct right up to close
				ls.snap.EndRead()
			}
			s.Reclaim()
			final := s.Stats()
			latest := model[len(model)-1]
			if final.RetainedNodes != len(latest) {
				t.Fatalf("post-reclaim RetainedNodes = %d, want %d (latest live keys)",
					final.RetainedNodes, len(latest))
			}
			t.Logf("post-reclaim retained=%d latest-keys=%d", final.RetainedNodes, len(latest))
		})
	}
}

// Deterministic race check: the writer commits val-<version> for the same
// key on every version, so every reader can verify its pinned version exactly
// while commits and reclamation race around it. Run with -race.
func TestConcurrentReadersWriterReclaimer(t *testing.T) {
	s := New()
	const batches = 300

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { // single serialized writer
		defer wg.Done()
		for i := 1; i <= batches; i++ {
			mustWrite(t, s, Op{Key: "k", Value: fmt.Sprintf("val-%d", i)})
		}
		close(stop)
	}()

	wg.Add(1)
	go func() { // reclaimer racing with readers
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.Reclaim()
			}
		}
	}()

	for r := 0; r < 8; r++ { // readers never hold any global lock
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
				v := snap.Version()
				got, ok, err := snap.Get("k")
				if err != nil {
					t.Errorf("Get: %v", err)
				} else if v == 0 {
					if ok {
						t.Errorf("v0 snapshot saw key %q", got)
					}
				} else if want := fmt.Sprintf("val-%d", v); !ok || got != want {
					t.Errorf("snap@v%d got (%q,%v), want (%q,true)", v, got, ok, want)
				}
				snap.EndRead()
			}
		}()
	}
	wg.Wait()

	s.Reclaim()
	if got := s.Stats().RetainedNodes; got != 1 {
		t.Fatalf("post-drain RetainedNodes = %d, want 1", got)
	}
}
