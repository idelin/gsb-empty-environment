package lockfreemvcc

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newSeededStore(numKeys int, value int64) *Store {
	s := NewStore(numKeys)
	for i := 0; i < numKeys; i++ {
		s.Seed(i, value)
	}
	return s
}

// TestSnapshotIsolation verifies that an open snapshot keeps observing a
// stable view while commits land, and that new snapshots observe them.
func TestSnapshotIsolation(t *testing.T) {
	s := newSeededStore(4, 100)

	snap := s.Begin()
	defer snap.Close()

	s.Commit(Mutation{Key: 0, Value: 1}, Mutation{Key: 1, Value: 2})
	s.Transfer(2, 3, 40)

	for i, want := range []int64{100, 100, 100, 100} {
		if got := snap.Get(i); got != want {
			t.Fatalf("stale snapshot key %d: got %d, want %d", i, got, want)
		}
	}

	fresh := s.Begin()
	defer fresh.Close()
	for i, want := range []int64{1, 2, 60, 140} {
		if got := fresh.Get(i); got != want {
			t.Fatalf("fresh snapshot key %d: got %d, want %d", i, got, want)
		}
	}
}

// TestMultiKeyCommitAtomicity verifies a multi-key commit is visible
// all-or-nothing to any snapshot.
func TestMultiKeyCommitAtomicity(t *testing.T) {
	s := newSeededStore(2, 500)
	before := s.Begin()
	s.Commit(Mutation{Key: 0, Value: 400}, Mutation{Key: 1, Value: 600})
	after := s.Begin()
	defer before.Close()
	defer after.Close()

	if got := before.Get(0) + before.Get(1); got != 1000 {
		t.Fatalf("pre-commit snapshot sum = %d, want 1000", got)
	}
	if got := after.Get(0) + after.Get(1); got != 1000 {
		t.Fatalf("post-commit snapshot sum = %d, want 1000", got)
	}
	if after.Get(0) != 400 || after.Get(1) != 600 {
		t.Fatalf("post-commit values = %d,%d, want 400,600", after.Get(0), after.Get(1))
	}
}

// TestGCReclaimsAndKeepsVisibleVersions verifies that an old pinned
// snapshot prevents reclamation of the versions it can observe, that GC
// truncates chains once no reader needs the old versions, and that both
// the pinned and fresh snapshots keep reading correct values.
func TestGCReclaimsAndKeepsVisibleVersions(t *testing.T) {
	s := newSeededStore(2, 0)

	pinned := s.Begin() // pins ts=1: GC must keep the seed versions

	const commits = 3000
	for i := 0; i < commits; i++ {
		s.Transfer(0, 1, 1)
	}

	// The pinned snapshot blocks reclamation of everything it might read.
	if got := s.chainLen(0); got != commits+1 {
		t.Fatalf("chain length with pinned reader = %d, want %d", got, commits+1)
	}
	if got := pinned.Get(0) + pinned.Get(1); got != 0 {
		t.Fatalf("pinned snapshot sum = %d, want 0", got)
	}
	pinned.Close()

	// With no active readers, GC truncates each chain to the single
	// version visible at the latest commit.
	s.gc()
	if got := s.chainLen(0); got != 1 {
		t.Fatalf("chain length after GC = %d, want 1", got)
	}

	fresh := s.Begin()
	defer fresh.Close()
	if got := fresh.Get(0) + fresh.Get(1); got != 0 {
		t.Fatalf("fresh snapshot sum = %d, want 0", got)
	}
	if fresh.Get(1) != commits {
		t.Fatalf("fresh key1 = %d, want %d", fresh.Get(1), commits)
	}
}

// TestReadersNeverBlockOnWriter hammers a single key from the writer
// while readers scan it; readers must make uninterrupted progress.
func TestReadersNeverBlockOnWriter(t *testing.T) {
	s := newSeededStore(8, 7)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.Transfer(0, 1, 1)
			}
		}
	}()

	var scans atomic.Int64
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Begin()
				var sum int64
				for k := 0; k < s.NumKeys(); k++ {
					sum += snap.Get(k)
				}
				snap.Close()
				if sum != 8*7 {
					t.Errorf("torn read: sum = %d, want %d", sum, 8*7)
					return
				}
				scans.Add(1)
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
	if scans.Load() == 0 {
		t.Fatal("readers made no progress while writer was active")
	}
	t.Logf("completed %d consistent scans during concurrent writes", scans.Load())
}

// TestConservationUnderContention is the acceptance boundary:
// 1 writer thread densely updates 1000 keys via sum-preserving transfers
// while 5 reader threads continuously run full-table checksum scans.
// Every snapshot must observe the conserved total: zero torn reads.
func TestConservationUnderContention(t *testing.T) {
	const (
		numKeys    = 1000
		perKey     = 1000
		numReaders = 5
		duration   = 2 * time.Second
	)
	const wantSum = int64(numKeys * perKey)

	s := newSeededStore(numKeys, perKey)

	var commits, scans, mismatches atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 1 writer thread: dense read-modify-write transfers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(1))
		for {
			select {
			case <-stop:
				return
			default:
			}
			from := rng.Intn(numKeys)
			to := rng.Intn(numKeys)
			if from == to {
				continue
			}
			s.Transfer(from, to, int64(rng.Intn(10)+1))
			commits.Add(1)
		}
	}()

	// 5 reader threads: continuous full-table checksum scans.
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Begin()
				var sum int64
				for k := 0; k < s.NumKeys(); k++ {
					sum += snap.Get(k)
				}
				snap.Close()
				scans.Add(1)
				if sum != wantSum {
					if mismatches.Add(1) <= 5 {
						t.Errorf("reader %d: torn snapshot, sum = %d, want %d", id, sum, wantSum)
					}
				}
			}
		}(r)
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	if got := mismatches.Load(); got != 0 {
		t.Fatalf("observed %d torn snapshots", got)
	}
	if commits.Load() == 0 || scans.Load() == 0 {
		t.Fatalf("no progress: commits=%d scans=%d", commits.Load(), scans.Load())
	}
	t.Logf("OK: %d commits, %d full-table scans in %v, %d torn reads (sum conserved at %d)",
		commits.Load(), scans.Load(), duration, mismatches.Load(), wantSum)
	fmt.Printf("commits=%d scans=%d torn=%d\n", commits.Load(), scans.Load(), mismatches.Load())
}
