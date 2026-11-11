package noncelease

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func assertDisjoint(t *testing.T, key KeyID, ivs []Interval) {
	t.Helper()
	seen := make(map[uint64]bool)
	for _, iv := range ivs {
		if iv.Key != key {
			t.Fatalf("interval key = %q, want %q", iv.Key, key)
		}
		for c := iv.Start; c < iv.End; c++ {
			if seen[c] {
				t.Fatalf("counter %d allocated twice", c)
			}
			seen[c] = true
			if c == ^uint64(0) {
				break
			}
		}
	}
}

// TestLastSegmentContention runs two writers racing for the final segment of
// a small counter space. Exactly one wins; the loser sees
// ErrCounterExhausted and no counter is double-allocated (run with -race).
func TestLastSegmentContention(t *testing.T) {
	key := KeyID("contended")
	const space uint64 = 1000
	const writers = 2

	a, _ := newTestAllocator(t, nil)
	if _, err := a.CreateKey(key, space); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	type result struct {
		lease Lease
		err   error
	}
	results := make([]result, writers)
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-ready
			results[idx].lease, results[idx].err = a.Allocate(key, space, time.Minute)
		}(w)
	}
	close(ready)
	wg.Wait()

	winners, exhausted := 0, 0
	var intervals []Interval
	for _, r := range results {
		switch {
		case r.err == nil:
			winners++
			intervals = append(intervals, r.lease.Interval)
		case errors.Is(r.err, ErrCounterExhausted):
			exhausted++
		default:
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if winners != 1 || exhausted != 1 {
		t.Fatalf("winners = %d, exhausted = %d, want 1 and 1", winners, exhausted)
	}
	assertDisjoint(t, key, intervals)
}

// TestExhaustionAndRotation walks a counter space to exhaustion, then rotates
// the key and verifies the new version restarts at zero while the old version
// is sealed. Rotation and reservations must both survive a crash.
func TestExhaustionAndRotation(t *testing.T) {
	key := KeyID("rot")
	a, disk := newTestAllocator(t, nil)
	const limit uint64 = 100
	if _, err := a.CreateKey(key, limit); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	first := mustAllocate(t, a, key, limit-10) // [0, 90)
	if first.Interval.End != limit-10 {
		t.Fatalf("big segment end = %d", first.Interval.End)
	}
	second := mustAllocate(t, a, key, 10) // [90, 100)
	if second.Interval.End != limit {
		t.Fatalf("tail segment end = %d, want limit %d", second.Interval.End, limit)
	}
	if _, err := a.Allocate(key, 1, time.Minute); !errors.Is(err, ErrCounterExhausted) {
		t.Fatalf("exhausted allocate err = %v, want ErrCounterExhausted", err)
	}

	// Rotate after a restart: the new version resets the counter space.
	store2, err := reopenCrashStore(disk, a.store)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a2, err := OpenWithStore(store2)
	if err != nil {
		t.Fatalf("reopen allocator: %v", err)
	}
	v, err := a2.RotateKey(key)
	if err != nil || v != 2 {
		t.Fatalf("RotateKey = %d, %v", v, err)
	}
	fresh := mustAllocate(t, a2, key, 8)
	if fresh.Interval.Version != 2 || fresh.Interval.Start != 0 || fresh.Interval.End != 8 {
		t.Fatalf("post-rotation interval = %+v", fresh.Interval)
	}
	if err := a2.Valid(second); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("old-version lease after rotation err = %v, want ErrLeaseInvalid", err)
	}

	// The rotation itself must survive another crash.
	store3, err := reopenCrashStore(disk, store2)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a3, err := OpenWithStore(store3)
	if err != nil {
		t.Fatalf("reopen allocator: %v", err)
	}
	if got, _ := a3.Version(key); got != 2 {
		t.Fatalf("persisted version = %d, want 2", got)
	}
	again := mustAllocate(t, a3, key, 4)
	if again.Interval.Start != 8 || again.Interval.End != 12 {
		t.Fatalf("interval after crash = %+v, want [8,12)", again.Interval)
	}
	_ = a3.Close()
}

// TestNoDuplicateNonceGlobal is the end-to-end invariant: several writers
// allocate concurrently across injected crashes and restarts, and every
// (key, version) counter encoded into a nonce is seen at most once.
func TestNoDuplicateNonceGlobal(t *testing.T) {
	keys := []KeyID{"k-a", "k-b", "k-c"}
	disk := newCrashDisk()
	a, _ := newTestAllocator(t, disk)
	for _, k := range keys {
		mustCreate(t, a, k)
	}

	type nonceKey struct {
		key     KeyID
		version uint32
		counter uint64
	}
	var mu sync.Mutex
	seen := make(map[nonceKey]bool)
	record := func(iv Interval) {
		mu.Lock()
		defer mu.Unlock()
		for c := iv.Start; c < iv.End; c++ {
			nk := nonceKey{iv.Key, iv.Version, c}
			if seen[nk] {
				t.Errorf("duplicate nonce: key=%q version=%d counter=%d", iv.Key, iv.Version, c)
			}
			seen[nk] = true
			if c == ^uint64(0) {
				break
			}
		}
	}

	const writers = 8
	const rounds = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				key := keys[(id+r)%len(keys)]
				count := uint64(1 + (id*7+r*3)%32)
				lease, err := a.Allocate(key, count, 0)
				if err != nil {
					t.Errorf("allocate: %v", err)
					return
				}
				record(lease.Interval)
				// Spot-check the nonce round trip.
				if got := CounterFromNonce(EncodeCounter(lease.Interval.Start)); got != lease.Interval.Start {
					t.Errorf("nonce round trip = %d, want %d", got, lease.Interval.Start)
				}
			}
		}(w)
	}
	wg.Wait()

	// Inject a torn write, restart, and keep allocating: the watermark must
	// remain consistent and no earlier counter may reappear.
	key := keys[0]
	highBefore, _ := a.HighWater(key)
	disk.armFault(faultTorn, disk.len()+3)
	if _, err := a.Allocate(key, 5, 0); err == nil {
		t.Fatal("torn Sync should fail")
	}
	store2, err := reopenCrashStore(disk, a.store)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a2, err := OpenWithStore(store2)
	if err != nil {
		t.Fatalf("reopen allocator: %v", err)
	}
	highAfter, _ := a2.HighWater(key)
	if highAfter != highBefore {
		t.Fatalf("watermark changed across torn restart: %d -> %d", highBefore, highAfter)
	}
	for r := 0; r < rounds; r++ {
		lease, err := a2.Allocate(key, uint64(1+r%16), 0)
		if err != nil {
			t.Fatalf("post-crash allocate: %v", err)
		}
		record(lease.Interval)
	}

	// Rotate one key and confirm counters restart at zero under a new version
	// without colliding with version 1 nonces.
	if _, err := a2.RotateKey(key); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	fresh, err := a2.Allocate(key, 16, 0)
	if err != nil {
		t.Fatalf("post-rotation allocate: %v", err)
	}
	record(fresh.Interval)
	if fresh.Interval.Start != 0 || fresh.Interval.Version != 2 {
		t.Fatalf("post-rotation interval = %+v", fresh.Interval)
	}

	total := 0
	for range seen {
		total++
	}
	if total == 0 {
		t.Fatal("no nonces recorded")
	}
	_ = a2.Close()
}

// TestRealFileStore exercises the production *os.File-backed store, including
// reopening through a real path.
func TestRealFileStore(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/nonce.wal"
	key := KeyID("disk-key")

	a, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreate(t, a, key)
	l1 := mustAllocate(t, a, key, 256)
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	a2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := a2.Valid(l1); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("lease across real restart err = %v, want ErrLeaseInvalid", err)
	}
	high, _ := a2.HighWater(key)
	if high != 256 {
		t.Fatalf("replayed high water = %d, want 256", high)
	}
	l2 := mustAllocate(t, a2, key, 64)
	if l2.Interval.Start != 256 || l2.Interval.End != 320 {
		t.Fatalf("real-file interval = %+v, want [256,320)", l2.Interval)
	}

	// Corrupt the tail with garbage and ensure replay truncates it cleanly.
	if err := a2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := appendGarbage(path); err != nil {
		t.Fatalf("appendGarbage: %v", err)
	}
	a3, err := Open(path)
	if err != nil {
		t.Fatalf("open after corruption: %v", err)
	}
	high, _ = a3.HighWater(key)
	if high != 320 {
		t.Fatalf("high water after garbage = %d, want 320", high)
	}
	_ = a3.Close()
}

func appendGarbage(path string) error {
	f, err := openAppend(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("garbage-torn-bytes!!")
	if err != nil {
		return err
	}
	return f.Sync()
}

func TestIntervalHelpers(t *testing.T) {
	iv := Interval{Start: 10, End: 20}
	if iv.Len() != 10 || !iv.Contains(10) || iv.Contains(20) || iv.Contains(9) {
		t.Fatal("Interval.Contains/Len wrong")
	}
	if got := fmt.Sprint(EncodeCounter(0x0102030405060708)); len(got) == 0 {
		t.Fatal("EncodeCounter produced empty output")
	}
}
