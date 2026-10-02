package rib

import (
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAtomicPublish: readers must never observe a mixed tree. The writer
// alternates between "both routes present" and "both absent" in single
// batches; every reader view must see them together.
func TestAtomicPublish(t *testing.T) {
	tab := NewTable()
	p1 := netip.MustParsePrefix("10.0.0.0/8")
	p2 := netip.MustParsePrefix("192.0.2.0/24")
	r1 := Route{Priority: 1, Tag: "r1"}
	r2 := Route{Priority: 1, Tag: "r2"}
	a1 := netip.MustParseAddr("10.1.2.3")
	a2 := netip.MustParseAddr("192.0.2.99")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := tab.Acquire()
				_, _, ok1 := v.Lookup(a1)
				_, _, ok2 := v.Lookup(a2)
				if ok1 != ok2 {
					bad.Add(1)
				}
				v.Release()
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		if _, err := tab.Apply(Add(p1, r1), Add(p2, r2)); err != nil {
			t.Fatal(err)
		}
		if _, err := tab.Apply(Del(p1, r1), Del(p2, r2)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("readers observed mixed tree %d times", bad.Load())
	}
}

// TestBatchRejectedAtomically: a bad op rejects the whole batch; the table
// keeps its previous version and contents.
func TestBatchRejectedAtomically(t *testing.T) {
	tab := NewTable()
	good := netip.MustParsePrefix("10.0.0.0/8")
	if _, err := tab.Apply(Add(good, Route{Priority: 1, Tag: "keep"})); err != nil {
		t.Fatal(err)
	}
	before := tab.Version()
	dirty := netip.MustParsePrefix("172.16.0.0/12")
	// 10.9.0.7/16 has host bits set -> whole batch must be refused
	_, err := tab.Apply(
		Add(dirty, Route{Priority: 1, Tag: "dirty"}),
		Add(netip.PrefixFrom(netip.MustParseAddr("10.9.0.7"), 16), Route{Priority: 1}),
	)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if tab.Version() != before {
		t.Fatal("version advanced on rejected batch")
	}
	v := tab.Acquire()
	defer v.Release()
	if _, _, ok := v.Lookup(netip.MustParseAddr("172.16.1.1")); ok {
		t.Fatal("partial batch leaked into table")
	}
	if _, r, ok := v.Lookup(netip.MustParseAddr("10.1.1.1")); !ok || r.Tag != "keep" {
		t.Fatal("previous contents lost")
	}
}

// TestSnapshotReclaim verifies old snapshots stay alive while readers hold
// views and are reclaimed by the GC once all views are released.
func TestSnapshotReclaim(t *testing.T) {
	tab := NewTable()
	p := netip.MustParsePrefix("10.0.0.0/8")
	if _, err := tab.Apply(Add(p, Route{Priority: 1, Tag: "v1"})); err != nil {
		t.Fatal(err)
	}
	var reclaimed atomic.Int64
	watch := func(s *snapshot) {
		runtime.SetFinalizer(s, func(*snapshot) { reclaimed.Add(1) })
	}
	v1 := tab.Acquire() // pins v1
	watch(tab.cur.Load())

	// publish v2; v1 is retired but still pinned by v1 view
	if _, err := tab.Apply(Add(netip.MustParsePrefix("192.0.2.0/24"), Route{Priority: 1, Tag: "v2"})); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	if reclaimed.Load() != 0 {
		t.Fatal("snapshot reclaimed while a reader holds a view")
	}

	// the pinned view still reads the old version consistently
	if _, r, ok := v1.Lookup(netip.MustParseAddr("10.0.0.1")); !ok || r.Tag != "v1" {
		t.Fatal("pinned view lost its routes")
	}
	if _, _, ok := v1.Lookup(netip.MustParseAddr("192.0.2.1")); ok {
		t.Fatal("pinned view sees newer version")
	}
	v1.Release()
	v1 = nil

	deadline := time.Now().Add(2 * time.Second)
	for reclaimed.Load() == 0 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if reclaimed.Load() != 1 {
		t.Fatalf("retired snapshot not reclaimed (reclaimed=%d)", reclaimed.Load())
	}
	if n := tab.cur.Load().refs.Load(); n != 0 {
		t.Fatalf("current snapshot refs = %d, want 0", n)
	}
}

// TestVersionMonotonic checks version numbers increase per published batch.
func TestVersionMonotonic(t *testing.T) {
	tab := NewTable()
	p := netip.MustParsePrefix("10.0.0.0/8")
	r := Route{Priority: 1}
	for want := uint64(1); want <= 5; want++ {
		got, err := tab.Apply(Add(p, r))
		if err != nil {
			t.Fatal(err)
		}
		if got != want || tab.Version() != want {
			t.Fatalf("version = %d/%d, want %d", got, tab.Version(), want)
		}
	}
}
