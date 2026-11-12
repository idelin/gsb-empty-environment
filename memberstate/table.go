// Package memberstate 维护虚拟交换机组播组的成员源过滤状态。
//
// 每个成员（可信租户 + 端口实例 + 组）独立声明 INCLUDE 或 EXCLUDE 源
// 集合；数据面查询时，组的最终放行集合是各成员“许可集合”的并集：
//   - INCLUDE 成员的许可集合即其包含前缀集合；
//   - EXCLUDE 成员的许可集合即全集中排除其排除前缀后的补集。
//
// 源集合一律以 IPv4/IPv6 CIDR 前缀表达，内部使用二进制基数 trie 做
// 集合代数，绝不展开为单地址。成员状态通过写时复制快照 + 原子指针
// 发布，模式切换对读侧原子可见；成员离开或租约到期只移除该成员自身
// 的贡献，不影响其他成员。
package memberstate

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// IPFamily 标识地址族。
type IPFamily uint8

const (
	IPv4 IPFamily = 4
	IPv6 IPFamily = 6
)

// FilterMode 是成员的源过滤模式，语义对齐 IGMPv3/MLDv2。
type FilterMode int

const (
	// Include 表示仅放行 Sources 中的源。
	Include FilterMode = iota
	// Exclude 表示放行除 Sources 之外的全部源。
	Exclude
)

// GroupKey 标识一个组播组（叠加 VLAN 隔离）。
type GroupKey struct {
	VLAN uint16
	Addr netip.Addr
}

// MemberKey 标识一个组成员：可信租户与端口实例在组内的绑定。
type MemberKey struct {
	Group  GroupKey
	Tenant TenantID
	Port   PortID
}

// TenantID 是经过控制面认证的可信租户标识。
type TenantID string

// PortID 是交换机端口实例标识。
type PortID string

// Registration 是成员加入或原子切换模式的请求。Sources 必须同族；
// 同族内每个前缀代表一个源网段，禁止以 /32 或 /128 枚举单地址来表达
// 网段语义。LeaseTTL <= 0 表示长期成员（无租约）。
type Registration struct {
	Group    GroupKey
	Tenant   TenantID
	Port     PortID
	Mode     FilterMode
	Sources  []netip.Prefix
	LeaseTTL time.Duration
}

// LeaseToken 是租约句柄，代次（generation）使旧令牌在成员离开、到期
// 或重新加入后立即失效，用于拒绝迟到续期。LeaseToken 不透明，调用方
// 只能持有而不能自行构造有效句柄。
type LeaseToken struct {
	group      GroupKey
	tenant     TenantID
	port       PortID
	generation uint64
}

// Group 返回令牌绑定的组。
func (t LeaseToken) Group() GroupKey { return t.group }

// Tenant 返回令牌绑定的可信租户。
func (t LeaseToken) Tenant() TenantID { return t.tenant }

// Port 返回令牌绑定的端口实例。
func (t LeaseToken) Port() PortID { return t.port }

// 常见错误。
var (
	// ErrInvalidRegistration 表示成员注册请求字段不合法。
	ErrInvalidRegistration = errors.New("memberstate: invalid member registration")
	// ErrInvalidPrefix 表示源前缀非法、越界或地址族不受支持。
	ErrInvalidPrefix = errors.New("memberstate: invalid source prefix")
	// ErrFamilyMismatch 表示一次注册混用了 IPv4 与 IPv6 前缀。
	ErrFamilyMismatch = errors.New("memberstate: mixed IPv4/IPv6 sources in one registration")
	// ErrUnknownLease 表示租约不存在、已到期、已被离开撤销，或令牌代次
	// 已过期（迟到续期/迟到离开）。
	ErrUnknownLease = errors.New("memberstate: unknown or superseded lease token")
)

// Config 构造成员表。两个全集前缀分别限定 IPv4/IPv6 的源空间；
// Clock 用于可测试的租约判定，为 nil 时使用 time.Now。
type Config struct {
	IPv4Universe netip.Prefix
	IPv6Universe netip.Prefix
	Clock        func() time.Time
}

// Table 是并发安全的组成员状态表。零值不可用，须经 NewTable 构造。
type Table struct {
	mu           sync.Mutex
	state        atomic.Pointer[snapshot]
	clock        func() time.Time
	ipv4Universe netip.Prefix
	ipv6Universe netip.Prefix
}

type snapshot struct {
	members        map[MemberKey]*member
	nextGeneration uint64
}

type member struct {
	mode       FilterMode
	sets       map[IPFamily]*PrefixSet
	expiry     time.Time
	generation uint64
}

// NewTable 构造成员表并校验全集前缀。
func NewTable(cfg Config) (*Table, error) {
	v4, err := checkPrefix(cfg.IPv4Universe)
	if err != nil {
		return nil, ErrInvalidPrefix
	}
	if v4.Addr().BitLen() != 32 {
		return nil, ErrInvalidPrefix
	}
	v6, err := checkPrefix(cfg.IPv6Universe)
	if err != nil {
		return nil, ErrInvalidPrefix
	}
	if v6.Addr().BitLen() != 128 {
		return nil, ErrInvalidPrefix
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	t := &Table{
		clock:        clock,
		ipv4Universe: v4,
		ipv6Universe: v6,
	}
	t.state.Store(&snapshot{members: map[MemberKey]*member{}, nextGeneration: 1})
	return t, nil
}

func (t *Table) load() *snapshot { return t.state.Load() }

// activeSnapshot 返回已清理到期租约的最新快照。读路径本身不持锁，
// 发现可能存在到期成员时通过 mutate 做一次写时复制替换，保证查询永不
// 放行已到期成员；无到期成员时完全无锁、零分配。
func (t *Table) activeSnapshot() *snapshot {
	prev := t.load()
	now := t.clock()
	stale := false
	for _, m := range prev.members {
		if !m.expiry.IsZero() && !m.expiry.After(now) {
			stale = true
			break
		}
	}
	if !stale {
		return prev
	}
	_ = t.mutate(func(snap *snapshot) error { return nil })
	return t.load()
}

// mutate 在写锁内执行写时复制：先清理到期租约，再应用变更，最后原子
// 发布一个全新的快照；读侧始终看到完整的旧状态或完整的新状态。
func (t *Table) mutate(fn func(snap *snapshot) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev := t.load()
	fresh := &snapshot{
		members:        make(map[MemberKey]*member, len(prev.members)),
		nextGeneration: prev.nextGeneration,
	}
	now := t.clock()
	for key, m := range prev.members {
		// 零值表示无租约；非零值时 expiry 不晚于当前时刻即视为到期，
		// 因此“恰好到期”的租约也会被立即撤销。
		if !m.expiry.IsZero() && !m.expiry.After(now) {
			continue
		}
		fresh.members[key] = m
	}
	if err := fn(fresh); err != nil {
		return err
	}
	t.state.Store(fresh)
	return nil
}

// Join 注册成员或对已存在成员做原子模式切换。同一 (组, 租户, 端口)
// 重新 Join 会整体替换模式与源集合作废旧租约代次，并返回新令牌。
func (t *Table) Join(reg Registration) (LeaseToken, error) {
	if !reg.Group.Addr.IsValid() || reg.Tenant == "" || reg.Port == "" {
		return LeaseToken{}, ErrInvalidRegistration
	}
	if reg.Mode != Include && reg.Mode != Exclude {
		return LeaseToken{}, ErrInvalidRegistration
	}

	sets, err := t.buildSets(reg.Sources)
	if err != nil {
		return LeaseToken{}, err
	}

	key := MemberKey{Group: reg.Group, Tenant: reg.Tenant, Port: reg.Port}
	err = t.mutate(func(snap *snapshot) error {
		generation := snap.nextGeneration
		snap.nextGeneration++
		expiry := time.Time{}
		if reg.LeaseTTL > 0 {
			expiry = t.clock().Add(reg.LeaseTTL)
		}
		snap.members[key] = &member{
			mode:       reg.Mode,
			sets:       sets,
			expiry:     expiry,
			generation: generation,
		}
		return nil
	})
	if err != nil {
		return LeaseToken{}, err
	}
	return t.tokenFor(key), nil
}

func (t *Table) buildSets(sources []netip.Prefix) (map[IPFamily]*PrefixSet, error) {
	sets := map[IPFamily]*PrefixSet{}
	var family IPFamily
	for _, raw := range sources {
		p, err := checkPrefix(raw)
		if err != nil {
			return nil, ErrInvalidPrefix
		}
		f := IPv4
		universe := t.ipv4Universe
		if p.Addr().BitLen() == 128 {
			f = IPv6
			universe = t.ipv6Universe
		} else if p.Addr().BitLen() != 32 {
			return nil, ErrInvalidPrefix
		}
		if family == 0 {
			family = f
		} else if family != f {
			return nil, ErrFamilyMismatch
		}
		set, ok := sets[f]
		if !ok {
			set = &PrefixSet{universe: universe, bits: universe.Addr().BitLen()}
			sets[f] = set
		}
		next, err := set.Add(p)
		if err != nil {
			return nil, ErrInvalidPrefix
		}
		sets[f] = next
	}
	return sets, nil
}

func (t *Table) tokenFor(key MemberKey) LeaseToken {
	m := t.load().members[key]
	if m == nil {
		return LeaseToken{}
	}
	return LeaseToken{
		group:      key.Group,
		tenant:     key.Tenant,
		port:       key.Port,
		generation: m.generation,
	}
}

// Renew 用有效令牌续租。租约到期、成员已离开、端口被复用后重新加入
// 等场景下旧令牌代次失效，迟到续期返回 ErrUnknownLease，绝不会把权限
// 重新授予一个已撤销的端口。
func (t *Table) Renew(token LeaseToken, ttl time.Duration) (LeaseToken, error) {
	if ttl <= 0 {
		return LeaseToken{}, ErrInvalidRegistration
	}
	key := memberKeyOf(token)
	err := t.mutate(func(snap *snapshot) error {
		m := snap.members[key]
		if m == nil || m.generation != token.generation {
			return ErrUnknownLease
		}
		m.expiry = t.clock().Add(ttl)
		return nil
	})
	if err != nil {
		return LeaseToken{}, err
	}
	return t.tokenFor(key), nil
}

// Leave 撤销令牌对应成员的全部贡献。代次不匹配（已离开、已到期或已
// 被新注册取代）返回 ErrUnknownLease。
func (t *Table) Leave(token LeaseToken) error {
	key := memberKeyOf(token)
	return t.mutate(func(snap *snapshot) error {
		m := snap.members[key]
		if m == nil || m.generation != token.generation {
			return ErrUnknownLease
		}
		delete(snap.members, key)
		return nil
	})
}

// Sweep 主动清理到期租约。普通读写路径也会惰性清理，本方法供控制面
// 定时维护使用。
func (t *Table) Sweep() {
	_ = t.mutate(func(snap *snapshot) error { return nil })
}

func memberKeyOf(token LeaseToken) MemberKey {
	return MemberKey{
		Group:  token.group,
		Tenant: token.tenant,
		Port:   token.port,
	}
}

func (t *Table) universeFor(family IPFamily) (netip.Prefix, bool) {
	switch family {
	case IPv4:
		return t.ipv4Universe, true
	case IPv6:
		return t.ipv6Universe, true
	default:
		return netip.Prefix{}, false
	}
}

// permitSet 返回单个成员在指定地址族下的许可集合：INCLUDE 为包含集合，
// EXCLUDE 为全集对排除集合的补集。成员未声明某地址族时，在该族没有
// 任何许可（空集合），避免把一个仅声明 IPv4 过滤的成员误当作 IPv6
// 通配授权而向隔离端口泄漏流量。
func (t *Table) permitSet(m *member, family IPFamily) *PrefixSet {
	universe, ok := t.universeFor(family)
	if !ok {
		return nil
	}
	empty := &PrefixSet{universe: universe, bits: universe.Addr().BitLen()}
	if set, ok := m.sets[family]; ok {
		if m.mode == Exclude {
			return set.Complement()
		}
		return set
	}
	return empty
}
