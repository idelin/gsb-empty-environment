// Package webhook 实现 CloudEvents HTTP Webhook 1.0 授权握手的订阅验证，
// 防止事件分发服务被利用为向第三方放大流量的跳板。
//
// 登记订阅时，验证器向回调地址发送 OPTIONS 握手请求，携带
// WebHook-Request-Origin 与 WebHook-Request-Rate；只有对端明确通过
// WebHook-Allowed-Origin 与 WebHook-Allowed-Rate 表示愿意接收时才允许登记。
// 同时对回调地址施加 HTTPS、内网地址、重定向与响应主机一致性等约束。
package webhook

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// 握手使用的协议头名称（CloudEvents HTTP Webhook 1.0）。
const (
	HeaderRequestOrigin = "WebHook-Request-Origin"
	HeaderRequestRate   = "WebHook-Request-Rate"
	HeaderAllowedOrigin = "WebHook-Allowed-Origin"
	HeaderAllowedRate   = "WebHook-Allowed-Rate"
)

// DefaultTimeout 是单次握手请求的默认超时。
const DefaultTimeout = 10 * time.Second

// Resolver 抽象主机名解析，便于在测试中替换并避免依赖真实 DNS。
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type netResolver struct{}

func (netResolver) LookupIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, network, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr.Unmap())
		}
	}
	return addrs, nil
}

// Config 配置验证器。
type Config struct {
	// Origin 是本平台的事件发送方 origin，回显在 WebHook-Request-Origin 中。必填。
	Origin string
	// RequestRate 是平台期望的投递速率上限，写入 WebHook-Request-Rate。必填。
	RequestRate Rate
	// Timeout 限制单次握手；零值使用 DefaultTimeout。
	Timeout time.Duration
	// Resolver 用于主机名解析；默认使用系统解析器。
	Resolver Resolver
	// DialContext 覆盖底层拨号（测试用）。生产环境留空，验证器会内置
	// 只允许拨向已解析白名单地址的 pin 拨号器。
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSClientConfig 覆盖握手客户端的 TLS 配置（主要用于测试注入可信证书）。
	TLSClientConfig *tls.Config
}

// Verifier 执行订阅地址的 Webhook 授权握手。
type Verifier struct {
	origin      string
	requestRate Rate
	timeout     time.Duration
	resolver    Resolver
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	tlsConfig   *tls.Config
	// allowHTTP 仅用于进程内测试；生产路径始终强制 HTTPS。
	allowHTTP bool
}

// New 构造验证器并校验必要配置。
func New(cfg Config) (*Verifier, error) {
	if strings.TrimSpace(cfg.Origin) == "" {
		return nil, fmt.Errorf("webhook: Config.Origin is required")
	}
	if !cfg.RequestRate.Wildcard && cfg.RequestRate.RequestsPerSecond <= 0 {
		return nil, fmt.Errorf("webhook: Config.RequestRate must be a positive integer or wildcard")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = netResolver{}
	}
	return &Verifier{
		origin:      cfg.Origin,
		requestRate: cfg.RequestRate,
		timeout:     timeout,
		resolver:    resolver,
		dialContext: cfg.DialContext,
		tlsConfig:   cfg.TLSClientConfig,
	}, nil
}

// Result 是一次成功握手的结论。
type Result struct {
	// URL 是通过验证的回调地址（标准化后的字符串）。
	URL string
	// Host 是登记地址的主机名（不含端口），用于响应主机一致性比对。
	Host string
	// EffectiveRate 是平台请求速率与订阅方允许速率的较小值。
	EffectiveRate Rate
	// VerifiedAt 是握手完成时间。
	VerifiedAt time.Time
}

// Verify 对 callbackURL 执行完整的授权握手。任何安全条件不满足都返回非空错误。
func (v *Verifier) Verify(ctx context.Context, callbackURL string) (Result, error) {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return Result{}, fmt.Errorf("webhook: parse callback URL: %w", err)
	}
	if parsed.Scheme != "https" && !v.allowHTTP {
		return Result{}, ErrNonHTTPS
	}
	host := parsed.Hostname()
	if host == "" {
		return Result{}, ErrMissingHost
	}
	if parsed.User != nil {
		return Result{}, fmt.Errorf("webhook: userinfo in callback URL is not allowed")
	}

	addrs, err := v.resolveAndCheck(ctx, host)
	if err != nil {
		return Result{}, err
	}

	dialContext := v.dialContext
	if dialContext == nil {
		dialContext = pinnedDialer(host, addrs)
	}

	client := &http.Client{
		// 显式不跟随重定向：3xx 直接作为握手失败处理。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: v.timeout,
		Transport: &http.Transport{
			// 禁止环境代理介入，避免绕过地址校验与主机一致性检查。
			Proxy:           nil,
			DialContext:     dialContext,
			TLSClientConfig: v.tlsConfig,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, parsed.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("webhook: build validation request: %w", err)
	}
	req.Header.Set(HeaderRequestOrigin, v.origin)
	req.Header.Set(HeaderRequestRate, v.requestRate.String())

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("webhook: validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices &&
		resp.StatusCode < http.StatusBadRequest {
		return Result{}, fmt.Errorf("%w: status %d", ErrRedirect, resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("%w: status %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	if err := checkResponseHost(host, resp.Request.URL); err != nil {
		return Result{}, err
	}

	allowedOrigin := resp.Header.Get(HeaderAllowedOrigin)
	if allowedOrigin == "" {
		return Result{}, ErrMissingAllowedOrigin
	}
	if allowedOrigin != "*" && allowedOrigin != v.origin {
		return Result{}, fmt.Errorf("%w: got %q", ErrOriginNotAllowed, allowedOrigin)
	}

	allowedRateValue := resp.Header.Get(HeaderAllowedRate)
	if allowedRateValue == "" {
		return Result{}, ErrMissingAllowedRate
	}
	allowedRate, err := ParseAllowedRate(allowedRateValue)
	if err != nil {
		return Result{}, err
	}

	effective, err := MinRate(v.requestRate, allowedRate)
	if err != nil {
		return Result{}, err
	}

	return Result{
		URL:           parsed.String(),
		Host:          host,
		EffectiveRate: effective,
		VerifiedAt:    time.Now().UTC(),
	}, nil
}

// resolveAndCheck 解析主机名并拒绝任何回环/私网等不可投递地址。
func (v *Verifier) resolveAndCheck(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddr(addr) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, addr)
		}
		return []netip.Addr{addr.Unmap()}, nil
	}

	addrs, err := v.resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("webhook: resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("webhook: host %q resolved to no addresses", host)
	}
	for _, addr := range addrs {
		if isBlockedAddr(addr) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlockedAddress, host, addr)
		}
	}
	return addrs, nil
}

// isBlockedAddr 判断地址是否属于禁止投递的范围：
// 回环、私网、链路本地、未指定地址、组播及保留网段。
func isBlockedAddr(addr netip.Addr) bool {
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() ||
		addr.IsMulticast() {
		return true
	}
	// IPv4 保留与受限广播网段 240.0.0.0/4（含 255.255.255.255）。
	if addr.Is4() {
		return addr.As4()[0] >= 240
	}
	return false
}

// pinnedDialer 返回只允许拨向本次解析结果的拨号器，固定 DNS 结果，
// 防止 DNS rebinding，也保证连接目标与登记主机对应。
//
// http.Transport 传入的 addr 仍是“主机名:端口”，拨号器校验主机名与登记
// 主机一致后，把主机名替换成本次解析出的白名单 IP 再实际建连。
func pinnedDialer(host string, allowed []netip.Addr) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: DefaultTimeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialHost, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("webhook: invalid dial address %q: %w", addr, err)
		}
		if !strings.EqualFold(dialHost, host) {
			return nil, fmt.Errorf("%w: dial host %q != registered host %q", ErrHostMismatch, dialHost, host)
		}
		if len(allowed) == 0 {
			return nil, fmt.Errorf("%w: no resolved address available for %q", ErrHostMismatch, host)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(allowed[0].String(), port))
	}
}

// checkResponseHost 确保最终响应来自登记时的同一主机。
func checkResponseHost(registeredHost string, responseURL *url.URL) error {
	if responseURL == nil {
		return fmt.Errorf("%w: missing response URL", ErrHostMismatch)
	}
	if !strings.EqualFold(responseURL.Hostname(), registeredHost) {
		return fmt.Errorf("%w: response host %q != registered host %q",
			ErrHostMismatch, responseURL.Hostname(), registeredHost)
	}
	return nil
}
