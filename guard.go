package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 端点校验阶段的哨兵错误，调用方可使用 errors.Is 判定拒绝原因。
var (
	ErrNonHTTPS         = errors.New("netsec: endpoint must use the https scheme")
	ErrEndpointUserInfo = errors.New("netsec: endpoint must not carry userinfo")
	ErrEmptyHost        = errors.New("netsec: endpoint has an empty host")
	ErrIPLiteral        = errors.New("netsec: endpoint host must be a whitelisted domain name, not an IP literal")
	ErrNotWhitelisted   = errors.New("netsec: endpoint host is not in the push service whitelist")
	ErrNoResolvedIP     = errors.New("netsec: endpoint host did not resolve to any IP address")
	ErrReservedIP       = errors.New("netsec: endpoint resolves to a reserved or non-public IP address")
	ErrPortNotAllowed   = errors.New("netsec: endpoint port is not allowed")
)

var hostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// HostPattern 是白名单中的一个主机模式。
// 普通形式（如 push.example.com）只做精确匹配；
// 通配形式（如 *.example.com）只匹配恰好一级子域，
// 例如 a.example.com 命中，而 example.com 与 a.b.example.com 不命中。
type HostPattern struct {
	raw      string
	base     string
	wildcard bool
}

// ParseHostPattern 解析白名单主机模式，拒绝 IP 字面量与非法标签。
func ParseHostPattern(pattern string) (HostPattern, error) {
	p := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pattern)), ".")
	if p == "" {
		return HostPattern{}, errors.New("netsec: empty host pattern")
	}
	if strings.Contains(p, ":") || net.ParseIP(p) != nil {
		return HostPattern{}, fmt.Errorf("netsec: invalid host pattern %q", pattern)
	}
	wildcard := false
	if strings.HasPrefix(p, "*.") {
		wildcard = true
		p = p[2:]
	}
	for _, label := range strings.Split(p, ".") {
		if !hostLabelPattern.MatchString(label) {
			return HostPattern{}, fmt.Errorf("netsec: invalid host label %q", label)
		}
	}
	return HostPattern{raw: wildcardPattern(p, wildcard), base: p, wildcard: wildcard}, nil
}

func wildcardPattern(base string, wildcard bool) string {
	if wildcard {
		return "*." + base
	}
	return base
}

// String 返回规范化后的模式文本。
func (p HostPattern) String() string { return p.raw }

func (p HostPattern) matches(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !p.wildcard {
		return host == p.base
	}
	suffix := "." + p.base
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	left := strings.TrimSuffix(host, suffix)
	// 通配符只代表一个标签，且不能为空。
	return left != "" && !strings.Contains(left, ".")
}

// Resolver 抽象 DNS 解析，便于注入测试解析器而不触达真实网络。
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Guard 集中实施订阅端点的 SSRF 防护策略：
// 强制 https、主机白名单、解析后 IP 复核，以及拨号时的二次复核。
type Guard struct {
	// Whitelist 为允许的推送服务主机模式。
	Whitelist []HostPattern
	// Resolver 默认为 net.DefaultResolver，测试中可替换为静态解析器。
	Resolver Resolver
	// IPAllowed 判定一个 IP 是否允许访问，默认为 IsPublicIP。
	IPAllowed func(net.IP) bool
	// AllowedPorts 为允许的 TCP 端口，空值表示仅允许 443。
	AllowedPorts []int
	// Dialer 为底层拨号器，默认带 10 秒超时。
	Dialer interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}

	once sync.Once
}

func (g *Guard) initDefaults() {
	g.once.Do(func() {
		if g.Resolver == nil {
			g.Resolver = net.DefaultResolver
		}
		if g.IPAllowed == nil {
			g.IPAllowed = IsPublicIP
		}
		if g.Dialer == nil {
			g.Dialer = &net.Dialer{Timeout: 10 * time.Second}
		}
	})
}

// IsHostAllowed 报告主机是否命中白名单。
func (g *Guard) IsHostAllowed(host string) bool {
	for _, pattern := range g.Whitelist {
		if pattern.matches(host) {
			return true
		}
	}
	return false
}

func (g *Guard) portAllowed(port string) bool {
	if port == "" {
		return true
	}
	if len(g.AllowedPorts) == 0 {
		return port == "443"
	}
	for _, allowed := range g.AllowedPorts {
		if port == strconv.Itoa(allowed) {
			return true
		}
	}
	return false
}

// ValidateEndpoint 对订阅端点做完整的静态校验：
// https 方案、无 userinfo、域名白名单、端口限制、解析结果全部为公网 IP。
func (g *Guard) ValidateEndpoint(ctx context.Context, raw string) (*url.URL, error) {
	g.initDefaults()
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse endpoint: %w", err)
	}
	if !u.IsAbs() || !strings.EqualFold(u.Scheme, "https") {
		return nil, ErrNonHTTPS
	}
	if u.User != nil {
		return nil, ErrEndpointUserInfo
	}
	host := u.Hostname()
	if host == "" {
		return nil, ErrEmptyHost
	}
	if net.ParseIP(host) != nil || strings.HasPrefix(host, "[") {
		return nil, ErrIPLiteral
	}
	if !g.portAllowed(u.Port()) {
		return nil, fmt.Errorf("%w: %s", ErrPortNotAllowed, u.Port())
	}
	if !g.IsHostAllowed(host) {
		return nil, fmt.Errorf("%w: %s", ErrNotWhitelisted, host)
	}
	addrs, err := g.Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoResolvedIP, host)
	}
	// 只要解析结果中混入一个保留地址即拒绝，防止攻击者借多 A 记录绕过。
	for _, addr := range addrs {
		if !g.IPAllowed(normalizeIP(addr.IP)) {
			return nil, fmt.Errorf("%w: %s -> %s", ErrReservedIP, host, addr.IP)
		}
	}
	return u, nil
}

// DialContext 供 http.Transport 使用，在每次实际建连前重新解析并复核 IP，
// 防止校验通过后发生 DNS 重绑定，同时保证拨号目标永远不是内网地址。
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	g.initDefaults()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid dial address %q: %w", address, err)
	}
	if !g.IsHostAllowed(host) {
		return nil, fmt.Errorf("%w: %s", ErrNotWhitelisted, host)
	}
	addrs, err := g.Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve %s: %w", host, err)
	}
	allowedIP, err := g.checkDialPolicy(host, addrs)
	if err != nil {
		return nil, err
	}
	return g.Dialer.DialContext(ctx, network, net.JoinHostPort(allowedIP.String(), port))
}

// checkDialPolicy 执行拨号前的白名单与解析 IP 复核，返回第一个可用 IP。
// 该方法与 DialContext 共享同一套判定，便于测试传输层在不建连的情况下验证策略。
func (g *Guard) checkDialPolicy(host string, addrs []net.IPAddr) (net.IP, error) {
	if !g.IsHostAllowed(host) {
		return nil, fmt.Errorf("%w: %s", ErrNotWhitelisted, host)
	}
	var lastErr error
	for _, addr := range addrs {
		ip := normalizeIP(addr.IP)
		if !g.IPAllowed(ip) {
			lastErr = fmt.Errorf("%w: %s -> %s", ErrReservedIP, host, ip)
			continue
		}
		return ip, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: %s", ErrNoResolvedIP, host)
	}
	return nil, lastErr
}

// CheckDialPolicy 暴露拨号前策略复核（白名单 + 解析 IP），
// 供自定义 http.RoundTripper 在真正建连前复用同一套安全判定。
func (g *Guard) CheckDialPolicy(ctx context.Context, host string) (net.IP, error) {
	g.initDefaults()
	if !g.IsHostAllowed(host) {
		return nil, fmt.Errorf("%w: %s", ErrNotWhitelisted, host)
	}
	addrs, err := g.Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve %s: %w", host, err)
	}
	return g.checkDialPolicy(host, addrs)
}

// normalizeIP 将 IPv4 映射地址还原为 IPv4，确保策略判定一致。
func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

var (
	reservedIPv4Nets = mustParseCIDRs([]string{
		"0.0.0.0/8",       // 本网段
		"10.0.0.0/8",      // 私网
		"100.64.0.0/10",   // 运营商级 NAT
		"127.0.0.0/8",     // 环回
		"169.254.0.0/16",  // 链路本地
		"172.16.0.0/12",   // 私网
		"192.0.0.0/24",    // IETF 协议保留
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 中继任播
		"192.168.0.0/16",  // 私网
		"198.18.0.0/15",   // 基准测试
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/3",     // 224.0.0.0/4 组播 + 240.0.0.0/4 保留地址
	})
	reservedIPv6Nets = mustParseCIDRs([]string{
		"::1/128",      // 环回
		"::/128",       // 未指定
		"fc00::/7",     // 唯一本地地址 ULA
		"fe80::/10",    // 链路本地
		"ff00::/8",     // 组播
		"64:ff9b::/96", // NAT64
		"64:ff9b:1::/48",
	})
)

func mustParseCIDRs(list []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(list))
	for _, entry := range list {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			panic(err)
		}
		nets = append(nets, network)
	}
	return nets
}

// IsPublicIP 报告 IP 是否为公网可路由地址，
// 环回、私网、链路本地、ULA、组播等保留地址一律返回 false。
func IsPublicIP(raw net.IP) bool {
	ip := normalizeIP(raw)
	if v4 := ip.To4(); v4 != nil {
		for _, network := range reservedIPv4Nets {
			if network.Contains(v4) {
				return false
			}
		}
		return true
	}
	for _, network := range reservedIPv6Nets {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}
