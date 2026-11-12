package netsec

import (
	"net/netip"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestCache(t *testing.T, minTTL time.Duration) (*Cache, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
	cache := NewCache(WithMinTTL(minTTL), WithClock(clock.now))
	return cache, clock
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return addr
}

// A name that no matchName/matchPattern covers must never populate the cache,
// even when it resolves to the same IP as an allowed name.
func TestUnmatchedNameNotRecorded(t *testing.T) {
	cache, _ := newTestCache(t, 0)
	cache.AddMatchName("allowed.example.com")

	evil := mustAddr(t, "203.0.113.7")
	if got := cache.Update(DNSResponse{
		Name: "evil.example.org",
		IPs:  []netip.Addr{evil},
		TTL:  60,
	}); got != nil {
		t.Fatalf("unmatched name recorded %v", got)
	}
	if cache.Allowed(evil) {
		t.Fatal("IP allowed solely from an unmatched queried name")
	}
	if cache.Len() != 0 {
		t.Fatalf("cache should stay empty, got %d entries", cache.Len())
	}
}

// '*' matches characters inside one label only; it must not span a dot, and
// patterns cannot match names with a different number of labels.
func TestWildcardDoesNotCrossDots(t *testing.T) {
	cache, clock := newTestCache(t, time.Minute)
	if !cache.AddMatchPattern("*.example.com") {
		t.Fatal("valid pattern rejected")
	}

	singleLabel := mustAddr(t, "198.51.100.1")
	if cache.Update(DNSResponse{Name: "api.example.com", IPs: []netip.Addr{singleLabel}, TTL: 10}) == nil {
		t.Fatal("api.example.com should match *.example.com")
	}
	if !cache.Allowed(singleLabel) {
		t.Fatal("matched wildcard name should allow its IP")
	}

	crossDot := mustAddr(t, "198.51.100.2")
	if got := cache.Update(DNSResponse{
		Name: "a.b.example.com",
		IPs:  []netip.Addr{crossDot},
		TTL:  10,
	}); got != nil {
		t.Fatalf("wildcard crossed a dot: recorded %v", got)
	}
	if cache.Allowed(crossDot) {
		t.Fatal("a.b.example.com must not match *.example.com")
	}

	// A bare label host must not match a two-label suffix either.
	bare := mustAddr(t, "198.51.100.3")
	if got := cache.Update(DNSResponse{Name: "example.com", IPs: []netip.Addr{bare}, TTL: 10}); got != nil {
		t.Fatalf("parent domain matched child pattern: %v", got)
	}

	// In-label wildcards (prefix/suffix/infix) work but stay label-local.
	cache.AddMatchPattern("cdn-*.example.com")
	cdn := mustAddr(t, "198.51.100.4")
	if cache.Update(DNSResponse{Name: "cdn-1.example.com", IPs: []netip.Addr{cdn}, TTL: 10}) == nil {
		t.Fatal("in-label wildcard should match cdn-1.example.com")
	}

	// The floor keeps the first entry alive past its 10s answer TTL.
	clock.advance(30 * time.Second)
	if !cache.Allowed(singleLabel) || !cache.Allowed(cdn) {
		t.Fatal("minTTL floor should still keep wildcard entries alive")
	}
}

// Every hop of a CNAME chain has to be policy-covered; one uncovered hop
// vetoes recording the terminal addresses.
func TestCNAMEChainMustBeFullyCovered(t *testing.T) {
	cache, _ := newTestCache(t, time.Minute)
	cache.AddMatchName("www.policy.test")
	cache.AddMatchName("edge.cdn.test")
	cache.AddMatchName("target.public.test")

	final := mustAddr(t, "192.0.2.10")

	// Fully covered chain: queried name and every CNAME target are covered.
	if cache.Update(DNSResponse{
		Name:   "www.policy.test",
		CNAMEs: []string{"edge.cdn.test", "target.public.test"},
		IPs:    []netip.Addr{final},
		TTL:    30,
	}) == nil {
		t.Fatal("fully covered CNAME chain should be recorded")
	}
	if !cache.Allowed(final) {
		t.Fatal("final address of covered chain should be allowed")
	}

	// One hop outside policy must veto the answer even though the queried
	// name and the final target are covered.
	vetoed := mustAddr(t, "192.0.2.11")
	if got := cache.Update(DNSResponse{
		Name:   "www.policy.test",
		CNAMEs: []string{"edge.cdn.test", "rogue.external.test", "target.public.test"},
		IPs:    []netip.Addr{vetoed},
		TTL:    30,
	}); got != nil {
		t.Fatalf("uncovered CNAME hop must veto recording, got %v", got)
	}
	if cache.Allowed(vetoed) {
		t.Fatal("IP recorded despite an uncovered CNAME hop")
	}

	// Wildcards apply per hop as well, and still cannot cross dots.
	cache.AddMatchPattern("*.cdn.test")
	viaWildcard := mustAddr(t, "192.0.2.12")
	if cache.Update(DNSResponse{
		Name:   "www.policy.test",
		CNAMEs: []string{"node-7.cdn.test"},
		IPs:    []netip.Addr{viaWildcard},
		TTL:    30,
	}) == nil {
		t.Fatal("wildcard-covered CNAME hop should be accepted")
	}
	if !cache.Allowed(viaWildcard) {
		t.Fatal("wildcard CNAME chain answer should be allowed")
	}
}

// Associations die at max(answer TTL, minTTL) and an expired name revokes the
// IP once no other live name covers it.
func TestTTLExpiryRevokesIP(t *testing.T) {
	cache, clock := newTestCache(t, 30*time.Second)
	cache.AddMatchName("short.policy.test")

	ip := mustAddr(t, "198.18.0.1")
	cache.Update(DNSResponse{Name: "short.policy.test", IPs: []netip.Addr{ip}, TTL: 5})

	// Answer TTL 5s is below the 30s floor, so the floor wins.
	clock.advance(20 * time.Second)
	if !cache.Allowed(ip) {
		t.Fatal("entry should be held to the minTTL floor")
	}
	clock.advance(11 * time.Second)
	if cache.Allowed(ip) {
		t.Fatal("entry must be revoked after the floor deadline passes")
	}
	if cache.Len() != 0 {
		t.Fatalf("expired entries should be reaped, got %d", cache.Len())
	}

	// A large answer TTL overrides the floor.
	cache.Update(DNSResponse{Name: "short.policy.test", IPs: []netip.Addr{ip}, TTL: 120})
	clock.advance(60 * time.Second)
	if !cache.Allowed(ip) {
		t.Fatal("answer TTL larger than the floor should govern expiry")
	}
	clock.advance(61 * time.Second)
	if cache.Allowed(ip) {
		t.Fatal("entry should expire at the answer TTL, not the floor")
	}
}

// Multiple matched names sharing one IP: the IP stays allowed while any name
// is live and is revoked only when the last name expires; re-answering
// refreshes the deadline.
func TestMultipleNamesSharedIP(t *testing.T) {
	cache, clock := newTestCache(t, 0)
	cache.AddMatchName("a.policy.test")
	cache.AddMatchName("b.policy.test")

	shared := mustAddr(t, "198.18.50.50")
	cache.Update(DNSResponse{Name: "a.policy.test", IPs: []netip.Addr{shared}, TTL: 10})
	cache.Update(DNSResponse{Name: "b.policy.test", IPs: []netip.Addr{shared}, TTL: 30})

	if names := cache.NamesForIP(shared); len(names) != 2 {
		t.Fatalf("expected two live names for shared IP, got %v", names)
	}

	clock.advance(15 * time.Second)
	if !cache.Allowed(shared) {
		t.Fatal("shared IP must stay allowed while b is still live")
	}
	if names := cache.NamesForIP(shared); len(names) != 1 || names[0] != "b.policy.test" {
		t.Fatalf("only b should remain live, got %v", names)
	}

	clock.advance(16 * time.Second)
	if cache.Allowed(shared) {
		t.Fatal("shared IP revoked once every covering name expires")
	}

	// Refresh: a new answer for a live/expired name re-establishes access.
	cache.Update(DNSResponse{Name: "a.policy.test", IPs: []netip.Addr{shared}, TTL: 10})
	if !cache.Allowed(shared) {
		t.Fatal("re-answer should restore the shared IP")
	}
}

// Case and trailing-root normalization must not create duplicate identities.
func TestNameNormalization(t *testing.T) {
	cache, _ := newTestCache(t, time.Minute)
	cache.AddMatchName("Allowed.Example.COM.")

	ip := mustAddr(t, "203.0.113.55")
	if cache.Update(DNSResponse{Name: "allowed.example.com", IPs: []netip.Addr{ip}, TTL: 60}) == nil {
		t.Fatal("normalized exact name should match")
	}
	if names := cache.NamesForIP(ip); len(names) != 1 || names[0] != "allowed.example.com" {
		t.Fatalf("unexpected normalized names: %v", names)
	}

	if cache.AddMatchPattern("bad..example.com") {
		t.Fatal("pattern with an empty label must be rejected")
	}
	if cache.AddMatchName("  ") {
		t.Fatal("blank match name must be rejected")
	}
}
