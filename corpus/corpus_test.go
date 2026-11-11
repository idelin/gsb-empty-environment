package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var errInjected = errors.New("injected disk failure")

func mustOpen(t *testing.T, dir string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(dir, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustAdd(t *testing.T, s *Store, sample []byte, edges ...uint64) {
	t.Helper()
	if _, err := s.Add(sample, edges); err != nil {
		t.Fatalf("Add(%q): %v", sample, err)
	}
}

func coverageSet(t *testing.T, s *Store) map[uint64]bool {
	t.Helper()
	cov, err := s.Coverage()
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	set := map[uint64]bool{}
	for _, e := range cov {
		set[e] = true
	}
	return set
}

func requireCovered(t *testing.T, s *Store, edges ...uint64) {
	t.Helper()
	set := coverageSet(t, s)
	for _, e := range edges {
		if !set[e] {
			t.Fatalf("edge %d lost from coverage", e)
		}
	}
}

// countOps returns how many hooked disk operations fn performs.
func countOps(t *testing.T, s *Store, fn func() error) int {
	t.Helper()
	n := 0
	s.hook = func(op string) error { n++; return nil }
	if err := fn(); err != nil {
		t.Fatalf("counting run failed: %v", err)
	}
	s.hook = nil
	return n
}

// failAt returns a hook that fails the n-th hooked operation.
func failAt(n int) (func(op string) error, *int) {
	count := new(int)
	return func(op string) error {
		*count++
		if *count == n {
			return errInjected
		}
		return nil
	}, count
}

func TestWriteFailuresConserveCoverage(t *testing.T) {
	// Measure the disk-op count of a successful Add.
	probeDir := t.TempDir()
	probe := mustOpen(t, probeDir)
	total := countOps(t, probe, func() error {
		_, err := probe.Add([]byte("probe-sample"), []uint64{1})
		return err
	})
	probe.Close()
	if total == 0 {
		t.Fatal("no hooked operations observed")
	}

	// Fail the Add at each successive disk operation. Whatever fails, the
	// previously acknowledged coverage must survive and the published index
	// must stay consistent with the files on disk.
	for step := 1; step <= total; step++ {
		t.Run(fmt.Sprintf("add-fails-at-op-%d", step), func(t *testing.T) {
			dir := t.TempDir()
			s := mustOpen(t, dir)
			defer s.Close()
			mustAdd(t, s, []byte("baseline"), 100, 101)

			hook, _ := failAt(step)
			s.hook = hook
			_, err := s.Add([]byte("victim-sample"), []uint64{200})
			if !errors.Is(err, errInjected) {
				t.Fatalf("expected injected error, got %v", err)
			}
			s.hook = nil

			requireCovered(t, s, 100, 101)
			if err := s.Validate(); err != nil {
				t.Fatalf("Validate after failed Add: %v", err)
			}
			// The store must keep working after the failure.
			mustAdd(t, s, []byte("after-failure"), 300)
			requireCovered(t, s, 100, 101, 300)
		})
	}

	// Same for Compact: fail at each successive disk operation.
	compactDir := t.TempDir()
	compactProbe := mustOpen(t, compactDir)
	mustAdd(t, compactProbe, []byte("aaaa-redundant-long"), 1, 2)
	mustAdd(t, compactProbe, []byte("bb"), 1)
	mustAdd(t, compactProbe, []byte("cc"), 2)
	totalCompact := countOps(t, compactProbe, compactProbe.Compact)
	compactProbe.Close()
	if totalCompact == 0 {
		t.Fatal("compact performed no hooked operations")
	}

	for step := 1; step <= totalCompact; step++ {
		t.Run(fmt.Sprintf("compact-fails-at-op-%d", step), func(t *testing.T) {
			dir := t.TempDir()
			s := mustOpen(t, dir)
			defer s.Close()
			mustAdd(t, s, []byte("aaaa-redundant-long"), 1, 2)
			mustAdd(t, s, []byte("bb"), 1)
			mustAdd(t, s, []byte("cc"), 2)

			hook, _ := failAt(step)
			s.hook = hook
			if err := s.Compact(); !errors.Is(err, errInjected) {
				t.Fatalf("expected injected error, got %v", err)
			}
			s.hook = nil

			requireCovered(t, s, 1, 2)
			if err := s.Validate(); err != nil {
				t.Fatalf("Validate after failed Compact: %v", err)
			}
		})
	}

	// Persistent failure: every operation fails, coverage stays put.
	t.Run("persistent-failure", func(t *testing.T) {
		dir := t.TempDir()
		s := mustOpen(t, dir)
		defer s.Close()
		mustAdd(t, s, []byte("baseline"), 7)
		s.hook = func(op string) error { return errInjected }
		for i := 0; i < 3; i++ {
			if _, err := s.Add([]byte("x"), []uint64{8}); !errors.Is(err, errInjected) {
				t.Fatalf("expected injected error, got %v", err)
			}
		}
		s.hook = nil
		requireCovered(t, s, 7)
		if err := s.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}

func TestDuplicateDigest(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	defer s.Close()

	mustAdd(t, s, []byte("same-content"), 1, 2)
	changed, err := s.Add([]byte("same-content"), []uint64{2, 3})
	if err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if !changed {
		t.Fatal("merging new edges into existing digest should report a change")
	}
	changed, err = s.Add([]byte("same-content"), []uint64{1, 3})
	if err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if changed {
		t.Fatal("re-adding known content with no new edges should be a no-op")
	}

	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !equalEdges(entries[0].Edges, []uint64{1, 2, 3}) {
		t.Fatalf("merged edges = %v, want [1 2 3]", entries[0].Edges)
	}
	requireCovered(t, s, 1, 2, 3)

	// Duplicate submissions racing across processes must still converge.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other, err := Open(dir)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer other.Close()
			if _, err := other.Add([]byte("raced"), []uint64{uint64(10 + i)}); err != nil {
				t.Errorf("raced Add: %v", err)
			}
		}(i)
	}
	wg.Wait()
	requireCovered(t, s, 1, 2, 3, 10, 11, 12, 13)
	entries, err = s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after raced duplicate adds, got %d", len(entries))
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestConcurrentAddAndCompact(t *testing.T) {
	dir := t.TempDir()
	const workers = 6
	const addsPerWorker = 30

	var mu sync.Mutex
	allEdges := map[uint64]bool{}
	stop := make(chan struct{})
	var compactors, adders sync.WaitGroup

	for i := 0; i < 2; i++ {
		compactors.Add(1)
		go func() {
			defer compactors.Done()
			s, err := Open(dir)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer s.Close()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Compact(); err != nil {
					t.Errorf("Compact: %v", err)
					return
				}
			}
		}()
	}

	for w := 0; w < workers; w++ {
		adders.Add(1)
		go func(seed int64) {
			defer adders.Done()
			s, err := Open(dir)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer s.Close()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < addsPerWorker; i++ {
				n := 1 + r.Intn(4)
				edges := make([]uint64, n)
				for j := range edges {
					edges[j] = uint64(r.Intn(32))
				}
				var sample []byte
				if i%3 == 0 {
					// Shared content across workers exercises the
					// duplicate-digest merge path concurrently.
					sample = []byte(fmt.Sprintf("shared-%d", i%5))
				} else {
					sample = make([]byte, 1+r.Intn(24))
					r.Read(sample)
				}
				if _, err := s.Add(sample, edges); err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				mu.Lock()
				for _, e := range edges {
					allEdges[e] = true
				}
				mu.Unlock()
			}
		}(int64(w))
	}

	adders.Wait()
	close(stop)
	compactors.Wait()

	s := mustOpen(t, dir)
	defer s.Close()
	if err := s.Compact(); err != nil {
		t.Fatalf("final Compact: %v", err)
	}
	set := coverageSet(t, s)
	for e := range allEdges {
		if !set[e] {
			t.Errorf("edge %d lost after concurrent adds and compactions", e)
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRecoveryDropsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	mustAdd(t, s, []byte("keep-me"), 1)
	mustAdd(t, s, []byte("lose-me"), 2)
	sum := sha256.Sum256([]byte("lose-me"))
	lost := hex.EncodeToString(sum[:])
	s.Close()

	// Simulate a torn state: the index still references a sample file that
	// no longer exists on disk.
	if err := os.Remove(filepath.Join(dir, "samples", lost)); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	s = mustOpen(t, dir)
	defer s.Close()
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate after recovery: %v", err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries after recovery: %v", err)
	}
	for _, e := range entries {
		if e.Digest == lost {
			t.Fatalf("recovered index still references missing sample %s", lost)
		}
	}
	requireCovered(t, s, 1)
}

func TestCompactPrefersShorter(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	defer s.Close()

	mustAdd(t, s, []byte("0123456789"), 1, 2) // long, fully redundant
	mustAdd(t, s, []byte("aaa"), 1)
	mustAdd(t, s, []byte("bbb"), 2)
	mustAdd(t, s, []byte("zz"), 1, 2) // shortest, covers both edges

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Length != 2 {
		t.Fatalf("expected only the shortest covering sample to survive, got %+v", entries)
	}
	requireCovered(t, s, 1, 2)
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCompactKeepsDeletedEdgesCovered(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	defer s.Close()

	// No single sample covers everything; compaction must keep a covering
	// subset and every dropped sample's edges must remain covered.
	mustAdd(t, s, []byte("s1"), 1, 2)
	mustAdd(t, s, []byte("s22"), 2, 3)
	mustAdd(t, s, []byte("s333"), 3, 4)
	mustAdd(t, s, []byte("redundant-1-2"), 1, 2)
	mustAdd(t, s, []byte("redundant-2-3"), 2, 3)

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	requireCovered(t, s, 1, 2, 3, 4)
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
