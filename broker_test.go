package netsec

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", s, err)
	}
	return addr
}

func TestPrefixOf(t *testing.T) {
	cases := []struct {
		addr, want string
	}{
		{"192.0.2.7", "192.0.2.0/24"},
		{"192.0.2.250", "192.0.2.0/24"},
		{"::ffff:192.0.2.7", "192.0.2.0/24"},
		{"2001:db8:abcd:1234::1", "2001:db8:abcd::/48"},
		{"2001:db8:abcd:ffff::1", "2001:db8:abcd::/48"},
		{"2001:db8:abce::1", "2001:db8:abce::/48"},
	}
	for _, c := range cases {
		if got := PrefixOf(mustAddr(t, c.addr)); got.String() != c.want {
			t.Errorf("PrefixOf(%s) = %s, want %s", c.addr, got, c.want)
		}
	}
}

// 轮换代理标识：同一 /24 前缀下不断更换代理 ID，限额仍按前缀生效。
func TestRotatingProxyIDsSamePrefix(t *testing.T) {
	b := NewBroker(nil)
	prefix := PrefixOf(mustAddr(t, "203.0.113.1"))

	// 每个客户端换一个全新的代理 ID，但地址都在同一 /24。
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("proxy-%d", i)
		b.RegisterProxy(Proxy{
			ID:          id,
			Addr:        mustAddr(t, fmt.Sprintf("203.0.113.%d", i+1)),
			ReportedNAT: NATUnrestricted,
		})
		if _, ok := b.Match(fmt.Sprintf("client-%d", i), NATUnknown); !ok {
			t.Fatalf("client-%d should match, prefix has %d/20 clients", i, i)
		}
	}
	if got := b.limiter.DistinctClients(prefix); got != 20 {
		t.Fatalf("DistinctClients = %d, want 20", got)
	}

	// 再注册一个新 ID 的代理，第 21 个不同客户端仍应被跳过。
	b.RegisterProxy(Proxy{ID: "proxy-20", Addr: mustAddr(t, "203.0.113.200"), ReportedNAT: NATUnrestricted})
	if id, ok := b.Match("client-20", NATUnknown); ok {
		t.Fatalf("client-20 matched %q in exhausted prefix, want skip", id)
	}

	// 不同前缀的代理不受影响。
	b.RegisterProxy(Proxy{ID: "proxy-other", Addr: mustAddr(t, "203.0.114.1"), ReportedNAT: NATUnrestricted})
	if id, ok := b.Match("client-20", NATUnknown); !ok || id != "proxy-other" {
		t.Fatalf("client-20 = (%q, %v), want (proxy-other, true)", id, ok)
	}
}

// 重复客户端：同一客户端对同一前缀反复匹配只计一次，不消耗限额。
func TestRepeatedClientCountsOnce(t *testing.T) {
	b := NewBroker(nil)
	b.RegisterProxy(Proxy{ID: "p1", Addr: mustAddr(t, "198.51.100.1"), ReportedNAT: NATUnrestricted})
	prefix := PrefixOf(mustAddr(t, "198.51.100.1"))

	for i := 0; i < 50; i++ {
		if _, ok := b.Match("loyal-client", NATUnknown); !ok {
			t.Fatalf("repeat match %d for loyal-client failed", i)
		}
	}
	if got := b.limiter.DistinctClients(prefix); got != 1 {
		t.Fatalf("DistinctClients = %d, want 1", got)
	}

	// 其余 19 个名额仍可用。
	for i := 0; i < 19; i++ {
		if _, ok := b.Match(fmt.Sprintf("client-%d", i), NATUnknown); !ok {
			t.Fatalf("client-%d should match", i)
		}
	}
	if _, ok := b.Match("client-19", NATUnknown); ok {
		t.Fatal("client-19 should be skipped after 20 distinct clients")
	}
	// 已满后，老客户端依然能匹配（重复不计数）。
	if _, ok := b.Match("loyal-client", NATUnknown); !ok {
		t.Fatal("loyal-client should still match after limit reached")
	}
}

// NAT 谎报：自报与探测冲突时以最近一次探测为准。
func TestNATProbeOverridesSelfReport(t *testing.T) {
	b := NewBroker(nil)

	// 代理谎称非限制型，探测揭穿为限制型。
	b.RegisterProxy(Proxy{ID: "liar", Addr: mustAddr(t, "192.0.2.1"), ReportedNAT: NATUnrestricted})
	b.RecordProbe("liar", NATRestricted)
	if id, ok := b.Match("restricted-client", NATRestricted); ok {
		t.Fatalf("restricted client matched NAT-liar %q, want skip", id)
	}
	// 非限制型客户端仍可匹配该代理。
	if id, ok := b.Match("open-client", NATUnrestricted); !ok || id != "liar" {
		t.Fatalf("open-client = (%q, %v), want (liar, true)", id, ok)
	}

	// 代理自报限制型，探测证明为非限制型，限制型客户端应能匹配。
	b.RegisterProxy(Proxy{ID: "modest", Addr: mustAddr(t, "192.0.3.1"), ReportedNAT: NATRestricted})
	b.RecordProbe("modest", NATUnrestricted)
	if id, ok := b.Match("restricted-client", NATRestricted); !ok || id != "modest" {
		t.Fatalf("restricted-client = (%q, %v), want (modest, true)", id, ok)
	}

	// 最近一次探测再次翻转，以最新结果为准。
	b.RecordProbe("modest", NATRestricted)
	if id, ok := b.Match("restricted-client-2", NATRestricted); ok {
		t.Fatalf("restricted-client-2 matched %q after latest probe restricted", id)
	}
}

// 限制型客户端只能匹配非限制型代理（无探测时按自报）。
func TestRestrictedClientNeedsUnrestrictedProxy(t *testing.T) {
	b := NewBroker(nil)
	b.RegisterProxy(Proxy{ID: "restricted-proxy", Addr: mustAddr(t, "198.51.100.1"), ReportedNAT: NATRestricted})
	if _, ok := b.Match("c1", NATRestricted); ok {
		t.Fatal("restricted client matched restricted proxy")
	}
	b.RegisterProxy(Proxy{ID: "open-proxy", Addr: mustAddr(t, "198.51.100.2"), ReportedNAT: NATUnrestricted})
	if id, ok := b.Match("c1", NATRestricted); !ok || id != "open-proxy" {
		t.Fatalf("c1 = (%q, %v), want (open-proxy, true)", id, ok)
	}
}

// 一小时滚动窗口边界：窗口内第 21 个客户端被拒，滑出窗口后恢复。
func TestRollingWindowBoundary(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	l := NewLimiter(WithClock(clock))
	b := NewBroker(l)
	b.RegisterProxy(Proxy{ID: "p1", Addr: mustAddr(t, "203.0.113.1"), ReportedNAT: NATUnrestricted})

	for i := 0; i < 20; i++ {
		if _, ok := b.Match(fmt.Sprintf("client-%d", i), NATUnknown); !ok {
			t.Fatalf("client-%d should match", i)
		}
	}

	// 59 分钟后窗口未滑出，第 21 个不同客户端仍被跳过。
	now = now.Add(59 * time.Minute)
	if _, ok := b.Match("client-20", NATUnknown); ok {
		t.Fatal("client-20 matched before window rolled")
	}

	// 老客户端在窗口内活跃会刷新时间，不释放名额。
	if _, ok := b.Match("client-0", NATUnknown); !ok {
		t.Fatal("client-0 repeat should match")
	}

	// 再过 2 分钟，最初的 19 个客户端滑出窗口（client-0 刚刷新过仍占用），
	// 新客户端可以获得名额。
	now = now.Add(2 * time.Minute)
	if _, ok := b.Match("client-21", NATUnknown); !ok {
		t.Fatal("client-21 should match after earliest clients left the window")
	}

	// 恰好在窗口边界上的记录应视为过期。
	now = now.Add(time.Hour)
	if got := l.DistinctClients(PrefixOf(mustAddr(t, "203.0.113.1"))); got != 0 {
		t.Fatalf("DistinctClients after full window = %d, want 0", got)
	}
}

// 每个 offer 至多交给一个代理，且并发下不超过限额。
func TestConcurrentMatchRespectsLimit(t *testing.T) {
	b := NewBroker(nil)
	for i := 0; i < 5; i++ {
		b.RegisterProxy(Proxy{
			ID:          fmt.Sprintf("p%d", i),
			Addr:        mustAddr(t, fmt.Sprintf("203.0.113.%d", i+1)),
			ReportedNAT: NATUnrestricted,
		})
	}

	var wg sync.WaitGroup
	matched := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if id, ok := b.Match(fmt.Sprintf("client-%d", i), NATUnknown); ok {
				matched <- id
			}
		}(i)
	}
	wg.Wait()
	close(matched)

	n := 0
	for range matched {
		n++
	}
	if n != 20 {
		t.Fatalf("matched %d offers, want exactly 20", n)
	}
}
