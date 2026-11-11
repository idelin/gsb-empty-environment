package ecscache

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"
)

const bigTTL = time.Hour

type fakeClock struct {
	t time.Time
}

func newTestCache() (*Cache, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return newWithClock(func() time.Time { return clk.t }), clk
}

func v4(last uint8) netip.Addr {
	return netip.AddrFrom4([4]byte{10, 0, 0, last})
}

func mustPrefix(t *testing.T, addr netip.Addr, bits uint8) Prefix {
	t.Helper()
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	p := netip.PrefixFrom(addr, int(bits)).Masked()
	return Prefix{Family: familyOf(addr), Bits: bits, Addr: p.Addr()}
}

// oracleEntry mirrors one independently modeled cache record over the small
// 10.0.0.0/24 address space. Only the last address byte varies.
type oracleEntry struct {
	sourceBits uint8
	scopeBits  uint8
	bucket     uint8 // last byte masked to scopeBits
}

type oracle struct {
	sourceBits uint8
	entries    map[oracleEntry]string
}

func lastByteMask(scopeBits uint8) uint8 {
	if scopeBits <= 24 {
		return 0
	}
	return uint8(0xff << (32 - scopeBits))
}

func (o *oracle) store(last, sourceBits, scopeBits uint8, value string) {
	e := oracleEntry{
		sourceBits: sourceBits,
		scopeBits:  scopeBits,
		bucket:     last & lastByteMask(scopeBits),
	}
	o.entries[e] = value
}

func (o *oracle) tighten(bits uint8) {
	o.sourceBits = bits
	for e := range o.entries {
		if e.scopeBits > bits {
			delete(o.entries, e)
		}
	}
}

func (o *oracle) lookup(last uint8) (string, bool) {
	var best oracleEntry
	found := false
	for e := range o.entries {
		if e.scopeBits > o.sourceBits {
			continue
		}
		if last&lastByteMask(e.scopeBits) != e.bucket {
			continue
		}
		if !found || e.scopeBits > best.scopeBits ||
			(e.scopeBits == best.scopeBits && e.sourceBits > best.sourceBits) {
			best = e
			found = true
		}
	}
	if !found {
		return "", false
	}
	return o.entries[best], true
}

// TestExhaustiveSmallSpace enumerates every client address in 10.0.0.0/24 and
// every response scope from /24 to /32, comparing the real cache against an
// independent reference model across policy tightening, widening and
// re-population. The sent prefix fields are checked on every lookup.
func TestExhaustiveSmallSpace(t *testing.T) {
	cache, clk := newTestCache()
	const tenant = "acme"
	const name = "example.com"
	const qtype = uint16(1)

	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 32, IPv6SourceBits: 128}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	ref := &oracle{sourceBits: 32, entries: map[oracleEntry]string{}}

	wantSent := func(t *testing.T, last uint8, bits uint8, sent Prefix) {
		t.Helper()
		wantAddr := v4(last & lastByteMask(bits))
		if sent.Family != FamilyIPv4 || sent.Bits != bits || sent.Addr != wantAddr {
			t.Fatalf("sent prefix = %v/%d, want %v/%d (last=%d)", sent.Addr, sent.Bits, wantAddr, bits, last)
		}
	}

	compareAll := func(phase string) {
		t.Helper()
		for last := uint8(0); ; last++ {
			client := v4(last)
			got, sent, err := cache.Lookup(tenant, client, name, qtype)
			if err != nil {
				t.Fatalf("[%s] Lookup last=%d: %v", phase, last, err)
			}
			wantSent(t, last, ref.sourceBits, sent)
			wantVal, wantHit := ref.lookup(last)
			if wantHit {
				if got == nil || string(got) != wantVal {
					t.Fatalf("[%s] last=%d hit=%q, oracle=%q", phase, last, string(got), wantVal)
				}
			} else if got != nil {
				t.Fatalf("[%s] last=%d unexpected hit %q", phase, last, got)
			}
			if last == 255 {
				break
			}
		}
	}

	// Phase A: policy /32, populate every (client, scope /24../32) pair.
	for last := uint8(0); ; last++ {
		for sc := uint8(24); sc <= 32; sc++ {
			client := v4(last)
			_, sent, err := cache.Lookup(tenant, client, name, qtype)
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			wantSent(t, last, 32, sent)
			scope := mustPrefix(t, client, sc)
			val := fmt.Sprintf("v-%d-%d", last, sc)
			if err := cache.Store(tenant, client, name, qtype, sent, scope, []byte(val), bigTTL); err != nil {
				t.Fatalf("Store last=%d scope=/%d: %v", last, sc, err)
			}
			ref.store(last, 32, sc, val)
		}
		if last == 255 {
			break
		}
		if last%64 == 63 {
			compareAll("A-checkpoint")
		}
	}
	compareAll("A-full")

	// Phase B: tighten to /24. Finer entries must be purged, no leakage.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 24, IPv6SourceBits: 128}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	ref.tighten(24)
	compareAll("B-tightened")

	for last := uint8(0); ; last++ {
		client := v4(last)
		_, sent, err := cache.Lookup(tenant, client, name, qtype)
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		wantSent(t, last, 24, sent)
		scope := mustPrefix(t, client, 24)
		val := fmt.Sprintf("w-%d", last)
		if err := cache.Store(tenant, client, name, qtype, sent, scope, []byte(val), bigTTL); err != nil {
			t.Fatalf("Store /24 last=%d: %v", last, err)
		}
		ref.store(last, 24, 24, val)
		if last == 255 {
			break
		}
	}
	compareAll("B-repopulated")

	// Phase C: widen to /28. Purged fine entries must not resurrect; new /28
	// answers must win over the coarser /24 answer.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 28, IPv6SourceBits: 128}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	ref.tighten(28)
	compareAll("C-widened")
	for last := uint8(0); ; last++ {
		client := v4(last)
		sent := mustPrefix(t, client, 28)
		scope := mustPrefix(t, client, 28)
		val := fmt.Sprintf("x-%d", last)
		if err := cache.Store(tenant, client, name, qtype, sent, scope, []byte(val), bigTTL); err != nil {
			t.Fatalf("Store /28 last=%d: %v", last, err)
		}
		ref.store(last, 28, 28, val)
		if last == 255 {
			break
		}
	}
	compareAll("C-repopulated")

	// Phase D: widen back to /32. Old /29../32 answers stay gone.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 32, IPv6SourceBits: 128}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	ref.tighten(32)
	compareAll("D-rewidened")

	// A fresh /32 answer is visible only to the single /32 client.
	client := v4(1)
	sent := mustPrefix(t, client, 32)
	scope := mustPrefix(t, client, 32)
	if err := cache.Store(tenant, client, name, qtype, sent, scope, []byte("fine"), bigTTL); err != nil {
		t.Fatalf("Store /32: %v", err)
	}
	ref.store(1, 32, 32, "fine")
	compareAll("D-fine")

	if cache.Len() != len(ref.entries) {
		t.Fatalf("Len = %d, oracle has %d", cache.Len(), len(ref.entries))
	}

	// Expiry turns every hit into a miss without removing config knowledge.
	clk.t = clk.t.Add(bigTTL + time.Second)
	if cache.Len() != 0 {
		t.Fatalf("expected all entries to expire, Len=%d", cache.Len())
	}
	for last := uint8(0); last < 10; last++ {
		got, _, err := cache.Lookup(tenant, v4(last), name, qtype)
		if err != nil || got != nil {
			t.Fatalf("post-expiry last=%d got=%v err=%v", last, got, err)
		}
	}
}

func TestStoreRejections(t *testing.T) {
	cache, _ := newTestCache()
	const tenant = "t"
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 32, IPv6SourceBits: 48}); err != nil {
		t.Fatal(err)
	}
	client := v4(5)
	sent32 := mustPrefix(t, client, 32)

	store := func(sent, scope Prefix, ttl time.Duration) error {
		return cache.Store(tenant, client, "example.com", 1, sent, scope, []byte("v"), ttl)
	}

	if err := store(sent32, mustPrefix(t, client, 32), bigTTL); err != nil {
		t.Fatalf("baseline store: %v", err)
	}
	before := cache.Len()

	// Scope longer than the sent source prefix is refused.
	err := store(sent32, Prefix{Family: FamilyIPv4, Bits: 33, Addr: client}, bigTTL)
	if !errors.Is(err, ErrScopeLongerThanSource) {
		t.Fatalf("scope 33 vs sent 32: got %v", err)
	}

	// Scope that excludes the client address is refused.
	err = store(sent32, mustPrefix(t, v4(128), 25), bigTTL)
	if !errors.Is(err, ErrScopeExcludesAddress) {
		t.Fatalf("excluding scope: got %v", err)
	}

	// Family mismatch between client and scope is refused.
	v6 := netip.MustParseAddr("2001:db8::1")
	err = store(sent32, mustPrefix(t, v6, 48), bigTTL)
	if !errors.Is(err, ErrScopeFamilyMismatch) {
		t.Fatalf("family mismatch: got %v", err)
	}

	// A sent prefix that does not follow tenant policy is refused.
	err = store(mustPrefix(t, client, 24), mustPrefix(t, client, 24), bigTTL)
	if err == nil {
		t.Fatal("sent /24 under /32 policy should be rejected")
	}

	// Non-positive TTL is refused.
	err = store(sent32, mustPrefix(t, client, 32), 0)
	if !errors.Is(err, ErrNonPositiveTTL) {
		t.Fatalf("zero ttl: got %v", err)
	}

	// Unknown tenant and invalid address are refused.
	err = cache.Store("nope", client, "example.com", 1, sent32, mustPrefix(t, client, 32), []byte("v"), bigTTL)
	if !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unknown tenant store: got %v", err)
	}
	if _, _, err := cache.Lookup("nope", client, "example.com", 1); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unknown tenant lookup: got %v", err)
	}
	if _, _, err := cache.Lookup(tenant, netip.Addr{}, "example.com", 1); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("invalid addr lookup: got %v", err)
	}

	if cache.Len() != before {
		t.Fatalf("rejected stores changed Len: before=%d after=%d", before, cache.Len())
	}

	// Out-of-range configuration is rejected and not installed.
	if err := cache.SetConfig("bad", Config{IPv4SourceBits: 33}); err == nil {
		t.Fatal("IPv4 source bits 33 should be rejected")
	}
	if _, _, err := cache.Lookup("bad", client, "example.com", 1); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("rejected config must not be installed: %v", err)
	}
}

func TestKeyIsolation(t *testing.T) {
	cache, _ := newTestCache()
	if err := cache.SetConfig("t1", Config{IPv4SourceBits: 24, IPv6SourceBits: 48}); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetConfig("t2", Config{IPv4SourceBits: 24, IPv6SourceBits: 48}); err != nil {
		t.Fatal(err)
	}
	client := v4(7)
	sent := mustPrefix(t, client, 24)
	scope := mustPrefix(t, client, 24)
	put := func(tenant, name string, qtype uint16, val string) {
		t.Helper()
		if err := cache.Store(tenant, client, name, qtype, sent, scope, []byte(val), bigTTL); err != nil {
			t.Fatalf("Store %s/%s/%d: %v", tenant, name, qtype, err)
		}
	}
	put("t1", "example.com", 1, "t1-a")
	put("t1", "example.com", 28, "t1-aaaa")
	put("t1", "other.com", 1, "t1-other")
	put("t2", "example.com", 1, "t2-a")

	hit := func(tenant, name string, qtype uint16) string {
		t.Helper()
		got, _, err := cache.Lookup(tenant, client, name, qtype)
		if err != nil || got == nil {
			t.Fatalf("Lookup %s/%s/%d: got=%v err=%v", tenant, name, qtype, got, err)
		}
		return string(got)
	}
	miss := func(tenant, name string, qtype uint16) {
		t.Helper()
		got, _, err := cache.Lookup(tenant, client, name, qtype)
		if err != nil || got != nil {
			t.Fatalf("Lookup %s/%s/%d: expected miss, got=%q err=%v", tenant, name, qtype, got, err)
		}
	}
	if v := hit("t1", "EXAMPLE.COM.", 1); v != "t1-a" {
		t.Fatalf("canonical name lookup = %q", v)
	}
	if v := hit("t1", "example.com", 28); v != "t1-aaaa" {
		t.Fatalf("qtype isolation broken: %q", v)
	}
	if v := hit("t2", "example.com", 1); v != "t2-a" {
		t.Fatalf("tenant isolation broken: %q", v)
	}
	miss("t1", "missing.com", 1)

	// Purging one tenant leaves the other intact.
	cache.PurgeTenant("t1")
	if _, _, err := cache.Lookup("t1", client, "example.com", 1); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("purged tenant should be unknown: %v", err)
	}
	if v := hit("t2", "example.com", 1); v != "t2-a" {
		t.Fatalf("t2 entry lost during t1 purge: %q", v)
	}
}

func TestIPv6TruncationAndScopeReuse(t *testing.T) {
	cache, _ := newTestCache()
	const tenant = "v6tenant"
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 24, IPv6SourceBits: 48}); err != nil {
		t.Fatal(err)
	}

	client1 := netip.MustParseAddr("2001:db8:1:2:3:4:5:6")
	client2 := netip.MustParseAddr("2001:db8:1:2:9:8:7:6")

	_, sent, err := cache.Lookup(tenant, client1, "example.com", 1)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	wantSent := netip.MustParseAddr("2001:db8:1::")
	if sent.Family != FamilyIPv6 || sent.Bits != 48 || sent.Addr != wantSent {
		t.Fatalf("sent = %v/%d, want %v/48", sent.Addr, sent.Bits, wantSent)
	}

	// A /52 response is finer than the /48 that was sent and must be refused.
	tooFine := mustPrefix(t, client1, 52)
	if err := cache.Store(tenant, client1, "example.com", 1, sent, tooFine, []byte("fine"), bigTTL); !errors.Is(err, ErrScopeLongerThanSource) {
		t.Fatalf("over-precise scope: got %v", err)
	}

	// /48 response covers every client sharing the top 48 bits.
	scope := mustPrefix(t, client1, 48)
	if err := cache.Store(tenant, client1, "example.com", 1, sent, scope, []byte("regional"), bigTTL); err != nil {
		t.Fatalf("Store /48: %v", err)
	}
	for _, client := range []netip.Addr{client1, client2} {
		got, _, err := cache.Lookup(tenant, client, "example.com", 1)
		if err != nil || string(got) != "regional" {
			t.Fatalf("client %v reuse: got=%q err=%v", client, got, err)
		}
	}

	outside := netip.MustParseAddr("2001:db9:1:2:3:4:5:6")
	if got, _, err := cache.Lookup(tenant, outside, "example.com", 1); err != nil || got != nil {
		t.Fatalf("outside /48 client: got=%q err=%v", got, err)
	}

	// Tightening to /40 removes the /48 answer.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 24, IPv6SourceBits: 40}); err != nil {
		t.Fatal(err)
	}
	if got, _, err := cache.Lookup(tenant, client1, "example.com", 1); err != nil || got != nil {
		t.Fatalf("after tightening: got=%q err=%v", got, err)
	}
}

func TestZeroScopeShared(t *testing.T) {
	cache, _ := newTestCache()
	const tenant = "z"
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 0, IPv6SourceBits: 0}); err != nil {
		t.Fatal(err)
	}
	client := v4(200)
	_, sent, err := cache.Lookup(tenant, client, "geo.example", 1)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if sent.Bits != 0 || sent.Addr != netip.MustParseAddr("0.0.0.0") {
		t.Fatalf("sent = %v/%d, want 0.0.0.0/0", sent.Addr, sent.Bits)
	}
	scope := mustPrefix(t, client, 0)
	if err := cache.Store(tenant, client, "geo.example", 1, sent, scope, []byte("global"), bigTTL); err != nil {
		t.Fatalf("Store /0: %v", err)
	}
	for last := uint8(0); last < 255; last += 17 {
		got, gotSent, err := cache.Lookup(tenant, v4(last), "geo.example", 1)
		if err != nil || string(got) != "global" {
			t.Fatalf("last=%d: got=%q err=%v", last, got, err)
		}
		if gotSent.Bits != 0 {
			t.Fatalf("last=%d sent bits=%d", last, gotSent.Bits)
		}
	}
}

func TestLongestScopeWinsAndValueDefensiveCopy(t *testing.T) {
	cache, _ := newTestCache()
	const tenant = "p"

	// Populate a /24 answer using a request constrained to /24.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 24, IPv6SourceBits: 128}); err != nil {
		t.Fatal(err)
	}
	client := v4(10)
	coarseSent := mustPrefix(t, client, 24)
	if err := cache.Store(tenant, client, "example.com", 1, coarseSent, mustPrefix(t, client, 24), []byte("coarse"), bigTTL); err != nil {
		t.Fatalf("Store coarse: %v", err)
	}

	// Widen policy and add a /32 answer for the same client.
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 32, IPv6SourceBits: 128}); err != nil {
		t.Fatal(err)
	}
	fineSent := mustPrefix(t, client, 32)
	if err := cache.Store(tenant, client, "example.com", 1, fineSent, mustPrefix(t, client, 32), []byte("fine"), bigTTL); err != nil {
		t.Fatalf("Store fine: %v", err)
	}

	if got, _, err := cache.Lookup(tenant, v4(10), "example.com", 1); err != nil || string(got) != "fine" {
		t.Fatalf("v4(10): got=%q err=%v", got, err)
	}
	if got, _, err := cache.Lookup(tenant, v4(11), "example.com", 1); err != nil || string(got) != "coarse" {
		t.Fatalf("v4(11) should fall through to /24: got=%q err=%v", got, err)
	}

	// Mutating caller buffers must not affect cached bytes.
	payload := []byte("mutable")
	if err := cache.Store(tenant, client, "mut.example", 1, fineSent, mustPrefix(t, client, 32), payload, bigTTL); err != nil {
		t.Fatalf("Store: %v", err)
	}
	payload[0] = 'X'
	got, _, err := cache.Lookup(tenant, client, "mut.example", 1)
	if err != nil || string(got) != "mutable" {
		t.Fatalf("stored value leaked caller mutation: got=%q err=%v", got, err)
	}
	got[0] = 'Y'
	again, _, err := cache.Lookup(tenant, client, "mut.example", 1)
	if err != nil || string(again) != "mutable" {
		t.Fatalf("returned value alias leaked: got=%q err=%v", again, err)
	}
}

func TestIPv4MappedIPv6(t *testing.T) {
	cache, _ := newTestCache()
	const tenant = "m"
	if err := cache.SetConfig(tenant, Config{IPv4SourceBits: 28, IPv6SourceBits: 64}); err != nil {
		t.Fatal(err)
	}
	plain := netip.MustParseAddr("192.0.2.7")
	mapped := netip.AddrFrom16(plain.As16()) // 4-in-6 representation

	_, sent, err := cache.Lookup(tenant, mapped, "example.com", 1)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if sent.Family != FamilyIPv4 || sent.Bits != 28 {
		t.Fatalf("mapped addr family = %v/%d, want IPv4/28", sent.Family, sent.Bits)
	}
	if err := cache.Store(tenant, mapped, "example.com", 1, sent, mustPrefix(t, mapped, 28), []byte("mapped"), bigTTL); err != nil {
		t.Fatalf("Store mapped: %v", err)
	}
	got, _, err := cache.Lookup(tenant, plain, "example.com", 1)
	if err != nil || string(got) != "mapped" {
		t.Fatalf("plain IPv4 reuse of 4-in-6 entry: got=%q err=%v", got, err)
	}
}
