package cachemodel

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

const (
	testPageSize = 128
	testPages    = 4
)

func randBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

func dirtyRange(t *testing.T, c *Cache, page int) (lo, hi int, count int) {
	t.Helper()
	_, dirty, resident := c.snapshotPage(page)
	if !resident {
		t.Fatalf("page %d not resident", page)
	}
	lo, hi = -1, -1
	for i, d := range dirty {
		if d {
			count++
			if lo < 0 {
				lo = i
			}
			hi = i + 1
		}
	}
	return lo, hi, count
}

// A writeback that completes must not clear bytes re-dirtied while it was
// in flight; only the snapshotted (old-version) bytes it actually wrote are
// cleared.
func TestWritebackDoesNotClearRedirtiedBytes(t *testing.T) {
	ms := NewMemStore(testPageSize)
	release := make(chan struct{})
	gate := NewGateStore(ms, release)
	c := New(gate, testPageSize)

	a := bytes.Repeat([]byte{'A'}, 64)
	if _, err := c.Write(0, 0, a); err != nil {
		t.Fatal(err)
	}

	wbDone := make(chan error, 1)
	go func() { wbDone <- c.writebackOnce(0) }()
	<-gate.Started // writeback snapshot taken, store write blocked

	// Re-dirty [32,96) while the writeback of [0,64)@v1 is in flight.
	b := bytes.Repeat([]byte{'B'}, 64)
	if _, err := c.Write(0, 32, b); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-wbDone; err != nil {
		t.Fatal(err)
	}

	lo, hi, count := dirtyRange(t, c, 0)
	// [0,32) was written back at v1 and never re-dirtied: cleared.
	// [32,96) was re-dirtied at v2 while the writeback was in flight:
	// the store still holds the stale v1 bytes for [32,64) and nothing
	// for [64,96), so the whole range must stay dirty.
	if lo != 32 || hi != 96 || count != 64 {
		t.Fatalf("dirty = [%d,%d) count %d, want [32,96) count 64", lo, hi, count)
	}
	got := ms.Bytes()
	if !bytes.Equal(got[:64], a) || !bytes.Equal(got[64:], make([]byte, 64)) {
		t.Fatalf("store after first writeback = %q..., want A*64 then zeros", got[:8])
	}

	// Second pass persists the re-dirtied tail.
	if err := c.writebackOnce(0); err != nil {
		t.Fatal(err)
	}
	if _, _, count := dirtyRange(t, c, 0); count != 0 {
		t.Fatalf("page still dirty after second writeback")
	}
	want := append(append([]byte{}, a[:32]...), b...)
	if !bytes.Equal(ms.Bytes()[:96], want) {
		t.Fatalf("store = %q..., want A*32 B*64", ms.Bytes()[:8])
	}
}

// A short write acks only a prefix of the dirty range; exactly that prefix
// (old version) is cleared, the rest stays dirty. A failing store acks only
// the bytes it persisted.
func TestShortWriteClearsOnlyAckedBytes(t *testing.T) {
	payload := make([]byte, 64)
	for i := range payload {
		payload[i] = byte(i + 1)
	}

	t.Run("short store", func(t *testing.T) {
		ms := NewMemStore(testPageSize)
		c := New(&ShortStore{Inner: ms, MaxChunk: 10}, testPageSize)
		if _, err := c.Write(0, 0, payload); err != nil {
			t.Fatal(err)
		}
		if err := c.writebackOnce(0); err != nil {
			t.Fatal(err)
		}
		lo, hi, count := dirtyRange(t, c, 0)
		if lo != 10 || hi != 64 || count != 54 {
			t.Fatalf("dirty = [%d,%d) count %d, want [10,64) count 54", lo, hi, count)
		}
		if !bytes.Equal(ms.Bytes()[:10], payload[:10]) {
			t.Fatalf("store prefix mismatch")
		}
		if err := c.Flush(0); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ms.Bytes()[:64], payload) {
			t.Fatalf("store = %v, want %v", ms.Bytes()[:64], payload)
		}
	})

	t.Run("failing store", func(t *testing.T) {
		errBoom := errors.New("device error")
		ms := NewMemStore(testPageSize)
		fs := &FailStore{Inner: ms, Budget: 25, Err: errBoom}
		c := New(fs, testPageSize)
		if _, err := c.Write(0, 0, payload); err != nil {
			t.Fatal(err)
		}
		if err := c.writebackOnce(0); err != errBoom {
			t.Fatalf("writeback err = %v, want %v", err, errBoom)
		}
		lo, hi, count := dirtyRange(t, c, 0)
		if lo != 25 || hi != 64 || count != 39 {
			t.Fatalf("dirty = [%d,%d) count %d, want [25,64) count 39", lo, hi, count)
		}
		if !bytes.Equal(ms.Bytes()[:25], payload[:25]) {
			t.Fatalf("store prefix mismatch")
		}
	})
}

// Evict must wait for an in-flight writeback, notice bytes re-dirtied
// during that writeback, write them back too, and only then remove the page.
func TestEvictWaitsForInflightWriteback(t *testing.T) {
	ms := NewMemStore(testPageSize)
	release := make(chan struct{})
	gate := NewGateStore(ms, release)
	c := New(gate, testPageSize)

	a := bytes.Repeat([]byte{'A'}, 64)
	if _, err := c.Write(0, 0, a); err != nil {
		t.Fatal(err)
	}

	evictDone := make(chan error, 1)
	go func() { evictDone <- c.Evict(0) }()
	<-gate.Started // evict's writeback is in flight, blocked in the store

	select {
	case <-evictDone:
		t.Fatal("Evict returned while its writeback was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	// Re-dirty [16,48) underneath the in-flight writeback.
	b := bytes.Repeat([]byte{'B'}, 32)
	if _, err := c.Write(0, 16, b); err != nil {
		t.Fatal(err)
	}
	close(release)

	select {
	case err := <-evictDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Evict did not finish after writeback release")
	}

	if _, _, resident := c.snapshotPage(0); resident {
		t.Fatal("page still resident after Evict")
	}
	want := make([]byte, testPageSize)
	copy(want[0:64], a)
	copy(want[16:48], b)
	if !bytes.Equal(ms.Bytes(), want) {
		t.Fatalf("store mismatch after eviction: got %q...", ms.Bytes()[:8])
	}

	// The next access reloads the evicted page from the store.
	buf := make([]byte, testPageSize)
	if err := c.Read(0, 0, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, want) {
		t.Fatalf("read after eviction mismatch")
	}
}

// Concurrent writers and a background flusher, checked against the per-byte
// version oracle: store versions must be monotone, every stored byte must
// come from a real write, and the final state must converge to the latest
// value of every byte.
func TestConcurrentWritesAndFlush(t *testing.T) {
	ms := NewMemStore(testPageSize * testPages)
	model := newByteModel(t, testPageSize, testPages)
	c := New(&modelStore{inner: &ShortStore{Inner: ms, MaxChunk: 17}, m: model}, testPageSize)
	c.WriteHook = model.hook

	var wg sync.WaitGroup
	stop := make(chan struct{})
	flusherDone := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 100; i++ {
				pg := r.Intn(testPages)
				off := r.Intn(testPageSize)
				n := 1 + r.Intn(testPageSize-off)
				if _, err := c.Write(pg, off, randBytes(r, n)); err != nil {
					t.Error(err)
					return
				}
			}
		}(int64(w) + 1)
	}
	go func() {
		defer close(flusherDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for pg := 0; pg < testPages; pg++ {
				if err := c.Flush(pg); err != nil {
					t.Error(err)
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	close(stop)
	<-flusherDone
	for pg := 0; pg < testPages; pg++ {
		if err := c.Flush(pg); err != nil {
			t.Fatal(err)
		}
	}
	model.checkConverged(c, testPages, ms.Bytes())
}

// Randomized sequential model check: every interleaving of writes, flushes
// and evictions must leave the dirty set exactly equal to the bytes whose
// cache version exceeds their store version.
func TestRandomizedModelCheck(t *testing.T) {
	ms := NewMemStore(testPageSize * testPages)
	model := newByteModel(t, testPageSize, testPages)
	c := New(&modelStore{inner: &ShortStore{Inner: ms, MaxChunk: 9}, m: model}, testPageSize)
	c.WriteHook = model.hook

	r := rand.New(rand.NewSource(42))
	for i := 0; i < 300; i++ {
		switch r.Intn(5) {
		case 0, 1, 2:
			pg := r.Intn(testPages)
			off := r.Intn(testPageSize)
			n := 1 + r.Intn(testPageSize-off)
			if _, err := c.Write(pg, off, randBytes(r, n)); err != nil {
				t.Fatal(err)
			}
		case 3:
			if err := c.Flush(r.Intn(testPages)); err != nil {
				t.Fatal(err)
			}
		case 4:
			if err := c.Evict(r.Intn(testPages)); err != nil {
				t.Fatal(err)
			}
		}
		model.checkQuiescent(c, testPages)
		if t.Failed() {
			t.Fatalf("oracle mismatch at op %d", i)
		}
	}
	for pg := 0; pg < testPages; pg++ {
		if err := c.Flush(pg); err != nil {
			t.Fatal(err)
		}
	}
	model.checkConverged(c, testPages, ms.Bytes())
}
