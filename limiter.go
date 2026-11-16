package netsec

import (
	"net/netip"
	"sync"
	"time"
)

const (
	// DefaultWindow 是滚动限额窗口的默认长度。
	DefaultWindow = time.Hour
	// DefaultMaxDistinctClients 是单个前缀在窗口内可接收的不同客户端 offer 的默认上限。
	DefaultMaxDistinctClients = 20
)

// PrefixOf 返回地址所属的限额前缀：IPv4 取 /24，IPv6 取 /48。
func PrefixOf(addr netip.Addr) netip.Prefix {
	if addr.Is4() || addr.Is4In6() {
		return netip.PrefixFrom(addr.Unmap(), 24).Masked()
	}
	return netip.PrefixFrom(addr, 48).Masked()
}

// Limiter 按代理所在前缀统计滚动窗口内出现过的不同客户端。
// 它只负责计数判定，不知道代理标识，因此轮换代理标识无法绕过限额。
type Limiter struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	max    int
	seen   map[netip.Prefix]map[string]time.Time
}

// Option 用于定制 Limiter。
type Option func(*Limiter)

// WithClock 替换时间源，主要用于测试窗口边界。
func WithClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// WithWindow 替换滚动窗口长度。
func WithWindow(window time.Duration) Option {
	return func(l *Limiter) { l.window = window }
}

// WithMaxDistinctClients 替换窗口内不同客户端数上限。
func WithMaxDistinctClients(max int) Option {
	return func(l *Limiter) { l.max = max }
}

// NewLimiter 创建一个按前缀限额的限流器。
func NewLimiter(opts ...Option) *Limiter {
	l := &Limiter{
		now:    time.Now,
		window: DefaultWindow,
		max:    DefaultMaxDistinctClients,
		seen:   make(map[netip.Prefix]map[string]time.Time),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Allow 判定前缀 prefix 是否还能接收客户端 clientID 的 offer。
// 窗口内已出现过的客户端重复匹配只计一次并刷新其活跃时间；
// 新客户端在窗口内不同客户端数达到上限时被拒绝（返回 false），
// 调用方应跳过该前缀而不报错。判定与计数是原子的。
func (l *Limiter) Allow(prefix netip.Prefix, clientID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	clients := l.seen[prefix]
	if clients == nil {
		clients = make(map[string]time.Time)
	}
	l.prune(prefix, clients, now)
	l.seen[prefix] = clients

	if _, ok := clients[clientID]; ok {
		clients[clientID] = now
		return true
	}
	if len(clients) >= l.max {
		return false
	}
	clients[clientID] = now
	return true
}

// DistinctClients 返回前缀当前窗口内的不同客户端数，用于观测与测试。
func (l *Limiter) DistinctClients(prefix netip.Prefix) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	clients := l.seen[prefix]
	l.prune(prefix, clients, l.now())
	return len(clients)
}

// prune 删除窗口外的时间戳，调用时必须持有锁。
func (l *Limiter) prune(prefix netip.Prefix, clients map[string]time.Time, now time.Time) {
	cutoff := now.Add(-l.window)
	for id, last := range clients {
		if !last.After(cutoff) {
			delete(clients, id)
		}
	}
	if len(clients) == 0 {
		delete(l.seen, prefix)
	}
}
