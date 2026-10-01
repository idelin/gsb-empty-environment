package cidx

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustView(t *testing.T, s *Store) *View {
	t.Helper()
	v, err := s.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	return v
}

func TestViewWithoutLoad(t *testing.T) {
	s := NewStore()
	if _, err := s.View(); err != ErrNoMapping {
		t.Fatalf("got %v, want ErrNoMapping", err)
	}
}

func TestQueryAfterClose(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, sampleEntries...)); err != nil {
		t.Fatal(err)
	}
	v := mustView(t, s)
	v.Close()
	if _, err := v.Query([]byte("apple")); err != ErrViewClosed {
		t.Fatalf("got %v, want ErrViewClosed", err)
	}
	if _, err := v.Cursor(); err != ErrViewClosed {
		t.Fatalf("cursor after close: got %v, want ErrViewClosed", err)
	}
}

func TestClosingOneViewKeepsOthers(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, sampleEntries...)); err != nil {
		t.Fatal(err)
	}
	m := s.Mapping()
	v1 := mustView(t, s)
	v2 := mustView(t, s)
	v1.Close()
	if _, err := v1.Query([]byte("apple")); err != ErrViewClosed {
		t.Fatalf("closed view: got %v, want ErrViewClosed", err)
	}
	got, err := v2.Query([]byte("apple"))
	if err != nil || string(got) != "fruit" {
		t.Fatalf("surviving view: %q, %v", got, err)
	}
	if m.TornDown() {
		t.Fatal("mapping torn down while a view is still open")
	}
	v2.Close()
}

func TestCloseDeferredUntilLastBorrow(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, sampleEntries...)); err != nil {
		t.Fatal(err)
	}
	m := s.Mapping()
	v := mustView(t, s)
	c, err := v.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	v.Close() // view borrow released; cursor borrow remains

	m.Close() // request close: must defer while the cursor holds a borrow
	if m.TornDown() {
		t.Fatal("teardown completed before last borrow was released")
	}
	if _, err := OpenView(m); err != ErrMappingClosing {
		t.Fatalf("new borrow after close request: got %v, want ErrMappingClosing", err)
	}
	// Existing borrower still iterates the whole index.
	n := 0
	for {
		_, _, ok, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		n++
	}
	if n != len(sampleEntries) {
		t.Fatalf("cursor saw %d entries, want %d", n, len(sampleEntries))
	}
	c.Close()
	if !m.TornDown() {
		t.Fatal("teardown did not complete after last borrow released")
	}
	select {
	case <-m.Done():
	default:
		t.Fatal("Done channel not closed")
	}
}

func TestOldHandleNotBoundToNewMapping(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, [2]string{"a", "v1"})); err != nil {
		t.Fatal(err)
	}
	m1 := s.Mapping()
	vOld := mustView(t, s)

	if err := s.Load(buildIndex(t, [2]string{"a", "v2"})); err != nil {
		t.Fatal(err)
	}
	m2 := s.Mapping()
	if m1 == m2 {
		t.Fatal("reload did not swap the mapping")
	}
	vNew := mustView(t, s)
	defer vNew.Close()

	if vOld.Generation() == vNew.Generation() {
		t.Fatal("old and new handles share a generation")
	}
	got, err := vOld.Query([]byte("a"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("old handle = %q, %v; want v1", got, err)
	}
	got, err = vNew.Query([]byte("a"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("new handle = %q, %v; want v2", got, err)
	}
	// The old mapping's close was requested at reload; it completes once
	// the old view lets go.
	if m1.TornDown() {
		t.Fatal("old mapping torn down while still borrowed")
	}
	vOld.Close()
	select {
	case <-m1.Done():
	case <-time.After(time.Second):
		t.Fatal("old mapping not torn down after last old handle closed")
	}
}

func TestFailedLoadKeepsMapping(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, sampleEntries...)); err != nil {
		t.Fatal(err)
	}
	m := s.Mapping()
	bad := buildIndex(t, sampleEntries...)
	bad[0] = 'X'
	if err := s.Load(bad); err != ErrBadMagic {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
	if s.Mapping() != m {
		t.Fatal("failed load swapped the mapping")
	}
	v := mustView(t, s)
	defer v.Close()
	if _, err := v.Query([]byte("apple")); err != nil {
		t.Fatalf("query after failed load: %v", err)
	}
}

func TestConcurrentCursorsShareLease(t *testing.T) {
	const entries = 64
	var b Builder
	for i := 0; i < entries; i++ {
		k := fmt.Sprintf("key%04d", i)
		if err := b.Add([]byte(k), []byte("val-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	if err := s.Load(data); err != nil {
		t.Fatal(err)
	}
	m := s.Mapping()

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.View()
			if err != nil {
				t.Error(err)
				return
			}
			c, err := v.Cursor()
			if err != nil {
				t.Error(err)
				v.Close()
				return
			}
			n := 0
			prev := []byte(nil)
			for {
				k, _, ok, err := c.Next()
				if err != nil {
					t.Error(err)
					break
				}
				if !ok {
					break
				}
				if prev != nil && bytes.Compare(prev, k) >= 0 {
					t.Errorf("cursor out of order: %q then %q", prev, k)
				}
				prev = append(prev[:0], k...)
				n++
			}
			if n != entries {
				t.Errorf("cursor saw %d entries, want %d", n, entries)
			}
			v.Close()
			c.Close()
		}()
	}
	wg.Wait()
	m.Close()
	if !m.TornDown() {
		t.Fatal("mapping not torn down after all cursors released")
	}
}

// TestInterleavedCloseQueryFailedLoad drives a deterministic interleaving:
// a query is inside the mapping when a close is requested and a corrupt
// load fails. The query must still complete correctly, the close must
// defer, and the failed load must not disturb anything.
func TestInterleavedCloseQueryFailedLoad(t *testing.T) {
	s := NewStore()
	if err := s.Load(buildIndex(t, sampleEntries...)); err != nil {
		t.Fatal(err)
	}
	m := s.Mapping()
	v := mustView(t, s)

	started := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	m.queryHook = func() {
		once.Do(func() {
			close(started)
			<-proceed
		})
	}

	type result struct {
		val []byte
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		val, err := v.Query([]byte("cherry"))
		resCh <- result{val, err}
	}()

	<-started // the query is now inside the mapping

	m.Close() // request close mid-query
	if m.TornDown() {
		t.Fatal("teardown completed while a query was in flight")
	}

	// A failing load interleaved here must not disturb the mapping.
	corrupt := buildIndex(t, sampleEntries...)
	putU32(corrupt, HeaderSize+2*offsetEntrySize, 0) // offset cycle
	if err := s.Load(corrupt); err != ErrOffsetCycle {
		t.Fatalf("got %v, want ErrOffsetCycle", err)
	}
	if s.Mapping() != m {
		t.Fatal("failed load swapped the mapping")
	}

	close(proceed)
	r := <-resCh
	if r.err != nil || string(r.val) != "red" {
		t.Fatalf("in-flight query = %q, %v; want red", r.val, r.err)
	}

	if _, err := OpenView(m); err != ErrMappingClosing {
		t.Fatalf("borrow after close request: got %v, want ErrMappingClosing", err)
	}
	if m.TornDown() {
		t.Fatal("teardown before last borrow released")
	}
	v.Close()
	select {
	case <-m.Done():
	case <-time.After(time.Second):
		t.Fatal("mapping not torn down after interleaved close")
	}
}
