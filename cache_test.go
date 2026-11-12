package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock 是测试用的手动时钟。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func rec(name string, typ, class uint16, ttl uint32, data string) Record {
	return Record{Name: name, Type: typ, Class: class, TTL: ttl, Data: data}
}

func aRecords(ttl uint32, datas ...string) []Record {
	out := make([]Record, len(datas))
	for i, d := range datas {
		out[i] = rec("example.org.", 1, 1, ttl, d)
	}
	return out
}

func TestPutGetRoundTrip(t *testing.T) {
	c := NewCache(newFakeClock())
	key, err := c.Put(aRecords(300, "192.0.2.1", "192.0.2.2"), TrustSecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, trust, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: miss")
	}
	if trust != TrustSecure {
		t.Fatalf("trust = %v, want %v", trust, TrustSecure)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestDuplicateRecordsDeduped(t *testing.T) {
	c := NewCache(newFakeClock())
	in := append(aRecords(300, "192.0.2.1"), aRecords(300, "192.0.2.1", "192.0.2.2")...)
	key, err := c.Put(in, TrustInsecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: miss")
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 after dedupe", len(got))
	}
}

func TestWholeSetExpiresAtMinTTL(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	in := []Record{
		rec("example.org.", 1, 1, 60, "192.0.2.1"),
		rec("example.org.", 1, 1, 3600, "192.0.2.2"),
	}
	key, err := c.Put(in, TrustSecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	clock.Advance(59 * time.Second)
	if _, _, ok := c.Get(key); !ok {
		t.Fatal("expired before min TTL")
	}
	// 到达最小 TTL 后整组到期，不得拆出长 TTL 的子集。
	clock.Advance(1 * time.Second)
	if _, _, ok := c.Get(key); ok {
		t.Fatal("set must expire as a whole at min TTL")
	}
}

func TestZeroTTLExpiresImmediately(t *testing.T) {
	c := NewCache(newFakeClock())
	key, err := c.Put(aRecords(0, "192.0.2.1"), TrustSecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, _, ok := c.Get(key); ok {
		t.Fatal("zero-TTL set must not be served from cache")
	}
}

func TestPutRejectsMixedSet(t *testing.T) {
	c := NewCache(newFakeClock())
	in := []Record{
		rec("example.org.", 1, 1, 60, "192.0.2.1"),
		rec("example.org.", 2, 1, 60, "ns1.example.org."),
	}
	if _, err := c.Put(in, TrustNone); !errors.Is(err, ErrMixedSet) {
		t.Fatalf("err = %v, want %v", err, ErrMixedSet)
	}
	if _, err := c.Put(nil, TrustNone); !errors.Is(err, ErrEmptySet) {
		t.Fatalf("err = %v, want %v", err, ErrEmptySet)
	}
}

func TestAtomicReplacePreservesTrustOnDowngrade(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	key, err := c.Put(aRecords(300, "192.0.2.1"), TrustSecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// 低信任结果不得覆盖仍有效的高信任组。
	if _, err := c.Put(aRecords(300, "192.0.2.99"), TrustInsecure); !errors.Is(err, ErrTrustDowngrade) {
		t.Fatalf("err = %v, want %v", err, ErrTrustDowngrade)
	}
	got, trust, ok := c.Get(key)
	if !ok || trust != TrustSecure || len(got) != 1 || got[0].Data != "192.0.2.1" {
		t.Fatalf("group not preserved: ok=%v trust=%v got=%v", ok, trust, got)
	}
	// 旧组到期后，低信任结果可以写入。
	clock.Advance(300 * time.Second)
	if _, err := c.Put(aRecords(300, "192.0.2.99"), TrustInsecure); err != nil {
		t.Fatalf("Put after expiry: %v", err)
	}
	if _, trust, ok := c.Get(key); !ok || trust != TrustInsecure {
		t.Fatalf("after expiry: ok=%v trust=%v", ok, trust)
	}
}

func TestAtomicReplaceWholeSet(t *testing.T) {
	c := NewCache(newFakeClock())
	key, err := c.Put(aRecords(300, "192.0.2.1", "192.0.2.2"), TrustInsecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// 同级更新必须整组替换：旧记录不得残留。
	if _, err := c.Put(aRecords(300, "192.0.2.3"), TrustInsecure); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: miss")
	}
	if len(got) != 1 || got[0].Data != "192.0.2.3" {
		t.Fatalf("got %v, want single replaced record", got)
	}
}

func TestHigherTrustMayOverwriteLower(t *testing.T) {
	c := NewCache(newFakeClock())
	key, err := c.Put(aRecords(300, "192.0.2.1"), TrustInsecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := c.Put(aRecords(300, "192.0.2.2"), TrustSecure); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, trust, ok := c.Get(key)
	if !ok || trust != TrustSecure || got[0].Data != "192.0.2.2" {
		t.Fatalf("ok=%v trust=%v got=%v", ok, trust, got)
	}
}

func TestConcurrentQueryAndReplace(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	key, err := c.Put(aRecords(120, "192.0.2.1"), TrustSecure)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if j%3 == 0 {
					clock.Advance(time.Second)
				}
				got, _, _ := c.Get(key)
				for _, r := range got {
					if r.Name != "example.org." {
						t.Errorf("bad record: %+v", r)
					}
				}
				if _, err := c.Put(aRecords(120, "192.0.2.1"), TrustSecure); err != nil {
					t.Errorf("Put: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
}
