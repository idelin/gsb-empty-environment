package rrset_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0145/rrset"
)

// fakeClock is a manually advanced clock shared with the cache under test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func aRecord(name, data string, ttl uint32) rrset.Record {
	return rrset.Record{Name: name, Type: 1, Class: 1, TTL: ttl, Data: data}
}

func TestWholeSetExpiresAtMinTTL(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "www.example.org", Type: 1, Class: 1}

	stored, err := c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.1", 300),
		aRecord(key.Name, "192.0.2.2", 60),
		aRecord(key.Name, "192.0.2.3", 120),
	}, rrset.LevelSecure)
	if err != nil || !stored {
		t.Fatalf("Store = (%v, %v), want (true, nil)", stored, err)
	}

	set, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: set missing before expiry")
	}
	for _, r := range set.Records {
		if r.TTL != 60 {
			t.Fatalf("record TTL = %d, want clamped whole-set TTL 60", r.TTL)
		}
	}
	if got := set.TTL(clock.now()); got != 60*time.Second {
		t.Fatalf("remaining TTL = %v, want 60s", got)
	}

	clock.advance(59 * time.Second)
	if _, ok := c.Get(key); !ok {
		t.Fatal("Get: set expired 1s before minimum TTL elapsed")
	}
	clock.advance(1 * time.Second)
	if _, ok := c.Get(key); ok {
		t.Fatal("Get: set still served after minimum TTL elapsed")
	}
}

func TestStoreRejectsSplitOrMergedSets(t *testing.T) {
	c := rrset.NewCache(newFakeClock().now)

	if _, err := c.Store(nil, rrset.LevelSecure); !errors.Is(err, rrset.ErrEmptySet) {
		t.Fatalf("Store(nil) err = %v, want ErrEmptySet", err)
	}
	mixed := []rrset.Record{
		aRecord("www.example.org", "192.0.2.1", 60),
		{Name: "www.example.org", Type: 28, Class: 1, TTL: 60, Data: "2001:db8::1"},
	}
	if _, err := c.Store(mixed, rrset.LevelSecure); !errors.Is(err, rrset.ErrMixedSet) {
		t.Fatalf("Store(mixed) err = %v, want ErrMixedSet", err)
	}
}

func TestLowTrustCannotDisplaceValidHighTrustSet(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "bank.example.org", Type: 1, Class: 1}

	if _, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.1", 120)}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store secure: %v", err)
	}
	stored, err := c.Store([]rrset.Record{aRecord(key.Name, "198.51.100.1", 120)}, rrset.LevelInsecure)
	if err != nil {
		t.Fatalf("Store insecure: %v", err)
	}
	if stored {
		t.Fatal("insecure update displaced a still-valid secure set")
	}
	set, ok := c.Get(key)
	if !ok || set.Level != rrset.LevelSecure || set.Records[0].Data != "192.0.2.1" {
		t.Fatalf("Get = %+v, %v; want original secure set", set, ok)
	}

	// Once the secure set expires, the insecure answer may be cached.
	clock.advance(120 * time.Second)
	stored, err = c.Store([]rrset.Record{aRecord(key.Name, "198.51.100.1", 120)}, rrset.LevelInsecure)
	if err != nil || !stored {
		t.Fatalf("Store after expiry = (%v, %v), want (true, nil)", stored, err)
	}
	set, ok = c.Get(key)
	if !ok || set.Level != rrset.LevelInsecure || set.Records[0].Data != "198.51.100.1" {
		t.Fatalf("Get after expiry = %+v, %v; want insecure replacement", set, ok)
	}
}

func TestAtomicReplacePreservesWholeSetAndLevel(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "www.example.org", Type: 1, Class: 1}

	if _, err := c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.1", 300),
		aRecord(key.Name, "192.0.2.2", 300),
	}, rrset.LevelInsecure); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// A same-trust refresh atomically replaces the whole set: no record
	// from the old set may survive alongside the new one.
	if _, err := c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.9", 300),
	}, rrset.LevelInsecure); err != nil {
		t.Fatalf("Store refresh: %v", err)
	}
	set, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: set missing after refresh")
	}
	if len(set.Records) != 1 || set.Records[0].Data != "192.0.2.9" {
		t.Fatalf("Get = %+v, want only the replacement record", set.Records)
	}
	if set.Level != rrset.LevelInsecure {
		t.Fatalf("level = %v, want insecure", set.Level)
	}
	// A higher-trust update replaces a valid lower-trust set.
	if _, err := c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.10", 300),
	}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store secure upgrade: %v", err)
	}
	set, _ = c.Get(key)
	if set.Level != rrset.LevelSecure || set.Records[0].Data != "192.0.2.10" {
		t.Fatalf("Get = %+v, want secure upgrade", set)
	}
}

func TestDuplicateRecordsAreCollapsed(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "mx.example.org", Type: 1, Class: 1}

	stored, err := c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.1", 300),
		aRecord(key.Name, "192.0.2.1", 300),
		aRecord(key.Name, "192.0.2.1", 60), // duplicate rdata, smaller TTL
		aRecord(key.Name, "192.0.2.2", 300),
	}, rrset.LevelSecure)
	if err != nil || !stored {
		t.Fatalf("Store = (%v, %v), want (true, nil)", stored, err)
	}
	set, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: set missing")
	}
	if len(set.Records) != 2 {
		t.Fatalf("Get returned %d records, want 2 deduplicated", len(set.Records))
	}
	// The duplicate's smaller TTL still bounds the whole set.
	clock.advance(60 * time.Second)
	if _, ok := c.Get(key); ok {
		t.Fatal("Get: set served past the duplicate record's smaller TTL")
	}
}

func TestZeroTTLSetIsNeverCached(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "zero.example.org", Type: 1, Class: 1}

	stored, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.1", 0)}, rrset.LevelSecure)
	if err != nil {
		t.Fatalf("Store zero TTL: %v", err)
	}
	if stored {
		t.Fatal("zero-TTL set was installed")
	}
	if _, ok := c.Get(key); ok {
		t.Fatal("Get: zero-TTL set served from cache")
	}

	// One zero-TTL record makes the whole set uncacheable: the set
	// expires at the minimum TTL, which is zero.
	stored, err = c.Store([]rrset.Record{
		aRecord(key.Name, "192.0.2.1", 300),
		aRecord(key.Name, "192.0.2.2", 0),
	}, rrset.LevelSecure)
	if err != nil || stored {
		t.Fatalf("Store mixed zero TTL = (%v, %v), want (false, nil)", stored, err)
	}
	if _, ok := c.Get(key); ok {
		t.Fatal("Get: set with a zero-TTL member served from cache")
	}

	// A zero-TTL answer must not evict a still-valid cached set.
	if _, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.9", 300)}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.1", 0)}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store zero TTL: %v", err)
	}
	set, ok := c.Get(key)
	if !ok || set.Records[0].Data != "192.0.2.9" {
		t.Fatalf("Get = %+v, %v; zero-TTL update evicted the valid set", set, ok)
	}
}

func TestConcurrentQueriesAndUpdates(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "hot.example.org", Type: 1, Class: 1}

	if _, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.1", 30)}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// The clock stays frozen during the race, so the secure set is always
	// valid and no insecure update may ever displace it.
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				set, ok := c.Get(key)
				if ok {
					// Readers must only ever observe a complete,
					// self-consistent set, never a mix of two updates.
					if len(set.Records) != 1 {
						t.Errorf("reader observed partial set: %+v", set.Records)
						return
					}
					if set.Level != rrset.LevelSecure {
						t.Errorf("reader observed downgraded level %v", set.Level)
						return
					}
				}
				if i%10 == 0 {
					data := fmt.Sprintf("192.0.2.%d", id)
					if _, err := c.Store([]rrset.Record{aRecord(key.Name, data, 30)}, rrset.LevelSecure); err != nil {
						t.Errorf("Store: %v", err)
						return
					}
					// Low-trust updates race too and must never win
					// against a valid secure set.
					if _, err := c.Store([]rrset.Record{aRecord(key.Name, "203.0.113.1", 30)}, rrset.LevelInsecure); err != nil {
						t.Errorf("Store insecure: %v", err)
						return
					}
				}
			}
		}(worker)
	}
	wg.Wait()

	// Advance past the TTL of the last stored set; the cache must not
	// serve stale data.
	clock.advance(31 * time.Second)
	if set, ok := c.Get(key); ok {
		t.Fatalf("Get after clock ran past all TTLs = %+v, want miss", set)
	}
}

func TestGetReturnsIndependentCopy(t *testing.T) {
	clock := newFakeClock()
	c := rrset.NewCache(clock.now)
	key := rrset.Key{Name: "copy.example.org", Type: 1, Class: 1}

	if _, err := c.Store([]rrset.Record{aRecord(key.Name, "192.0.2.1", 300)}, rrset.LevelSecure); err != nil {
		t.Fatalf("Store: %v", err)
	}
	set, ok := c.Get(key)
	if !ok {
		t.Fatal("Get: set missing")
	}
	set.Records[0].Data = "203.0.113.99"
	again, ok := c.Get(key)
	if !ok || again.Records[0].Data != "192.0.2.1" {
		t.Fatalf("Get after caller mutation = %+v, %v; cache was mutated", again, ok)
	}
}
