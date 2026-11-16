package netsec

import (
	"net/netip"
	"sort"
	"sync"
)

// Proxy 是代理向经纪服务注册时上报的信息。
type Proxy struct {
	// ID 是代理标识，审查方可能频繁轮换它。
	ID string
	// Addr 是代理的 IP 地址，限额按其所在前缀统计，与 ID 无关。
	Addr netip.Addr
	// ReportedNAT 是代理自报的 NAT 类型。
	ReportedNAT NATType
}

type proxyState struct {
	proxy   Proxy
	prefix  netip.Prefix
	probed  NATType
	hasProb bool
}

// effectiveNAT 返回代理的有效 NAT 类型：
// 自报类型与最近一次探测结果冲突时，以探测结果为准。
func (p *proxyState) effectiveNAT() NATType {
	if p.hasProb {
		return p.probed
	}
	return p.proxy.ReportedNAT
}

// Broker 为客户端 offer 匹配代理，并按代理前缀执行限额。
type Broker struct {
	mu      sync.Mutex
	limiter *Limiter
	proxies map[string]*proxyState
}

// NewBroker 创建经纪服务，limiter 为 nil 时使用默认限额。
func NewBroker(limiter *Limiter) *Broker {
	if limiter == nil {
		limiter = NewLimiter()
	}
	return &Broker{limiter: limiter, proxies: make(map[string]*proxyState)}
}

// RegisterProxy 注册或替换一个代理。重新注册会清除旧的探测结果。
func (b *Broker) RegisterProxy(p Proxy) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.proxies[p.ID] = &proxyState{proxy: p, prefix: PrefixOf(p.Addr)}
}

// RemoveProxy 注销代理。
func (b *Broker) RemoveProxy(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.proxies, id)
}

// RecordProbe 记录一次 NAT 探测结果，之后的匹配以它为准。
func (b *Broker) RecordProbe(proxyID string, nat NATType) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.proxies[proxyID]; ok {
		p.probed = nat
		p.hasProb = true
	}
}

// Match 为客户端 clientID 的一个 offer 挑选代理。
// 返回匹配到的代理 ID；ok 为 false 表示当前没有可用代理
// （包括相关前缀达到限额，此时静默跳过而不报错）。
// 每次调用至多把一个 offer 交给一个代理。
func (b *Broker) Match(clientID string, clientNAT NATType) (proxyID string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ids := make([]string, 0, len(b.proxies))
	for id := range b.proxies {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		p := b.proxies[id]
		if !natCompatible(clientNAT, p.effectiveNAT()) {
			continue
		}
		if b.limiter.Allow(p.prefix, clientID) {
			return id, true
		}
	}
	return "", false
}
