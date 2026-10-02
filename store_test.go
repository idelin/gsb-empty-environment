package mvcc

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildAcceptanceStore creates item X with v1(t=1), v2(t=5), v3(t=10).
func buildAcceptanceStore() *Store {
	s := NewStore()
	s.WriteAt("X", "v1", 1)
	s.WriteAt("X", "v2", 5)
	s.WriteAt("X", "v3", 10)
	return s
}

func TestChainIsNewestFirst(t *testing.T) {
	s := buildAcceptanceStore()
	want := []uint64{10, 5, 1}
	if got := s.Versions("X"); !reflect.DeepEqual(got, want) {
		t.Fatalf("chain = %v, want %v (descending commit time)", got, want)
	}
}

func TestReadVisibility(t *testing.T) {
	s := buildAcceptanceStore()
	cases := []struct {
		ts   uint64
		want string
		ok   bool
	}{
		{0, "", false},
		{1, "v1", true},
		{4, "v1", true},
		{5, "v2", true},
		{9, "v2", true},
		{10, "v3", true},
		{100, "v3", true},
	}
	for _, c := range cases {
		got, ok := s.GetAt("X", c.ts)
		if ok != c.ok || got != c.want {
			t.Errorf("GetAt(X, %d) = %q, %v; want %q, %v", c.ts, got, ok, c.want, c.ok)
		}
	}
	if _, ok := s.GetAt("missing", 100); ok {
		t.Error("GetAt on unknown key returned ok=true")
	}
}

func TestMinActiveReadView(t *testing.T) {
	s := buildAcceptanceStore()
	if _, ok := s.MinActiveRead(); ok {
		t.Fatal("expected no active readers")
	}
	rv8 := s.BeginReadAt(8)
	rv10 := s.BeginRead()
	if got, ok := s.MinActiveRead(); !ok || got != 8 {
		t.Fatalf("min active read = %d, %v; want 8, true", got, ok)
	}
	rv8.Close()
	if got, ok := s.MinActiveRead(); !ok || got != 10 {
		t.Fatalf("min active read = %d, %v; want 10, true", got, ok)
	}
	rv10.Close()
	if _, ok := s.MinActiveRead(); ok {
		t.Fatal("expected no active readers after Close")
	}
}

// Acceptance scenario: X has v1(t=1), v2(t=5), v3(t=10); the global
// minimum active ReadView is t=8; GC must prune v1 while reads at t=8
// and t=10 still return v2 and v3.
func TestGCPrunesCoveredVersions(t *testing.T) {
	s := buildAcceptanceStore()
	rv := s.BeginReadAt(8) // global minimum active read view
	defer rv.Close()

	if n := s.GC(); n != 1 {
		t.Fatalf("GC pruned %d versions, want 1", n)
	}
	if got, want := s.Versions("X"), []uint64{10, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chain after GC = %v, want %v", got, want)
	}
	if got, ok := rv.Get("X"); !ok || got != "v2" {
		t.Fatalf("read @t=8 after GC = %q, %v; want v2, true", got, ok)
	}
	fresh := s.BeginRead() // t=10
	defer fresh.Close()
	if got, ok := fresh.Get("X"); !ok || got != "v3" {
		t.Fatalf("read @t=10 after GC = %q, %v; want v3, true", got, ok)
	}
}

func TestGCKeepsVersionVisibleToOldestReader(t *testing.T) {
	s := buildAcceptanceStore()
	rv := s.BeginReadAt(3) // can only see v1, so nothing may be pruned
	defer rv.Close()
	if n := s.GC(); n != 0 {
		t.Fatalf("GC pruned %d versions, want 0 (v1 still visible @t=3)", n)
	}
	if got, ok := rv.Get("X"); !ok || got != "v1" {
		t.Fatalf("read @t=3 = %q, %v; want v1, true", got, ok)
	}
}

func TestGCWithoutActiveReaders(t *testing.T) {
	s := buildAcceptanceStore()
	if n := s.GC(); n != 2 {
		t.Fatalf("GC pruned %d versions, want 2", n)
	}
	if got, want := s.Versions("X"), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chain after GC = %v, want %v", got, want)
	}
	if got, ok := s.GetAt("X", 10); !ok || got != "v3" {
		t.Fatalf("read after GC = %q, %v; want v3, true", got, ok)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	s.WriteAt("X", "v1", 1)
	rv := s.BeginRead() // snapshot @t=1
	defer rv.Close()
	s.WriteAt("X", "v2", 2)
	if got, ok := rv.Get("X"); !ok || got != "v1" {
		t.Fatalf("snapshot read = %q, %v; want v1, true", got, ok)
	}
	if got, ok := s.GetAt("X", 2); !ok || got != "v2" {
		t.Fatalf("latest read = %q, %v; want v2, true", got, ok)
	}
}

// After pruning, the unlinked v1 node is unreachable and must be
// reclaimed by the Go garbage collector.
func TestGCReclaimsPrunedVersionMemory(t *testing.T) {
	s := buildAcceptanceStore()
	rv := s.BeginReadAt(8)
	defer rv.Close()

	v1 := s.chains["X"].head.Next.Next // tail node, t=1
	if v1 == nil || v1.TS != 1 {
		t.Fatalf("expected tail node t=1, got %+v", v1)
	}
	collected := make(chan struct{}, 1)
	runtime.SetFinalizer(v1, func(*Version) { collected <- struct{}{} })

	if n := s.GC(); n != 1 {
		t.Fatalf("GC pruned %d versions, want 1", n)
	}
	v1 = nil // drop our last reference

	deadline := time.After(5 * time.Second)
	for {
		runtime.GC()
		select {
		case <-collected:
			return // v1 was reclaimed: memory freed
		case <-deadline:
			t.Fatal("pruned v1 node was not reclaimed by the Go GC")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestConcurrentReadWriteGC(t *testing.T) {
	s := NewStore()
	const writes = 500
	stop := make(chan struct{})

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < writes; i++ {
			s.Write("X", fmt.Sprintf("v%d", i))
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 50; i++ {
			s.GC()
		}
	}()

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rv := s.BeginRead()
				rv.Get("X")
				rv.Close()
			}
		}()
	}

	workers.Wait()
	close(stop)
	readers.Wait()

	if got, ok := s.GetAt("X", s.clock); !ok || got != fmt.Sprintf("v%d", writes-1) {
		t.Fatalf("final read = %q, %v; want v%d, true", got, ok, writes-1)
	}
}
