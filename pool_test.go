package wirebuf

import (
	"sync"
	"testing"
)

func TestBufferPoolSizeClasses(t *testing.T) {
	cases := []struct {
		size    int
		wantCap int
	}{
		{0, 16}, {1, 16}, {16, 16}, {17, 32}, {100, 128},
		{1024, 1024}, {1025, 2048}, {1 << 20, 1 << 20},
	}
	p := &BufferPool{}
	for _, c := range cases {
		b := p.Get(c.size)
		if len(b) != c.size {
			t.Fatalf("Get(%d): len %d", c.size, len(b))
		}
		if cap(b) != c.wantCap {
			t.Fatalf("Get(%d): cap %d, want %d", c.size, cap(b), c.wantCap)
		}
		p.Put(b)
	}
}

func TestBufferPoolReuse(t *testing.T) {
	p := &BufferPool{}
	b1 := p.Get(100)
	ptr1 := &b1[:1][0]
	p.Put(b1)
	b2 := p.Get(100)
	if !raceEnabled && &b2[:1][0] != ptr1 {
		t.Fatal("expected recycled backing array")
	}
	p.Put(b2)
	if !Balanced() {
		t.Fatal("pool imbalance after buffer reuse")
	}
}

func TestBufferPoolOversizedNotPooled(t *testing.T) {
	p := &BufferPool{}
	before := Stats.BufferRelease
	big := p.Get(1<<20 + 1) // exceeds largest class
	if len(big) != 1<<20+1 {
		t.Fatalf("len %d", len(big))
	}
	p.Put(big) // must be dropped, not pooled
	if Stats.BufferRelease != before {
		t.Fatal("oversized buffer was pooled")
	}
}

func TestBufferPoolRejectsForeignCapacity(t *testing.T) {
	p := &BufferPool{}
	before := Stats.BufferRelease
	p.Put(make([]byte, 100))    // cap 100 matches no size class
	p.Put(make([]byte, 0, 200)) // cap 200 matches no size class
	p.Put(nil)                  // must be a no-op
	if Stats.BufferRelease != before {
		t.Fatal("foreign buffer was pooled")
	}
}

func TestU64SlicePool(t *testing.T) {
	u1 := AcquireU64s()
	u1.V = append(u1.V, 1, 2, 3)
	ReleaseU64s(u1)
	u2 := AcquireU64s()
	if len(u2.V) != 0 {
		t.Fatalf("pooled vector not reset: len %d", len(u2.V))
	}
	ReleaseU64s(u2)
	ReleaseU64s(nil) // no-op, must not panic
	if !Balanced() {
		t.Fatal("pool imbalance after u64 slice cycle")
	}
}

func TestCachePutTake(t *testing.T) {
	c := NewCache(4)
	defer c.Close()

	m := AcquireMessage()
	if err := Decode(buildTestPayload(), m); err != nil {
		t.Fatal(err)
	}
	key := FNV1a64(buildTestPayload())
	c.Put(key, m)
	if c.Len() != 1 {
		t.Fatalf("Len: got %d, want 1", c.Len())
	}

	var sawID uint64
	if !c.Peek(key, func(got *Message) { sawID = got.Field(1).Varint }) {
		t.Fatal("Peek miss after Put")
	}
	if sawID != 9001 {
		t.Fatalf("Peek saw id %d, want 9001", sawID)
	}
	if c.Len() != 1 {
		t.Fatal("Peek must not remove the entry")
	}

	got, ok := c.Take(key)
	if !ok {
		t.Fatal("Take miss after Put")
	}
	if v := got.Field(1).Varint; v != 9001 {
		t.Fatalf("Take got id %d, want 9001", v)
	}
	got.Release()
	if _, ok := c.Take(key); ok {
		t.Fatal("second Take should miss")
	}
	if _, ok := c.Take(0xDEAD); ok {
		t.Fatal("Take on unknown key should miss")
	}
	if c.Peek(0xDEAD, func(*Message) { t.Fatal("fn called on miss") }) {
		t.Fatal("Peek on unknown key should miss")
	}
	if !Balanced() {
		t.Fatal("pool imbalance after cache cycle")
	}
}

func TestCacheEvictionReleases(t *testing.T) {
	c := NewCache(0) // single slot: every key collides
	before := Stats.MessageRelease

	m1 := AcquireMessage()
	m2 := AcquireMessage()
	c.Put(1, m1)
	c.Put(2, m2) // evicts m1, releasing it to the pool
	if Stats.MessageRelease != before+1 {
		t.Fatal("evicted message was not released")
	}
	if _, ok := c.Take(1); ok {
		t.Fatal("evicted key should miss")
	}
	got, ok := c.Take(2)
	if !ok {
		t.Fatal("resident key should hit")
	}
	got.Release()
	if c.Len() != 0 {
		t.Fatalf("Len: got %d, want 0", c.Len())
	}
}

func TestCacheCloseReleasesAll(t *testing.T) {
	c := NewCache(4)
	before := Stats.MessageRelease
	for i := 0; i < 10; i++ {
		m := AcquireMessage()
		if err := Decode(buildTestPayload(), m); err != nil {
			t.Fatal(err)
		}
		c.Put(uint64(i)*97, m)
	}
	c.Close()
	if Stats.MessageRelease != before+10 {
		t.Fatalf("Close released %d, want 10", Stats.MessageRelease-before)
	}
	if c.Len() != 0 {
		t.Fatal("Close must empty the cache")
	}
	if !Balanced() {
		t.Fatal("pool imbalance after cache close")
	}
}

func TestFNV1a64(t *testing.T) {
	// Known FNV-1a 64 vectors.
	if got := FNV1a64(nil); got != 14695981039346656037 {
		t.Fatalf("empty hash: got %d", got)
	}
	if got := FNV1a64([]byte("a")); got != 12638187200555641996 {
		t.Fatalf("hash(a): got %d", got)
	}
	if FNV1a64([]byte("x")) == FNV1a64([]byte("y")) {
		t.Fatal("distinct inputs collided")
	}
}

func TestConcurrentPoolsAndCache(t *testing.T) {
	payload := buildTestPayload()
	cache := NewCache(8)
	pool := &BufferPool{}

	const workers = 16
	const iters = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := uint64(id) * 0x9E3779B1
			for i := 0; i < iters; i++ {
				// Parse with pooled messages.
				m := AcquireMessage()
				if err := Decode(payload, m); err != nil {
					t.Errorf("decode: %v", err)
					return
				}
				if _, err := m.Field(5).DecodeNested(); err != nil {
					t.Errorf("nested: %v", err)
					return
				}
				if _, err := m.Field(6).DecodePacked(); err != nil {
					t.Errorf("packed: %v", err)
					return
				}
				m.Release()

				// Buffer pool churn across size classes.
				b := pool.Get(64 + (i%8)*64)
				b[0] = byte(i)
				pool.Put(b)

				// Cache put/take on a per-worker key.
				cm := AcquireMessage()
				if err := Decode(payload, cm); err != nil {
					t.Errorf("cache decode: %v", err)
					return
				}
				cache.Put(key, cm)
				if got, ok := cache.Take(key); ok {
					got.Release()
				}
			}
		}(w)
	}
	wg.Wait()
	cache.Close()
	if !Balanced() {
		t.Fatal("pool imbalance after concurrent run: leak detected")
	}
}

func TestZeroAllocSteadyState(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are inflated by the race detector")
	}
	payload := buildTestPayload()
	// Warm up pools and slice capacities.
	for i := 0; i < 5; i++ {
		m := AcquireMessage()
		if err := Decode(payload, m); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Field(5).DecodeNested(); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Field(6).DecodePacked(); err != nil {
			t.Fatal(err)
		}
		m.Release()
	}
	allocs := testing.AllocsPerRun(200, func() {
		m := AcquireMessage()
		if err := Decode(payload, m); err != nil {
			t.Fatal(err)
		}
		inner, err := m.Field(5).DecodeNested()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inner.Field(2).DecodeNested(); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Field(6).DecodePacked(); err != nil {
			t.Fatal(err)
		}
		m.Release()
	})
	if allocs != 0 {
		t.Fatalf("steady-state decode allocated %v times per run, want 0", allocs)
	}
	if !Balanced() {
		t.Fatal("pool imbalance after zero-alloc run")
	}
}

func BenchmarkDecodePooled(b *testing.B) {
	payload := buildTestPayload()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := AcquireMessage()
		if err := Decode(payload, m); err != nil {
			b.Fatal(err)
		}
		m.Release()
	}
}

func BenchmarkDecodeUnpooled(b *testing.B) {
	payload := buildTestPayload()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := &Message{}
		r := NewReader(payload)
		if err := ParseMessage(r, m); err != nil {
			b.Fatal(err)
		}
	}
}
