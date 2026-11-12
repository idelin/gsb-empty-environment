package memberstate

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

// TestPortReuseAndLateRenewal 验证同一端口被新注册复用后旧租约令牌
// 立即失效；到期后的迟到续期不能重新授权已撤销端口。
func TestPortReuseAndLateRenewal(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	key := MemberKey{Group: testGroup, Tenant: "tenant-a", Port: "shared-port"}
	first, err := table.Join(Registration{
		Group:    testGroup,
		Tenant:   "tenant-a",
		Port:     "shared-port",
		Mode:     Include,
		Sources:  []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("first join: %v", err)
	}

	clock.Advance(30 * time.Second)
	if _, err := table.Renew(first, time.Minute); err != nil {
		t.Fatalf("timely renew: %v", err)
	}

	// 端口复用：同端口重新加入并切换模式，旧代次作废。
	second, err := table.Join(Registration{
		Group:    testGroup,
		Tenant:   "tenant-a",
		Port:     "shared-port",
		Mode:     Exclude,
		Sources:  []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("reuse join: %v", err)
	}
	if first == second {
		t.Fatal("reused port must receive a new lease generation")
	}
	if _, err := table.Renew(first, time.Minute); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("stale token renew err=%v, want ErrUnknownLease", err)
	}
	if err := table.Leave(first); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("stale token leave err=%v, want ErrUnknownLease", err)
	}

	// EXCLUDE 成员许可为全集减排除集合，排除前缀内无授权，防止泄漏。
	permit, ok := table.MemberPermit(key, IPv4)
	if !ok {
		t.Fatal("member missing after reuse")
	}
	for _, p := range permit {
		if p.Overlaps(mustPrefix(t, "192.0.2.0/30")) {
			t.Fatalf("excluded space leaked onto shared port via %s", p)
		}
	}

	// 租约在最近一次续期后 1 分钟（即时钟 3m30s）到期；推进到 4 分钟，
	// 迟到续期必须被拒绝，且贡献自动撤销。
	clock.Advance(4 * time.Minute)
	if _, err := table.Renew(second, time.Minute); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("late renew err=%v, want ErrUnknownLease", err)
	}
	if got := table.AllowedSources(testGroup, IPv4); len(got) != 0 {
		t.Fatalf("expired member still contributes: %v", got)
	}
}

// TestLeaveOnlyRemovesOwnContribution 验证成员离开只撤销自身贡献。
func TestLeaveOnlyRemovesOwnContribution(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	tokenA, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-a",
		Port:    "port-1",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
	})
	if err != nil {
		t.Fatalf("join a: %v", err)
	}
	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-b",
		Port:    "port-2",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "192.0.2.4/30")},
	}); err != nil {
		t.Fatalf("join b: %v", err)
	}

	before := table.AllowedSources(testGroup, IPv4)
	if len(before) != 1 || before[0].Prefix.String() != "192.0.2.0/29" {
		t.Fatalf("unexpected merged coverage: %+v", before)
	}

	if err := table.Leave(tokenA); err != nil {
		t.Fatalf("leave: %v", err)
	}
	after := table.AllowedSources(testGroup, IPv4)
	if len(after) != 1 || after[0].Prefix.String() != "192.0.2.4/30" {
		t.Fatalf("leave removed too much: %+v", after)
	}
}

// TestAtomicModeSwitch 验证同一成员模式切换以一次原子发布生效，且旧
// 租约令牌在切换后立即失效。
func TestAtomicModeSwitch(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	key := MemberKey{Group: testGroup, Tenant: "tenant-a", Port: "port-1"}
	old, err := table.Join(Registration{
		Group:    testGroup,
		Tenant:   "tenant-a",
		Port:     "port-1",
		Mode:     Include,
		Sources:  []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	includeView := table.AllowedSources(testGroup, IPv4)
	if len(includeView) != 1 || includeView[0].Prefix.String() != "192.0.2.0/30" {
		t.Fatalf("include view = %+v", includeView)
	}

	newToken, err := table.Join(Registration{
		Group:    testGroup,
		Tenant:   "tenant-a",
		Port:     "port-1",
		Mode:     Exclude,
		Sources:  []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("mode switch join: %v", err)
	}

	excludeView := table.AllowedSources(testGroup, IPv4)
	wantExclude := []string{"192.0.2.4/30", "192.0.2.8/29"}
	if len(excludeView) != len(wantExclude) {
		t.Fatalf("exclude view = %+v, want %v", excludeView, wantExclude)
	}
	for i, want := range wantExclude {
		if excludeView[i].Prefix.String() != want {
			t.Fatalf("exclude view[%d]=%s, want %s", i, excludeView[i].Prefix, want)
		}
		if len(excludeView[i].Authorizers) != 1 {
			t.Fatalf("authorizers = %+v", excludeView[i].Authorizers)
		}
	}
	for _, block := range excludeView {
		auth := block.Authorizers[0]
		if auth.Mode != Exclude || len(auth.ExcludePrefixes) != 1 ||
			auth.ExcludePrefixes[0].String() != "192.0.2.0/30" {
			t.Fatalf("bad exclude evidence: %+v", auth)
		}
	}

	if _, err := table.Renew(old, time.Minute); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("pre-switch token err=%v, want ErrUnknownLease", err)
	}

	permit, ok := table.MemberPermit(key, IPv4)
	if !ok || len(permit) != 2 ||
		permit[0].String() != "192.0.2.4/30" || permit[1].String() != "192.0.2.8/29" {
		t.Fatalf("member permit after switch = %v ok=%v", permit, ok)
	}

	clock.Advance(2 * time.Minute)
	if _, err := table.Renew(newToken, time.Minute); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("expired token err=%v, want ErrUnknownLease", err)
	}
}
