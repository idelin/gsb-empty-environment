package tagcache

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSource returns a DataSource serving fixed entries per tag and counts
// Release calls so tests can verify resources are freed.
type fakeSource struct {
	entries  map[string][]Entry
	err      error
	released *atomic.Int64
	// block, when non-nil, makes the load wait until the channel is closed.
	block <-chan struct{}
	// started, when non-nil, is closed once the load begins.
	started chan<- struct{}
}

func (f fakeSource) load(tag string, gen uint64) (LoadResult, error) {
	if f.started != nil {
		close(f.started)
		f.started = nil
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return LoadResult{}, f.err
	}
	entries := append([]Entry(nil), f.entries[tag]...)
	return LoadResult{
		Entries: entries,
		Release: func() { f.released.Add(1) },
	}, nil
}

func TestLoadAndReverseIndex(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"news": {{ID: "a"}, {ID: "b"}}},
		released: &released,
	}
	s := New(src.load, nil, nil)

	if err := s.LoadTag("news"); err != nil {
		t.Fatalf("LoadTag: %v", err)
	}
	got := s.EntriesForTag("news")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("EntriesForTag = %v", got)
	}
	if released.Load() != 1 {
		t.Fatalf("Release called %d times, want 1", released.Load())
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteEntryCleansReverseIndex(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries: map[string][]Entry{
			"t1": {{ID: "a"}, {ID: "b"}},
			"t2": {{ID: "a"}},
		},
		released: &released,
	}
	s := New(src.load, nil, nil)
	mustLoad(t, s, "t1", "t2")

	s.DeleteEntry("a")
	if got := s.EntriesForTag("t1"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("t1 after delete = %v", got)
	}
	if got := s.EntriesForTag("t2"); len(got) != 0 {
		t.Fatalf("t2 after delete = %v", got)
	}
	if got := s.TagsOf("a"); got != nil {
		t.Fatalf("TagsOf(a) = %v", got)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidateTagInvalidatesEntries(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"t1": {{ID: "a"}, {ID: "b"}}, "t2": {{ID: "a"}}},
		released: &released,
	}
	s := New(src.load, nil, nil)
	mustLoad(t, s, "t1", "t2")

	s.InvalidateTag("t1")
	if got := s.EntriesForTag("t1"); len(got) != 0 {
		t.Fatalf("t1 after invalidate = %v", got)
	}
	// "a" survives only through t2.
	if got := s.TagsOf("a"); len(got) != 1 || got[0] != "t2" {
		t.Fatalf("TagsOf(a) = %v", got)
	}
	// "b" had no other tag, so it is gone entirely.
	if got := s.BillableEntries(); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("BillableEntries = %v", got)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteTagKeepsOtherTagRelations(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"t1": {{ID: "a"}}, "t2": {{ID: "a"}, {ID: "c"}}},
		released: &released,
	}
	s := New(src.load, nil, nil)
	mustLoad(t, s, "t1", "t2")

	s.DeleteTag("t1")
	// Entry "a" must keep its t2 relation; t2's index is untouched.
	if got := s.TagsOf("a"); len(got) != 1 || got[0] != "t2" {
		t.Fatalf("TagsOf(a) = %v", got)
	}
	if got := s.EntriesForTag("t2"); len(got) != 2 {
		t.Fatalf("t2 entries = %v", got)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleGenerationDiscardsLoadAndReleases(t *testing.T) {
	var released atomic.Int64
	block := make(chan struct{})
	started := make(chan struct{})
	src := fakeSource{
		entries:  map[string][]Entry{"t1": {{ID: "a"}}},
		released: &released,
		block:    block,
		started:  started,
	}
	s := New(src.load, nil, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- s.LoadTag("t1") }()
	<-started             // load is in flight
	s.InvalidateTag("t1") // version changes mid-load
	close(block)          // let the load finish

	if err := <-errCh; !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("LoadTag err = %v, want ErrStaleGeneration", err)
	}
	if got := s.EntriesForTag("t1"); len(got) != 0 {
		t.Fatalf("stale entries committed: %v", got)
	}
	if released.Load() != 1 {
		t.Fatalf("Release called %d times, want 1 (resources freed on discard)", released.Load())
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestFailureInjectionStillReleases(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"t1": {{ID: "a"}}},
		released: &released,
	}
	injected := errors.New("boom")
	fail := func(point string) error {
		if point == "loadtag.commit" {
			return injected
		}
		return nil
	}
	s := New(src.load, nil, fail)

	if err := s.LoadTag("t1"); !errors.Is(err, injected) {
		t.Fatalf("LoadTag err = %v, want injected", err)
	}
	if got := s.EntriesForTag("t1"); len(got) != 0 {
		t.Fatalf("entries committed despite failure: %v", got)
	}
	if released.Load() != 1 {
		t.Fatalf("Release called %d times, want 1 (resources freed on failure)", released.Load())
	}
}

func TestSourceErrorPropagates(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{err: errors.New("backend down"), released: &released}
	s := New(src.load, nil, nil)
	if err := s.LoadTag("t1"); err == nil {
		t.Fatal("expected error")
	}
	if released.Load() != 0 {
		t.Fatalf("Release called on failed load: %d", released.Load())
	}
}

func TestBillingCountsEntryOnceAcrossTags(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries: map[string][]Entry{
			"t1": {{ID: "a"}, {ID: "b"}},
			"t2": {{ID: "a"}}, // "a" linked to two tags
		},
		released: &released,
	}
	s := New(src.load, nil, nil)
	mustLoad(t, s, "t1", "t2")

	bill := s.BillableEntries()
	if len(bill) != 2 {
		t.Fatalf("BillableEntries = %v, want 2 distinct entries (no double charge)", bill)
	}
	seen := map[string]int{}
	for _, e := range bill {
		seen[e.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("entry %q charged %d times", id, n)
		}
	}
}

func TestClockInjection(t *testing.T) {
	var released atomic.Int64
	fixed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	src := fakeSource{
		entries:  map[string][]Entry{"t1": {{ID: "a"}}},
		released: &released,
	}
	s := New(src.load, func() time.Time { return fixed }, nil)
	mustLoad(t, s, "t1")

	got, ok := s.LoadedAt("a")
	if !ok || !got.Equal(fixed) {
		t.Fatalf("LoadedAt = %v, %v; want %v", got, ok, fixed)
	}
}

// TestConcurrentInvalidateAndLoad hammers the store with concurrent loads
// and invalidations, then asserts no stale-generation entry remains valid.
func TestConcurrentInvalidateAndLoad(t *testing.T) {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"hot": {{ID: "a"}, {ID: "b"}, {ID: "c"}}},
		released: &released,
	}
	s := New(src.load, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = s.LoadTag("hot") // ErrStaleGeneration is fine
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.InvalidateTag("hot")
			}
		}()
	}
	wg.Wait()

	// After the dust settles, one final load at the current generation.
	if err := s.LoadTag("hot"); err != nil {
		t.Fatalf("final LoadTag: %v", err)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("stale valid entries survived the race: %v", err)
	}
	if got := s.EntriesForTag("hot"); len(got) != 3 {
		t.Fatalf("final entries = %v", got)
	}
}

func mustLoad(t *testing.T, s *Store, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		if err := s.LoadTag(tag); err != nil {
			t.Fatalf("LoadTag(%q): %v", tag, err)
		}
	}
}

func Example() {
	var released atomic.Int64
	src := fakeSource{
		entries:  map[string][]Entry{"go": {{ID: "1", Data: "generics"}, {ID: "2", Data: "slices"}}},
		released: &released,
	}
	s := New(src.load, nil, nil)
	if err := s.LoadTag("go"); err != nil {
		panic(err)
	}
	for _, e := range s.EntriesForTag("go") {
		fmt.Println(e.ID, e.Data)
	}
	fmt.Println("released:", released.Load())
	// Output:
	// 1 generics
	// 2 slices
	// released: 1
}
