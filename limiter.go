package netsec

import (
	"errors"
	"net/netip"
	"sync"
	"time"
)

// NATType 描述一个代理或客户端的 NAT 类型。
type NATType int

const (
	// NATUnknown 表示没有任何可用的 NAT 信息，按非限制型处理。
	NATUnknown NATType = iota
	// NATUnrestricted 表示非限制型 NAT（或无 NAT 的公网地址）。
	NATUnrestricted
	// NATRestricted 表示限制型 NAT（对称 NAT、受限锥等）。
	NATRestricted
)

const (
	// DefaultWindow 是前缀限额的滚动时间窗口。
	DefaultWindow = time.Hour
	// DefaultMaxClients 是单个前缀在一个窗口内最多收到的不同客户端数。
	DefaultMaxClients = 20

	ipv4PrefixBits = 24
	ipv6PrefixBits = 48
)

// ErrInvalidOffer 在 offer 缺少标识或携带无效地址时返回。
var ErrInvalidOffer = errors.New("netsec: invalid offer")

// ErrInvalidProxy 在代理缺少标识或携带无效地址时返回。
var ErrInvalidProxy = errors.New("netsec: invalid proxy")

// Offer 是一个等待匹配给代理的客户端 offer。
type Offer struct {
	// ID 是 offer 的唯一标识，用于保证每个 offer 最多交给一个代理。
	ID string
	// ClientID 是客户端的稳定标识，用于“不同客户端”去重计数。
	ClientID string
	// Addr 是客户端来源地址，仅用于校验其合法性。
	Addr netip.Addr
	// Restricted 表示客户端是否处于限制型 NAT 之后。
	Restricted bool
}

// Proxy 是一次代理轮询请求。
type Proxy struct {
	// ID 是代理标识，允许同一个网络前缀频繁更换。
	ID string
	// Addr 是代理轮询请求的来源地址，限额按其网络前缀聚合。
	Addr netip.Addr
	// SelfNAT 是代理自报的 NAT 类型。
	SelfNAT NATType
}

// probe 记录对某个代理最近一次的 NAT 探测结果。
type probe struct {
	nat       NATType
	at        time.Time
	hasResult bool
}

// prefixState 保存一个网络前缀的滚动窗口计数状态。
type prefixState struct {
	clients map[string]time.Time
}

// Limiter 是经纪服务的限额与匹配引擎。
//
// 它保证：
//   - 每个代理来源前缀（IPv4 /24 或 IPv6 /48）在滚动一小时内最多收到
//     DefaultMaxClients 个不同客户端的 offer，与代理标识是否轮换无关；
//   - 同一客户端对同一前缀的重复投递只计一次；
//   - 限制型 NAT 的客户端只会匹配到非限制型 NAT 的代理，且代理自报
//     NAT 与最近探测结果冲突时以探测结果为准；
//   - 每个 offer 最多交给一个代理；
//   - 达到限额的前缀在匹配时被跳过，不返回错误。
type Limiter struct {
	mu        sync.Mutex
	window    time.Duration
	maxClient int
	now       func() time.Time

	prefixes map[string]*prefixState
	probes   map[string]probe
	queue    []*Offer
	assigned map[string]bool
}

// Option 配置 Limiter。
type Option func(*Limiter)

// WithWindow 覆盖默认的滚动窗口长度。
func WithWindow(d time.Duration) Option {
	return func(l *Limiter) { l.window = d }
}

// WithMaxClients 覆盖默认的每窗口不同客户端限额。
func WithMaxClients(n int) Option {
	return func(l *Limiter) { l.maxClient = n }
}

// withClock 注入时钟，仅用于测试。
func withClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// NewLimiter 创建一个就绪的限额引擎。
func NewLimiter(opts ...Option) *Limiter {
	l := &Limiter{
		window:    DefaultWindow,
		maxClient: DefaultMaxClients,
		now:       time.Now,
		prefixes:  make(map[string]*prefixState),
		probes:    make(map[string]probe),
		assigned:  make(map[string]bool),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Enqueue 接收一个客户端 offer 等待后续代理轮询匹配。
// 重复入队同一个 Offer.ID 是幂等的，不会重复计数或重复分配。
func (l *Limiter) Enqueue(o Offer) error {
	if o.ID == "" || o.ClientID == "" || !o.Addr.IsValid() {
		return ErrInvalidOffer
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.assigned[o.ID] {
		return nil
	}
	for _, existing := range l.queue {
		if existing.ID == o.ID {
			return nil
		}
	}
	offer := o
	l.queue = append(l.queue, &offer)
	return nil
}

// Poll 处理一次代理轮询，返回匹配给该代理的 offer；没有可匹配的
// offer 时返回 (nil, nil)。达到限额的前缀会被静默跳过，不构成错误。
func (l *Limiter) Poll(p Proxy) (*Offer, error) {
	if p.ID == "" || !p.Addr.IsValid() {
		return nil, ErrInvalidProxy
	}
	pfx, err := prefixOf(p.Addr)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	state := l.pruneLocked(pfx, now)

	for idx, offer := range l.queue {
		if offer.Restricted && l.effectiveNATLocked(p) == NATRestricted {
			continue
		}
		if !l.admitLocked(state, offer.ClientID, now) {
			continue
		}
		l.queue = append(l.queue[:idx], l.queue[idx+1:]...)
		l.assigned[offer.ID] = true
		matched := *offer
		return &matched, nil
	}
	return nil, nil
}

// RecordProbe 记录对某代理最近一次的 NAT 探测结果。探测结果一旦存在，
// 即覆盖该代理自报的 NAT 类型；更新的探测会覆盖更旧的结果。
func (l *Limiter) RecordProbe(proxyID string, nat NATType, at time.Time) {
	if proxyID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, ok := l.probes[proxyID]
	if ok && prev.hasResult && at.Before(prev.at) {
		return
	}
	l.probes[proxyID] = probe{nat: nat, at: at, hasResult: true}
}

// effectiveNATLocked 返回代理的有效 NAT 类型：存在不冲突要求时，
// 最近一次探测结果优先于代理自报值；没有探测结果时采用自报值，
// 自报值也缺失（NATUnknown）时按非限制型处理。
func (l *Limiter) effectiveNATLocked(p Proxy) NATType {
	if result, ok := l.probes[p.ID]; ok && result.hasResult {
		return result.nat
	}
	if p.SelfNAT == NATUnknown {
		return NATUnrestricted
	}
	return p.SelfNAT
}

// admitLocked 判断客户端 clientID 此刻能否占用前缀的名额；可以占用时
// 同时记录/刷新该客户端的时间戳。已在窗口内的客户端重复出现不新增名额。
func (l *Limiter) admitLocked(state *prefixState, clientID string, now time.Time) bool {
	if _, seen := state.clients[clientID]; !seen && len(state.clients) >= l.maxClient {
		return false
	}
	state.clients[clientID] = now
	return true
}

// pruneLocked 清理指定前缀上已滑出窗口的客户端记录并返回其状态。
// 空状态不在此删除，以免当次轮询刚写入的计数在同一临界区被回收；
// 完全空闲前缀的回收由 Sweep 负责。
func (l *Limiter) pruneLocked(pfx string, now time.Time) *prefixState {
	state, ok := l.prefixes[pfx]
	if !ok {
		state = &prefixState{clients: make(map[string]time.Time)}
		l.prefixes[pfx] = state
		return state
	}
	cutoff := now.Add(-l.window)
	for clientID, lastSeen := range state.clients {
		if !lastSeen.After(cutoff) {
			delete(state.clients, clientID)
		}
	}
	return state
}

// PrefixStats 是一个前缀的可观测计数快照。
type PrefixStats struct {
	Prefix     string
	Active     int
	Limit      int
	Window     time.Duration
	QueueDepth int
}

// Stats 返回当前各活跃前缀的占用快照，供监控使用。
func (l *Limiter) Stats() []PrefixStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	out := make([]PrefixStats, 0, len(l.prefixes))
	for pfx, state := range l.prefixes {
		active := 0
		cutoff := now.Add(-l.window)
		for _, lastSeen := range state.clients {
			if lastSeen.After(cutoff) {
				active++
			}
		}
		if active == 0 {
			continue
		}
		out = append(out, PrefixStats{
			Prefix:     pfx,
			Active:     active,
			Limit:      l.maxClient,
			Window:     l.window,
			QueueDepth: len(l.queue),
		})
	}
	return out
}

// Sweep 回收所有客户端均已滑出窗口的前缀状态。经纪服务可周期性调用，
// 防止攻击者用海量不同前缀制造空状态导致 map 无限增长。
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	for pfx, state := range l.prefixes {
		for clientID, lastSeen := range state.clients {
			if !lastSeen.After(cutoff) {
				delete(state.clients, clientID)
			}
		}
		if len(state.clients) == 0 {
			delete(l.prefixes, pfx)
		}
	}
}

// prefixOf 把地址归一化为限额前缀键：IPv4 取 /24，IPv6 取 /48。
// IPv4 映射的 IPv6 地址先还原为 IPv4，避免同一主机产生两个前缀键。
func prefixOf(addr netip.Addr) (string, error) {
	if !addr.IsValid() {
		return "", ErrInvalidProxy
	}
	addr = addr.Unmap()
	if addr.Is4() {
		prefix, err := addr.Prefix(ipv4PrefixBits)
		if err != nil {
			return "", err
		}
		return prefix.Masked().String(), nil
	}
	prefix, err := addr.Prefix(ipv6PrefixBits)
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}
