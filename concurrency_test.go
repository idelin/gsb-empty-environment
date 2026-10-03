package roaring

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

// Run with -race: concurrent writers and readers must be safe.
func TestConcurrentReadWrite(t *testing.T) {
	bm := New()
	other := New()
	other.AddRange(0, 0, 65535)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(id)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch rnd.Intn(3) {
				case 0:
					bm.Add(uint32(rnd.Intn(1 << 24)))
				case 1:
					lo := uint16(rnd.Intn(32768))
					bm.AddRange(uint16(rnd.Intn(256)), lo, lo+uint16(rnd.Intn(32768)))
				case 2:
					_ = bm.Contains(uint32(rnd.Intn(1 << 24)))
				}
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = bm.Cardinality()
				_ = bm.And(other)
				_ = bm.Or(other)
				_ = bm.Xor(other)
				_ = bm.Marshal()
				_ = bm.Clone()
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
	checkContainerInvariants(t, bm)
}

func TestConcurrentStoreAccess(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := s.Add(uint16(id), uint16(i), uint16(i*2)); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				if i%50 == 0 {
					if err := s.Checkpoint(); err != nil {
						t.Errorf("checkpoint: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	want := s.Bitmap()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("recovered state diverged from in-memory state")
	}
	for id := 0; id < 8; id++ {
		for i := 0; i < 200; i++ {
			if !s2.Bitmap().Contains(uint32(id)<<16 | uint32(i)) {
				t.Fatalf("missing id key=%d val=%d", id, i)
			}
		}
	}
}
