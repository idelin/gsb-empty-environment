package memberstate

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// TestIPv6SmallUniverse 验证 IPv6 前缀集合与补集同样不展开地址。
func TestIPv6SmallUniverse(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-a",
		Port:    "port-1",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "2001:db8::/126")},
	}); err != nil {
		t.Fatalf("v6 join: %v", err)
	}
	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-b",
		Port:    "port-2",
		Mode:    Exclude,
		Sources: []netip.Prefix{mustPrefix(t, "2001:db8::4/126")},
	}); err != nil {
		t.Fatalf("v6 exclude join: %v", err)
	}

	got := table.AllowedSources(testGroup, IPv6)
	want := []string{"2001:db8::/126", "2001:db8::8/125"}
	if len(got) != len(want) {
		t.Fatalf("v6 coverage = %+v, want %v", got, want)
	}
	for i, p := range want {
		if got[i].Prefix.String() != p {
			t.Fatalf("v6 prefix[%d]=%s, want %s", i, got[i].Prefix, p)
		}
	}
	assertDisjointAndMinimal(t, got)

	if got := table.AllowedSources(testGroup, IPv4); len(got) != 0 {
		t.Fatalf("v4 query must be empty for v6-only members, got %v", got)
	}
}

// TestInvalidInputs 校验注册与前缀的边界错误。
func TestInvalidInputs(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	if _, err := table.Join(Registration{
		Group:   GroupKey{VLAN: 1},
		Tenant:  "t",
		Port:    "p",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "192.0.2.0/30")},
	}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("missing group addr err=%v", err)
	}

	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "t",
		Port:    "p",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "203.0.113.0/24")},
	}); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("out-of-universe err=%v", err)
	}

	if _, err := table.Join(Registration{
		Group:  testGroup,
		Tenant: "t", Port: "p",
		Mode: Include,
		Sources: []netip.Prefix{
			mustPrefix(t, "192.0.2.0/30"),
			mustPrefix(t, "2001:db8::/126"),
		},
	}); !errors.Is(err, ErrFamilyMismatch) {
		t.Fatalf("family mix err=%v", err)
	}

	if _, err := table.Renew(LeaseToken{}, time.Minute); !errors.Is(err, ErrUnknownLease) {
		t.Fatalf("zero token err=%v", err)
	}

	if _, err := NewTable(Config{
		IPv4Universe: mustPrefix(t, "2001:db8::/124"),
		IPv6Universe: mustPrefix(t, "2001:db8::/124"),
	}); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("wrong-family universe err=%v", err)
	}
}

// TestConcurrentAtomicRead 在 -race 下并发读写，验证快照发布安全性。
func TestConcurrentAtomicRead(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = table.Join(Registration{
						Group:  testGroup,
						Tenant: TenantID("tenant-" + string(rune('a'+i%3))),
						Port:   PortID("port-" + string(rune('0'+i))),
						Mode:   FilterMode(i % 2),
						Sources: []netip.Prefix{
							mustPrefix(t, "192.0.2.0/30"),
						},
					})
				}
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = table.AllowedSources(testGroup, IPv4)
				_ = table.Members(testGroup)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestEmptyTableAndWildcardExclude 覆盖空表与无租约/通配排除语义。
func TestEmptyTableAndWildcardExclude(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	if got := table.AllowedSources(testGroup, IPv4); got != nil {
		t.Fatalf("empty table should return nil, got %v", got)
	}

	if _, err := table.Join(Registration{
		Group:    testGroup,
		Tenant:   "tenant-a",
		Port:     "port-1",
		Mode:     Exclude,
		Sources:  []netip.Prefix{mustPrefix(t, "192.0.2.8/29")},
		LeaseTTL: time.Minute,
	}); err != nil {
		t.Fatalf("join: %v", err)
	}

	members := table.Members(testGroup)
	if len(members) != 1 || members[0].Expiry.IsZero() {
		t.Fatalf("members = %+v", members)
	}

	// 该成员仅声明 IPv4 过滤：未声明的 IPv6 族不得产生任何通配许可。
	v6 := table.AllowedSources(testGroup, IPv6)
	if len(v6) != 0 {
		t.Fatalf("undeclared family must have no permit, got %+v", v6)
	}

	clock.Advance(time.Minute + time.Second)
	table.Sweep()
	if members := table.Members(testGroup); len(members) != 0 {
		t.Fatalf("sweep left expired member: %+v", members)
	}
	if got := table.AllowedSources(testGroup, IPv4); len(got) != 0 {
		t.Fatalf("sweep did not revoke contribution: %+v", got)
	}
}

// TestPrefixSetAlgebra 直接验证 trie 集合代数：并/交/差/补及不相交输出。
func TestPrefixSetAlgebra(t *testing.T) {
	universe := mustPrefix(t, "192.0.2.0/28")
	set, err := NewPrefixSet(universe)
	if err != nil {
		t.Fatalf("new set: %v", err)
	}
	a, err := set.Add(mustPrefix(t, "192.0.2.0/30"))
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	b, err := set.Add(mustPrefix(t, "192.0.2.4/30"))
	if err != nil {
		t.Fatalf("add b: %v", err)
	}

	if got := a.Union(b).Prefixes(); len(got) != 1 || got[0].String() != "192.0.2.0/29" {
		t.Fatalf("union = %v", got)
	}
	if got := a.Complement().Prefixes(); len(got) != 2 ||
		got[0].String() != "192.0.2.4/30" || got[1].String() != "192.0.2.8/29" {
		t.Fatalf("complement = %v", got)
	}
	if got := a.Complement().Intersect(b).Prefixes(); len(got) != 1 ||
		got[0].String() != "192.0.2.4/30" {
		t.Fatalf("intersect = %v", got)
	}
	if got := a.Union(b).Difference(b).Prefixes(); len(got) != 1 ||
		got[0].String() != "192.0.2.0/30" {
		t.Fatalf("difference = %v", got)
	}

	// 原集合不可变，Add 返回的是新集合。
	if len(set.Prefixes()) != 0 {
		t.Fatalf("base set mutated: %v", set.Prefixes())
	}
	if !a.Contains(mustAddr(t, "192.0.2.1")) {
		t.Fatal("Contains missed an in-set address")
	}
	if a.Contains(mustAddr(t, "192.0.2.5")) {
		t.Fatal("Contains matched an out-of-set address")
	}
	if !a.Union(b).Covers(mustPrefix(t, "192.0.2.0/29")) {
		t.Fatal("Covers failed on fully covered prefix")
	}

	// 全集之外的地址永远不被包含。
	if a.Contains(mustAddr(t, "198.51.100.1")) {
		t.Fatal("address outside universe must not be contained")
	}

	if _, err := set.Add(mustPrefix(t, "203.0.113.0/24")); err == nil {
		t.Fatal("expected error for prefix outside universe")
	}

	hits := b.IntersectingPrefixes(mustPrefix(t, "192.0.2.0/29"))
	if len(hits) != 1 || hits[0].String() != "192.0.2.4/30" {
		t.Fatalf("intersecting prefixes = %v", hits)
	}
}

// TestPrefixSetNoSingleAddressExpansion 保证对外输出始终是前缀，且覆盖
// 等价于小全集逐地址判定（参考枚举只存在于测试中）。
func TestPrefixSetNoSingleAddressExpansion(t *testing.T) {
	universe := mustPrefix(t, "192.0.2.0/28")
	set, err := NewPrefixSet(universe)
	if err != nil {
		t.Fatalf("new set: %v", err)
	}
	for _, p := range []string{"192.0.2.0/30", "192.0.2.8/30"} {
		set, err = set.Add(mustPrefix(t, p))
		if err != nil {
			t.Fatalf("add %s: %v", p, err)
		}
	}

	prefixes := set.Prefixes()
	for _, p := range prefixes {
		if p.Bits() == 32 {
			t.Fatalf("output must stay prefix-encoded, got single address %s", p)
		}
	}

	allowed := map[netip.Addr]bool{}
	for _, p := range prefixes {
		for _, addr := range enumerateV4(t, universe) {
			if p.Contains(addr) {
				allowed[addr] = true
			}
		}
	}
	range0 := mustPrefix(t, "192.0.2.0/30")
	range1 := mustPrefix(t, "192.0.2.8/30")
	for _, addr := range enumerateV4(t, universe) {
		want := range0.Contains(addr) || range1.Contains(addr)
		if allowed[addr] != want || set.Contains(addr) != want {
			t.Fatalf("addr %s allowed=%v trie=%v want=%v", addr, allowed[addr], set.Contains(addr), want)
		}
	}
}

// TestRootUniverseAndDeepPrefix 验证全集前缀、主机级前缀与补集的组合
// 仍然输出不相交前缀，且深度边界（/32、/128 的父层）不会越界。
func TestRootUniverseAndDeepPrefix(t *testing.T) {
	set, err := NewPrefixSet(mustPrefix(t, "192.0.2.0/28"))
	if err != nil {
		t.Fatalf("new set: %v", err)
	}

	// 单主机前缀用于表达特定授权源（集合内部仍是前缀节点，不做枚举）。
	host := mustPrefix(t, "192.0.2.15/32")
	withHost, err := set.Add(host)
	if err != nil {
		t.Fatalf("add /32: %v", err)
	}
	if got := withHost.Prefixes(); len(got) != 1 || got[0].String() != "192.0.2.15/32" {
		t.Fatalf("host prefix = %v", got)
	}

	// 补集必须覆盖除该主机外的全部全集，且不与主机前缀相交。
	rest := withHost.Complement()
	for _, p := range rest.Prefixes() {
		if p.Overlaps(host) {
			t.Fatalf("complement leaks host space via %s", p)
		}
	}
	joined := rest.Union(withHost)
	if got := joined.Prefixes(); len(got) != 1 || got[0].String() != "192.0.2.0/28" {
		t.Fatalf("re-union = %v, want full universe", got)
	}

	// 空集合补集即整个全集。
	if got := set.Complement().Prefixes(); len(got) != 1 ||
		got[0].String() != "192.0.2.0/28" {
		t.Fatalf("empty complement = %v", got)
	}
}

// TestExcludeEvidenceSlices 验证 EXCLUDE 成员对合并块给出的证据前缀
// 与放行块不相交，INCLUDE 成员给出精确相交切片。
func TestExcludeEvidenceSlices(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)

	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-a",
		Port:    "port-1",
		Mode:    Include,
		Sources: []netip.Prefix{mustPrefix(t, "192.0.2.1/32")},
	}); err != nil {
		t.Fatalf("include join: %v", err)
	}
	if _, err := table.Join(Registration{
		Group:   testGroup,
		Tenant:  "tenant-b",
		Port:    "port-2",
		Mode:    Exclude,
		Sources: []netip.Prefix{mustPrefix(t, "192.0.2.8/29")},
	}); err != nil {
		t.Fatalf("exclude join: %v", err)
	}

	for _, block := range table.AllowedSources(testGroup, IPv4) {
		for _, auth := range block.Authorizers {
			switch auth.Mode {
			case Include:
				for _, p := range auth.IncludePrefixes {
					if !p.Overlaps(block.Prefix) {
						t.Fatalf("include evidence %s not inside block %s", p, block.Prefix)
					}
				}
			case Exclude:
				for _, p := range auth.ExcludePrefixes {
					if p.Overlaps(block.Prefix) {
						t.Fatalf("block %s overlaps excluded %s", block.Prefix, p)
					}
				}
			}
		}
	}
}
