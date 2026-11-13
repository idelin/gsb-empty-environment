package netsec

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func statsFor(t *testing.T, l *Limiter, prefix string) PrefixStats {
	t.Helper()
	for _, s := range l.Stats() {
		if s.Prefix == prefix {
			return s
		}
	}
	t.Fatalf("prefix %q not found in stats %+v", prefix, l.Stats())
	return PrefixStats{}
}

// 滚动一小时窗口：窗口内满额，条目恰好滑出一小时后名额释放。
func TestRollingOneHourWindow(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	first := offerFor(t, "first", "client-0", "198.51.100.1", false)
	if err := l.Enqueue(first); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := l.Poll(proxyFor(t, "p0", "203.0.113.1", NATUnrestricted)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	for i := 1; i < DefaultMaxClients; i++ {
		clock.advance(time.Minute)
		offer := offerFor(t, fmt.Sprintf("o-%d", i), fmt.Sprintf("client-%d", i), "198.51.100.2", false)
		if err := l.Enqueue(offer); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if _, err := l.Poll(proxyFor(t, fmt.Sprintf("p-%d", i), "203.0.113.2", NATUnrestricted)); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if statsFor(t, l, "203.0.113.0/24").Active != DefaultMaxClients {
		t.Fatalf("prefix should be full at %d", DefaultMaxClients)
	}

	// 推进到最早客户端满一小时前 1 秒，全部条目仍在窗口内。
	clock.advance(time.Hour - 19*time.Minute - time.Second)
	if err := l.Enqueue(offerFor(t, "late", "late-client", "198.51.100.3", false)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if matched, err := l.Poll(proxyFor(t, "px", "203.0.113.3", NATUnrestricted)); err != nil || matched != nil {
		t.Fatalf("still inside window: want skip, got %v err=%v", matched, err)
	}

	// 再推进 2 秒，最早的客户端年龄超过一小时并滑出窗口，恰好释放一个名额。
	clock.advance(2 * time.Second)
	matched, err := l.Poll(proxyFor(t, "py", "203.0.113.4", NATUnrestricted))
	if err != nil {
		t.Fatalf("poll after window: %v", err)
	}
	if matched == nil || matched.ID != "late" {
		t.Fatalf("slot should free at the one-hour boundary, got %v", matched)
	}
	if active := statsFor(t, l, "203.0.113.0/24").Active; active != DefaultMaxClients {
		t.Fatalf("freed slot refilled by late client, want %d active, got %d", DefaultMaxClients, active)
	}

	clock.advance(time.Hour + time.Second)
	if _, err := l.Poll(proxyFor(t, "pz", "203.0.113.5", NATUnrestricted)); err != nil {
		t.Fatalf("idle poll: %v", err)
	}
	for _, s := range l.Stats() {
		if s.Active != 0 {
			t.Fatalf("all entries should be expired, got %+v", l.Stats())
		}
	}
	l.Sweep()
	if len(l.Stats()) != 0 || len(l.prefixes) != 0 {
		t.Fatalf("Sweep should reclaim empty prefix states, got %+v", l.prefixes)
	}
}

// 每个 offer 最多交给一个代理：被取走后不会再次出现在后续轮询中。
func TestOfferDeliveredAtMostOnce(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	if err := l.Enqueue(offerFor(t, "only", "c1", "198.51.100.7", false)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := l.Poll(proxyFor(t, "winner", "203.0.113.70", NATUnrestricted))
	if err != nil || first == nil || first.ID != "only" {
		t.Fatalf("first poll should take the offer, got %v err=%v", first, err)
	}
	second, err := l.Poll(proxyFor(t, "runner-up", "203.0.113.71", NATUnrestricted))
	if err != nil || second != nil {
		t.Fatalf("offer must not be redelivered, got %v err=%v", second, err)
	}

	// 同一 Offer.ID 重复入队是幂等的，不会让已分配的 offer 复活。
	if err := l.Enqueue(offerFor(t, "only", "c1", "198.51.100.7", false)); err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	third, err := l.Poll(proxyFor(t, "another", "203.0.113.72", NATUnrestricted))
	if err != nil || third != nil {
		t.Fatalf("duplicate enqueue of assigned offer must be ignored, got %v err=%v", third, err)
	}
}

// IPv6 按 /48 聚合：同 /48 内不同地址与轮换标识共享 20 个客户端的限额。
func TestIPv6Slash48Quota(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	for i := 0; i < DefaultMaxClients; i++ {
		offer := offerFor(t, "v6-o-"+idRune(i), "v6-c-"+idRune(i), "2001:db8:abcd:100::10", false)
		if err := l.Enqueue(offer); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		proxyAddr := fmt.Sprintf("2001:db8:aaaa:%x::%x", i%4, i+1)
		if _, err := l.Poll(proxyFor(t, "v6-p-"+idRune(i), proxyAddr, NATUnrestricted)); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	stats := l.Stats()
	if len(stats) != 1 || stats[0].Prefix != "2001:db8:aaaa::/48" || stats[0].Active != DefaultMaxClients {
		t.Fatalf("want one full /48 prefix, got %+v", stats)
	}

	if err := l.Enqueue(offerFor(t, "v6-extra", "v6-c-extra", "2001:db8:abcd:100::11", false)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if matched, err := l.Poll(proxyFor(t, "v6-new-id", "2001:db8:aaaa:ffff::99", NATUnrestricted)); err != nil || matched != nil {
		t.Fatalf("saturated /48 must skip silently, got %v err=%v", matched, err)
	}

	// 相邻 /48 是独立限额域，可以立即取走该 offer。
	matched, err := l.Poll(proxyFor(t, "v6-neighbor", "2001:db8:aaab::1", NATUnrestricted))
	if err != nil || matched == nil || matched.ID != "v6-extra" {
		t.Fatalf("neighbor /48 has independent quota, got %v err=%v", matched, err)
	}
}

func TestPrefixOf(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"203.0.113.77", "203.0.113.0/24"},
		{"192.0.2.255", "192.0.2.0/24"},
		{"2001:db8:aaaa:1234::5678", "2001:db8:aaaa::/48"},
		// IPv4 映射的 IPv6 地址先还原，避免同一主机产生两个键。
		{"::ffff:203.0.113.77", "203.0.113.0/24"},
		// 带链路本地 zone 的地址不影响 /48 归一化。
		{"fe80:dead:beef:1::1%eth0", "fe80:dead:beef::/48"},
	}
	for _, tc := range cases {
		got, err := prefixOf(mustAddr(t, tc.addr))
		if err != nil {
			t.Fatalf("prefixOf(%q): %v", tc.addr, err)
		}
		if got != tc.want {
			t.Fatalf("prefixOf(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	if err := l.Enqueue(Offer{ID: "", ClientID: "c", Addr: mustAddr(t, "198.51.100.1")}); err != ErrInvalidOffer {
		t.Fatalf("missing offer ID: want ErrInvalidOffer, got %v", err)
	}
	if err := l.Enqueue(Offer{ID: "o", ClientID: "", Addr: mustAddr(t, "198.51.100.1")}); err != ErrInvalidOffer {
		t.Fatalf("missing client ID: want ErrInvalidOffer, got %v", err)
	}
	if _, err := l.Poll(Proxy{ID: "", Addr: mustAddr(t, "203.0.113.1")}); err != ErrInvalidProxy {
		t.Fatalf("missing proxy ID: want ErrInvalidProxy, got %v", err)
	}

	// 空轮询不是错误。
	if matched, err := l.Poll(proxyFor(t, "idle", "203.0.113.9", NATUnrestricted)); err != nil || matched != nil {
		t.Fatalf("empty queue should return (nil, nil), got %v err=%v", matched, err)
	}
}

// 并发轮询下每个 offer 仍然恰好交给一个代理。
func TestConcurrentPollsEachOfferOnce(t *testing.T) {
	clock := &fakeClock{}
	l := newTestLimiter(clock)

	const offerCount = 50
	for i := 0; i < offerCount; i++ {
		if err := l.Enqueue(offerFor(t, fmt.Sprintf("offer-%d", i), fmt.Sprintf("client-%d", i), "198.51.100.1", false)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		seen    = make(map[string]int)
		workers = 20
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			proxy := proxyFor(t, fmt.Sprintf("worker-%d", id), fmt.Sprintf("10.%d.0.1", id+1), NATUnrestricted)
			for {
				offer, err := l.Poll(proxy)
				if err != nil {
					t.Errorf("poll: %v", err)
					return
				}
				if offer == nil {
					return
				}
				mu.Lock()
				seen[offer.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(seen) != offerCount {
		t.Fatalf("want %d distinct deliveries, got %d", offerCount, len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("offer %q delivered %d times, want exactly once", id, count)
		}
	}
}
