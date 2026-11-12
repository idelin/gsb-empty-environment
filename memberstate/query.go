package memberstate

import (
	"net/netip"
	"time"
)

// AllowedPrefix 是一块精确、不相交的放行前缀及其授权成员证据。
type AllowedPrefix struct {
	Prefix      netip.Prefix
	Authorizers []Authorizer
}

// Authorizer 说明某成员为何授权某块放行前缀。
type Authorizer struct {
	Tenant TenantID
	Port   PortID
	Mode   FilterMode
	// IncludePrefixes 是 INCLUDE 模式下与本放行前缀相交的包含前缀。
	IncludePrefixes []netip.Prefix
	// ExcludePrefixes 是 EXCLUDE 模式下成员配置的排除前缀；放行前缀
	// 与其中任何前缀都不相交，可据此复核“源不在排除集合内”。
	ExcludePrefixes []netip.Prefix
}

// MemberStatus 是成员的只读状态快照。
type MemberStatus struct {
	Key  MemberKey
	Mode FilterMode
	// Sources 按地址族给出成员声明的包含/排除前缀。
	Sources map[IPFamily][]netip.Prefix
	// Expiry 为零值表示无租约，否则为到期时刻。
	Expiry time.Time
}

// AllowedSources 返回组在指定地址族下的放行覆盖：一组最小、互不相交的
// 前缀，每块前缀附带所有贡献授权的成员证据。无成员或无许可时返回空。
//
// 合并规则严格为各成员许可集合的并集：INCLUDE 成员只贡献其包含前缀，
// EXCLUDE 成员贡献全集减排除集合。排除集合不会被跨成员错误合并，因此
// 不会把某个成员本应排除的源泄漏给隔离端口。
func (t *Table) AllowedSources(group GroupKey, family IPFamily) []AllowedPrefix {
	universe, ok := t.universeFor(family)
	if !ok {
		return nil
	}
	snap := t.activeSnapshot()

	var entries []entryView
	merged := &PrefixSet{universe: universe, bits: universe.Addr().BitLen()}
	for key, m := range snap.members {
		if key.Group != group {
			continue
		}
		permit := t.permitSet(m, family)
		entries = append(entries, entryView{key: key, member: m, permit: permit})
		merged = merged.Union(permit)
	}
	sortMembers(entries)

	var result []AllowedPrefix
	for _, prefix := range merged.Prefixes() {
		allowed := AllowedPrefix{Prefix: prefix}
		for _, e := range entries {
			// 许可集合与输出块相交即为授权者；相交的具体子前缀由
			// IncludePrefixes/ExcludePrefixes 精确给出，因此合并块也能
			// 完整保留每个成员的授权证据，不丢失部分覆盖的贡献。
			if len(e.permit.IntersectingPrefixes(prefix)) == 0 {
				continue
			}
			authorizer := Authorizer{Tenant: e.key.Tenant, Port: e.key.Port, Mode: e.member.mode}
			if e.member.mode == Include {
				if set, ok := e.member.sets[family]; ok {
					authorizer.IncludePrefixes = set.IntersectingPrefixes(prefix)
				}
			} else {
				if set, ok := e.member.sets[family]; ok {
					authorizer.ExcludePrefixes = set.Prefixes()
				}
			}
			allowed.Authorizers = append(allowed.Authorizers, authorizer)
		}
		result = append(result, allowed)
	}
	return result
}

// MemberPermit 返回单个成员当前在指定地址族下的许可前缀（最小不相交
// 表示）。供数据面做端口级隔离复核；成员不存在时返回 false。
func (t *Table) MemberPermit(key MemberKey, family IPFamily) ([]netip.Prefix, bool) {
	m, ok := t.activeSnapshot().members[key]
	if !ok {
		return nil, false
	}
	return t.permitSet(m, family).Prefixes(), true
}

// Members 返回组内当前有效成员（已到期租约不计入），按租户与端口排序。
func (t *Table) Members(group GroupKey) []MemberStatus {
	snap := t.activeSnapshot()
	var statuses []MemberStatus
	for key, m := range snap.members {
		if key.Group != group {
			continue
		}
		status := MemberStatus{
			Key:     key,
			Mode:    m.mode,
			Sources: map[IPFamily][]netip.Prefix{},
			Expiry:  m.expiry,
		}
		for family, set := range m.sets {
			status.Sources[family] = set.Prefixes()
		}
		statuses = append(statuses, status)
	}
	sortStatuses(statuses)
	return statuses
}

type entryView struct {
	key    MemberKey
	member *member
	permit *PrefixSet
}

func sortMembers(entries []entryView) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && memberKeyLess(entries[j].key, entries[j-1].key); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func sortStatuses(statuses []MemberStatus) {
	for i := 1; i < len(statuses); i++ {
		for j := i; j > 0 && memberKeyLess(statuses[j].Key, statuses[j-1].Key); j-- {
			statuses[j], statuses[j-1] = statuses[j-1], statuses[j]
		}
	}
}

func memberKeyLess(a, b MemberKey) bool {
	if a.Group.VLAN != b.Group.VLAN {
		return a.Group.VLAN < b.Group.VLAN
	}
	if cmp := compareAddr(a.Group.Addr, b.Group.Addr); cmp != 0 {
		return cmp < 0
	}
	if a.Tenant != b.Tenant {
		return a.Tenant < b.Tenant
	}
	return a.Port < b.Port
}

func compareAddr(a, b netip.Addr) int {
	ab, bb := a.AsSlice(), b.AsSlice()
	for i := 0; i < len(ab) && i < len(bb); i++ {
		if ab[i] != bb[i] {
			if ab[i] < bb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ab) < len(bb):
		return -1
	case len(ab) > len(bb):
		return 1
	default:
		return 0
	}
}
