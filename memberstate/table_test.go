package memberstate

import (
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ now atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	// 使用纳秒基准，与 time.Duration 的 Add 保持同一量级。
	c.now.Store(1_000_000_000_000)
	return c
}

func (c *fakeClock) Time() time.Time { return time.Unix(0, c.now.Load()) }

func (c *fakeClock) Advance(d time.Duration) { c.now.Store(c.now.Load() + int64(d)) }

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", s, err)
	}
	return p
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse addr %q: %v", s, err)
	}
	return a
}

func newTestTable(t *testing.T, clock *fakeClock) *Table {
	t.Helper()
	table, err := NewTable(Config{
		IPv4Universe: mustPrefix(t, "192.0.2.0/28"),
		IPv6Universe: mustPrefix(t, "2001:db8::/124"),
		Clock:        clock.Time,
	})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return table
}

var testGroup = GroupKey{VLAN: 7, Addr: netip.MustParseAddr("239.1.2.3")}

// refModel 是测试专用的逐地址参考模型：仅在小全集内枚举每个地址，
// 独立于 trie 实现，用来交叉验证生产代码（生产代码不做地址枚举）。
type refModel struct {
	universe netip.Prefix
	members  map[MemberKey]refMember
}

type refMember struct {
	mode FilterMode
	sets []netip.Prefix
}

func newRefModel(universe netip.Prefix) *refModel {
	return &refModel{universe: universe, members: map[MemberKey]refMember{}}
}

func (r *refModel) put(key MemberKey, mode FilterMode, sets []netip.Prefix) {
	r.members[key] = refMember{mode: mode, sets: sets}
}

func (r *refModel) allowed(addr netip.Addr) (bool, map[MemberKey]bool) {
	authorizers := map[MemberKey]bool{}
	for key, m := range r.members {
		matched := false
		for _, p := range m.sets {
			if p.Contains(addr) {
				matched = true
				break
			}
		}
		permit := m.mode == Include && matched
		if m.mode == Exclude && !matched {
			permit = true
		}
		if permit {
			authorizers[key] = true
		}
	}
	return len(authorizers) > 0, authorizers
}

// enumerateV4 在小全集（IPv4）内逐地址枚举，仅供测试对照。
func enumerateV4(t *testing.T, universe netip.Prefix) []netip.Addr {
	t.Helper()
	base := universe.Addr().As4()
	size := 1 << (32 - universe.Bits())
	out := make([]netip.Addr, 0, size)
	for i := 0; i < size; i++ {
		v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		v += uint32(i)
		out = append(out, netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}))
	}
	return out
}

// TestMixedIncludeExcludeSmallUniverse 用 16 地址的小全集对照混合
// INCLUDE/EXCLUDE 成员的并集语义，并逐地址核对授权成员证据。
func TestMixedIncludeExcludeSmallUniverse(t *testing.T) {
	clock := newFakeClock()
	table := newTestTable(t, clock)
	ref := newRefModel(mustPrefix(t, "192.0.2.0/28"))

	m1 := MemberKey{Group: testGroup, Tenant: "tenant-a", Port: "port-1"}
	m2 := MemberKey{Group: testGroup, Tenant: "tenant-b", Port: "port-2"}
	m3 := MemberKey{Group: testGroup, Tenant: "tenant-c", Port: "port-3"}

	join := func(key MemberKey, mode FilterMode, srcs ...string) {
		t.Helper()
		prefixes := make([]netip.Prefix, 0, len(srcs))
		for _, s := range srcs {
			prefixes = append(prefixes, mustPrefix(t, s))
		}
		if _, err := table.Join(Registration{
			Group: key.Group, Tenant: key.Tenant, Port: key.Port,
			Mode: mode, Sources: prefixes,
		}); err != nil {
			t.Fatalf("join %s/%s: %v", key.Tenant, key.Port, err)
		}
		ref.put(key, mode, prefixes)
	}

	join(m1, Include, "192.0.2.0/30")
	join(m2, Exclude, "192.0.2.8/30")
	join(m3, Include, "192.0.2.2/31")

	got := table.AllowedSources(testGroup, IPv4)
	wantPrefixes := []string{"192.0.2.0/29", "192.0.2.12/30"}
	if len(got) != len(wantPrefixes) {
		t.Fatalf("allowed prefix count = %d, want %d (%v)", len(got), len(wantPrefixes), got)
	}
	for i, want := range wantPrefixes {
		if got[i].Prefix.String() != want {
			t.Errorf("allowed[%d] = %s, want %s", i, got[i].Prefix, want)
		}
	}

	assertDisjointAndMinimal(t, got)

	for _, addr := range enumerateV4(t, ref.universe) {
		refAllowed, refAuthors := ref.allowed(addr)
		var hit *AllowedPrefix
		for i := range got {
			if got[i].Prefix.Contains(addr) {
				hit = &got[i]
				break
			}
		}
		if refAllowed != (hit != nil) {
			t.Fatalf("addr %s allowed=%v, ref=%v", addr, hit != nil, refAllowed)
		}
		if !refAllowed {
			continue
		}
		gotAuthors := map[MemberKey]bool{}
		for _, auth := range hit.Authorizers {
			gotAuthors[MemberKey{Group: testGroup, Tenant: auth.Tenant, Port: auth.Port}] = true
		}
		// 块级证据：每个证据成员都必须在参考模型中许可该块内至少一个
		// 地址（即与块相交），且所有相交成员都必须留证；INCLUDE 成员
		// 的具体授权切片由 IncludePrefixes 精确表达。
		blockRef := map[MemberKey]bool{}
		for blockAddr := range blockAddresses(t, hit.Prefix) {
			_, authors := ref.allowed(blockAddr)
			for key := range authors {
				blockRef[key] = true
			}
		}
		if len(gotAuthors) != len(blockRef) {
			t.Fatalf("addr %s block authorizers=%v, blockRef=%v (per-addr ref=%v)",
				addr, gotAuthors, blockRef, refAuthors)
		}
		for key := range blockRef {
			if !gotAuthors[key] {
				t.Fatalf("addr %s missing block authorizer %v", addr, key)
			}
		}
		for _, auth := range hit.Authorizers {
			key := MemberKey{Group: testGroup, Tenant: auth.Tenant, Port: auth.Port}
			verifyEvidence(t, ref.members[key], hit.Prefix, auth)
		}
	}
}

func blockAddresses(t *testing.T, block netip.Prefix) map[netip.Addr]struct{} {
	t.Helper()
	out := map[netip.Addr]struct{}{}
	for _, addr := range enumerateV4(t, block) {
		out[addr] = struct{}{}
	}
	return out
}

func verifyEvidence(t *testing.T, m refMember, block netip.Prefix, auth Authorizer) {
	t.Helper()
	if m.mode != auth.Mode {
		t.Fatalf("evidence mode mismatch for %s/%s", auth.Tenant, auth.Port)
	}
	switch m.mode {
	case Include:
		var want []netip.Prefix
		for _, p := range m.sets {
			if p.Overlaps(block) {
				want = append(want, p)
			}
		}
		if !prefixesEqual(want, auth.IncludePrefixes) {
			t.Fatalf("INCLUDE evidence=%v, want %v", auth.IncludePrefixes, want)
		}
	case Exclude:
		if !prefixesEqual(m.sets, auth.ExcludePrefixes) {
			t.Fatalf("EXCLUDE evidence=%v, want %v", auth.ExcludePrefixes, m.sets)
		}
		for _, excluded := range auth.ExcludePrefixes {
			if excluded.Overlaps(block) {
				t.Fatalf("block %s overlaps excluded prefix %s", block, excluded)
			}
		}
	}
}

func prefixesEqual(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]int{}
	for _, p := range a {
		set[p.String()]++
	}
	for _, p := range b {
		set[p.String()]--
	}
	for _, n := range set {
		if n != 0 {
			return false
		}
	}
	return true
}

func assertDisjointAndMinimal(t *testing.T, got []AllowedPrefix) {
	t.Helper()
	for i := range got {
		for j := i + 1; j < len(got); j++ {
			if got[i].Prefix.Overlaps(got[j].Prefix) {
				t.Fatalf("prefixes overlap: %s vs %s", got[i].Prefix, got[j].Prefix)
			}
		}
	}
	prefixes := make([]netip.Prefix, len(got))
	for i, g := range got {
		prefixes[i] = g.Prefix
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			parent, ok := mergeableParent(prefixes[i], prefixes[j])
			if !ok {
				continue
			}
			covered := 0
			for _, p := range prefixes {
				if parent.Overlaps(p) {
					covered++
				}
			}
			if covered == 2 {
				t.Fatalf("prefixes %s and %s could merge into %s", prefixes[i], prefixes[j], parent)
			}
		}
	}
}

func mergeableParent(a, b netip.Prefix) (netip.Prefix, bool) {
	if a.Bits() != b.Bits() {
		return netip.Prefix{}, false
	}
	ab, bb := a.Addr().AsSlice(), b.Addr().AsSlice()
	firstDiff := -1
	for i := 0; i < len(ab); i++ {
		if xor := ab[i] ^ bb[i]; xor != 0 {
			firstDiff = i*8 + leadingZeros8(xor)
			break
		}
	}
	if firstDiff != a.Bits()-1 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a.Addr(), a.Bits()-1), true
}

func leadingZeros8(b byte) int {
	for i := 0; i < 8; i++ {
		if b&(1<<(7-i)) != 0 {
			return i
		}
	}
	return 8
}
