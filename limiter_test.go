package netsec

import (
	"fmt"
	"net/netip"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return addr
}

func newTestLimiter(c *fakeClock) *Limiter {
	c.t = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	return NewLimiter(withClock(c.now))
}

func offerFor(t *testing.T, id, clientID, addr string, restricted bool) Offer {
	t.Helper()
	return Offer{ID: id, ClientID: clientID, Addr: mustAddr(t, addr), Restricted: restricted}
}

func proxyFor(t *testing.T, id, addr string, self NATType) Proxy {
	t.Helper()
	return Proxy{ID: id, Addr: mustAddr(t, addr), SelfNAT: self}
}

func idRune(n int) string { return string(rune('a' + n)) }

// 同前缀代理无论怎么轮换标识，都共享每小时 20 个不同客户端的限额。
func TestRotatingProxyIDsSharePrefixQuota(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	for i := 0; i < DefaultMaxClients; i++ {
		offer := offerFor(t, "offer-"+idRune(i), "client-"+idRune(i), "198.51.100.200", false)
		if err := l.Enqueue(offer); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		proxy := proxyFor(t, "proxy-rotating-"+idRune(i), fmt.Sprintf("203.0.113.%d", i+1), NATUnrestricted)
		matched, err := l.Poll(proxy)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if matched == nil || matched.ID != offer.ID {
			t.Fatalf("offer %d not delivered to rotating proxy", i)
		}
	}

	stats := l.Stats()
	if len(stats) != 1 || stats[0].Active != DefaultMaxClients {
		t.Fatalf("want one full /24 with %d active clients, got %+v", DefaultMaxClients, stats)
	}

	extra := offerFor(t, "offer-extra", "client-extra", "198.51.100.201", false)
	if err := l.Enqueue(extra); err != nil {
		t.Fatalf("enqueue extra: %v", err)
	}
	matched, err := l.Poll(proxyFor(t, "proxy-brand-new-id", "203.0.113.99", NATUnrestricted))
	if err != nil {
		t.Fatalf("saturated prefix must not error, got %v", err)
	}
	if matched != nil {
		t.Fatalf("saturated prefix must be skipped, got offer %q", matched.ID)
	}

	matched, err = l.Poll(proxyFor(t, "proxy-other-prefix", "192.0.2.1", NATUnrestricted))
	if err != nil || matched == nil || matched.ID != "offer-extra" {
		t.Fatalf("skipped offer should remain available, got %+v err=%v", matched, err)
	}
}

// 同一客户端对同一前缀的重复投递只占用一个名额。
func TestDuplicateClientCountsOnce(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	const repeats = 5
	for i := 0; i < repeats; i++ {
		offer := offerFor(t, "dup-offer-"+idRune(i), "same-client", "198.51.100.10", false)
		if err := l.Enqueue(offer); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		matched, err := l.Poll(proxyFor(t, "p"+idRune(i), "203.0.113.5", NATUnrestricted))
		if err != nil || matched == nil {
			t.Fatalf("repeat %d: matched=%v err=%v", i, matched, err)
		}
	}

	stats := l.Stats()
	if len(stats) != 1 || stats[0].Active != 1 {
		t.Fatalf("same client must count once, got %+v", stats)
	}

	for i := 0; i < DefaultMaxClients-1; i++ {
		offer := offerFor(t, "fill-"+idRune(i), "client-fill-"+idRune(i), "198.51.100.11", false)
		if err := l.Enqueue(offer); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if _, err := l.Poll(proxyFor(t, "fp"+idRune(i), "203.0.113.6", NATUnrestricted)); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if l.Stats()[0].Active != DefaultMaxClients {
		t.Fatalf("prefix should be full at %d", DefaultMaxClients)
	}
	if err := l.Enqueue(offerFor(t, "one-too-many", "brand-new-client", "198.51.100.12", false)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err := l.Poll(proxyFor(t, "overflow", "203.0.113.7", NATUnrestricted))
	if err != nil || matched != nil {
		t.Fatalf("want silent skip, matched=%v err=%v", matched, err)
	}
}

// 代理自报 NAT 与最近探测结果冲突时，以探测结果为准。
func TestProbeOverridesSelfReportedNAT(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	lyingProxy := proxyFor(t, "lying-proxy", "203.0.113.10", NATUnrestricted)
	l.RecordProbe("lying-proxy", NATRestricted, clock.now())

	if err := l.Enqueue(offerFor(t, "restricted-offer", "rc", "198.51.100.1", true)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err := l.Poll(lyingProxy)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if matched != nil {
		t.Fatalf("restricted client must not reach probe-confirmed restricted proxy")
	}
	// 被 NAT 规则跳过的 offer 仍留在队列中，交给后续非限制型代理。
	if matched, err = l.Poll(proxyFor(t, "cleanup", "192.0.2.50", NATUnrestricted)); err != nil || matched == nil || matched.ID != "restricted-offer" {
		t.Fatalf("skipped restricted offer should remain queued, got %v err=%v", matched, err)
	}

	if err := l.Enqueue(offerFor(t, "open-offer", "oc", "198.51.100.2", false)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err = l.Poll(lyingProxy)
	if err != nil || matched == nil || matched.ID != "open-offer" {
		t.Fatalf("unrestricted client should match, got %v err=%v", matched, err)
	}

	modestProxy := proxyFor(t, "modest-proxy", "203.0.113.20", NATRestricted)
	l.RecordProbe("modest-proxy", NATUnrestricted, clock.now())
	if err := l.Enqueue(offerFor(t, "restricted-offer-2", "rc2", "198.51.100.3", true)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err = l.Poll(modestProxy)
	if err != nil || matched == nil || matched.ID != "restricted-offer-2" {
		t.Fatalf("probe-confirmed unrestricted proxy must serve restricted client, got %v err=%v", matched, err)
	}

	unknownProxy := proxyFor(t, "fresh-proxy", "203.0.113.30", NATUnknown)
	if err := l.Enqueue(offerFor(t, "restricted-offer-3", "rc3", "198.51.100.4", true)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err = l.Poll(unknownProxy)
	if err != nil || matched == nil || matched.ID != "restricted-offer-3" {
		t.Fatalf("unknown NAT defaults to unrestricted, got %v err=%v", matched, err)
	}

	selfRestricted := proxyFor(t, "self-restricted", "203.0.113.40", NATRestricted)
	if err := l.Enqueue(offerFor(t, "restricted-offer-4", "rc4", "198.51.100.5", true)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if matched, err = l.Poll(selfRestricted); err != nil || matched != nil {
		t.Fatalf("self-reported restricted proxy must be skipped, got %v err=%v", matched, err)
	}
	matched, err = l.Poll(proxyFor(t, "open-proxy", "192.0.2.9", NATUnrestricted))
	if err != nil || matched == nil || matched.ID != "restricted-offer-4" {
		t.Fatalf("restricted offer should survive for an unrestricted proxy, got %v err=%v", matched, err)
	}
}

// 更新的探测结果覆盖更旧的结果，迟到的旧结果不能回滚。
func TestLatestProbeWins(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	l.RecordProbe("p", NATRestricted, clock.now())
	l.RecordProbe("p", NATUnrestricted, clock.now().Add(time.Minute))
	l.RecordProbe("p", NATRestricted, clock.now().Add(-time.Minute))

	if err := l.Enqueue(offerFor(t, "o", "c", "198.51.100.9", true)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	matched, err := l.Poll(proxyFor(t, "p", "203.0.113.50", NATRestricted))
	if err != nil || matched == nil {
		t.Fatalf("latest unrestricted probe should win, got %v err=%v", matched, err)
	}
}
