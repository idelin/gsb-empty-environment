package pool

import (
	"errors"
	"sync"
	"testing"
)

func TestSizeClasses(t *testing.T) {
	p := NewBufferPool()
	cases := []struct {
		req  int
		want int
	}{
		{0, 64}, {1, 64}, {64, 64}, {65, 256}, {256, 256},
		{257, 1024}, {4096, 4096}, {4097, 16384}, {65536, 65536},
	}
	for _, c := range cases {
		b := p.Get(c.req)
		if b.Cap() != c.want {
			t.Fatalf("Get(%d): cap %d, want class %d", c.req, b.Cap(), c.want)
		}
		if len(b.Bytes()) != 0 {
			t.Fatalf("Get(%d): dirty buffer, len %d", c.req, len(b.Bytes()))
		}
		if err := p.Release(b); err != nil {
			t.Fatal(err)
		}
	}
	if s := p.Stats(); s.InUse() != 0 {
		t.Fatalf("leak: %+v", s)
	}
}

func TestOversizedAdHoc(t *testing.T) {
	p := NewBufferPool()
	b := p.Get(1 << 20) // beyond largest class
	if b.Cap() < 1<<20 {
		t.Fatalf("oversized cap: %d", b.Cap())
	}
	if err := p.Release(b); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.AdHoc != 1 {
		t.Fatalf("adhoc count: %d, want 1", s.AdHoc)
	}
	if s.InUse() != 0 {
		t.Fatalf("leak: %+v", s)
	}
}

func TestReuse(t *testing.T) {
	p := NewBufferPool()
	b := p.Get(100)
	b.data = append(b.data, make([]byte, 100)...)
	if err := p.Release(b); err != nil {
		t.Fatal(err)
	}
	b2 := p.Get(100)
	if b2.Cap() != 256 {
		t.Fatalf("reused buffer cap: %d, want 256", b2.Cap())
	}
	if len(b2.Bytes()) != 0 {
		t.Fatal("reused buffer not reset")
	}
	_ = p.Release(b2)
}

func TestDoubleReleaseDetected(t *testing.T) {
	p := NewBufferPool()
	b := p.Get(10)
	if err := p.Release(b); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(b); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("double release: got %v, want ErrDoubleRelease", err)
	}
	// Foreign buffer (from another pool) must be rejected.
	other := NewBufferPool()
	foreign := other.Get(10)
	if err := p.Release(foreign); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("foreign release: got %v, want ErrDoubleRelease", err)
	}
	_ = other.Release(foreign)
	if err := p.Release(nil); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("nil release: got %v, want ErrDoubleRelease", err)
	}
}

func TestResize(t *testing.T) {
	p := NewBufferPool()
	b := p.Get(8)
	b.data = append(b.data, []byte("abcdefgh")...)
	b = b.Resize(1000) // forces class upgrade, old lease released
	if b.Cap() < 1000 {
		t.Fatalf("resized cap: %d", b.Cap())
	}
	if string(b.Bytes()[:8]) != "abcdefgh" {
		t.Fatalf("resize lost contents: %q", b.Bytes()[:8])
	}
	if err := p.Release(b); err != nil {
		t.Fatal(err)
	}
	if s := p.Stats(); s.InUse() != 0 {
		t.Fatalf("leak after resize: %+v", s)
	}
}

func TestConcurrentNoLeak(t *testing.T) {
	p := NewBufferPool()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				size := (g*37 + i) % 70000 // mix of pooled and ad hoc
				b := p.Get(size)
				b.data = b.data[:cap(b.data)]
				b.data[0] = byte(g)
				if err := p.Release(b); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	s := p.Stats()
	if s.InUse() != 0 {
		t.Fatalf("leak under concurrency: %+v", s)
	}
	if s.Acquired != 32*2000 {
		t.Fatalf("acquired: %d, want %d", s.Acquired, 32*2000)
	}
}

func TestZeroAllocGet(t *testing.T) {
	p := NewBufferPool()
	// Warm every class.
	for _, s := range classSizes {
		_ = p.Release(p.Get(s))
	}
	allocs := testing.AllocsPerRun(1000, func() {
		b := p.Get(512)
		_ = p.Release(b)
	})
	if allocs != 0 {
		t.Fatalf("warm Get/Release allocated %v times, want 0", allocs)
	}
}
