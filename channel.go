package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// 通道相关错误。
var (
	ErrCapabilityRequired      = errors.New("netsec: request to metadata channel requires a capability")
	ErrDialProtected           = errors.New("netsec: dialed address belongs to the metadata channel, capability required")
	ErrRedirectNeedsCapability = errors.New("netsec: redirect into metadata channel requires a freshly minted capability")
)

type ctxKey int

const (
	ctxKeyCapability ctxKey = iota
	ctxKeyAuthorized
)

// WithCapability 把能力绑定到请求上下文，供 Channel.RoundTrip 校验。
func WithCapability(ctx context.Context, cap *Capability) context.Context {
	return context.WithValue(ctx, ctxKeyCapability, cap)
}

func capabilityFrom(ctx context.Context) *Capability {
	cap, _ := ctx.Value(ctxKeyCapability).(*Capability)
	return cap
}

func withAuthorized(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyAuthorized, true)
}

func authorizedFrom(ctx context.Context) bool {
	ok, _ := ctx.Value(ctxKeyAuthorized).(bool)
	return ok
}

// Config 描述受控元数据通道的密封边界。
type Config struct {
	// EndpointHost 是元数据通道的 URL authority（如 "127.0.0.1:7600"）。
	EndpointHost string
	// ProtectedAddrs 是元数据端点实际拨号地址集合（"ip:port"）。
	// 任何拨号落点命中该集合而请求未持有效能力时，连接被强制关闭。
	ProtectedAddrs []string
	// CredentialHeaders 是跨源重定向时必须剥离的凭据头；
	// 为空时使用默认值。
	CredentialHeaders []string
	// BaseDial 是底层拨号函数，由网络宿主注入（默认 net.Dialer）。
	// 通道在其之上核对实际拨号地址。
	BaseDial func(ctx context.Context, network, address string) (net.Conn, error)
}

var defaultCredentialHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
}

// RedirectApprover 在重定向发生前被调用。若目标落入元数据通道，
// 调用方必须向可信启动器重新取得针对目标路径前缀的能力并返回；
// 返回 nil 能力或错误都会阻断此次重定向。
type RedirectApprover func(ctx context.Context, target *url.URL, via []*http.Request) (*Capability, error)

// Channel 是供 Go 网络宿主使用的 http.RoundTripper：
// 每次请求同时校验能力与实际拨号地址，重定向必须重新取得能力。
type Channel struct {
	cfg         Config
	verifier    *Verifier
	credHeaders []string
	protected   map[string]struct{}
	transport   *http.Transport

	mu     sync.Mutex
	dialed []string
}

// NewChannel 基于校验器与配置构建通道。
func NewChannel(cfg Config, verifier *Verifier) *Channel {
	c := &Channel{
		cfg:         cfg,
		verifier:    verifier,
		credHeaders: cfg.CredentialHeaders,
		protected:   make(map[string]struct{}, len(cfg.ProtectedAddrs)),
	}
	if len(c.credHeaders) == 0 {
		c.credHeaders = defaultCredentialHeaders
	}
	for _, addr := range cfg.ProtectedAddrs {
		c.protected[addr] = struct{}{}
	}
	c.transport = &http.Transport{
		DialContext:         c.guardedDial,
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        16,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return c
}

// guardedDial 拨号后核对实际远端地址：命中受保护集合而上下文未携带
// 能力授权标记时，立即关闭连接。这样即使业务域名被解析/劫持到元数据
// 地址，也无法绕过能力校验。
func (c *Channel) guardedDial(ctx context.Context, network, address string) (net.Conn, error) {
	dial := c.cfg.BaseDial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	remote := conn.RemoteAddr().String()
	c.mu.Lock()
	c.dialed = append(c.dialed, remote)
	c.mu.Unlock()
	if _, hit := c.protected[remote]; hit && !authorizedFrom(ctx) {
		conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrDialProtected, remote)
	}
	return conn, nil
}

// Dialed 返回通道记录的实际拨号地址序列，供审计与测试断言。
func (c *Channel) Dialed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dialed...)
}

// isProtectedURL 判断 URL 是否指向元数据通道端点。
func (c *Channel) isProtectedURL(u *url.URL) bool {
	return u != nil && u.Host == c.cfg.EndpointHost
}

// RoundTrip 实现 http.RoundTripper。命中元数据通道的请求必须携带
// 有效能力；校验通过后在内部上下文中打上授权标记，供拨号守卫核对。
func (c *Channel) RoundTrip(req *http.Request) (*http.Response, error) {
	if c.isProtectedURL(req.URL) {
		cap := capabilityFrom(req.Context())
		if cap == nil {
			return nil, ErrCapabilityRequired
		}
		if err := c.verifier.Verify(cap.Token(), req.URL.Path); err != nil {
			return nil, err
		}
		req = req.WithContext(withAuthorized(req.Context()))
	}
	return c.transport.RoundTrip(req)
}

// Client 返回挂接了通道重定向策略的 http.Client。
// approver 可为 nil，此时任何指向元数据通道的重定向都会被拒绝。
func (c *Channel) Client(approver RedirectApprover) *http.Client {
	return &http.Client{
		Transport: c,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("netsec: stopped after 10 redirects")
			}
			// 跨源跳转：剥离凭据头，防止泄漏到第三方端点。
			if !sameOrigin(req.URL, via[len(via)-1].URL) {
				for _, h := range c.credHeaders {
					req.Header.Del(h)
				}
			}
			// 重定向进入元数据通道：必须重新取得能力。
			if c.isProtectedURL(req.URL) {
				if approver == nil {
					return ErrRedirectNeedsCapability
				}
				cap, err := approver(req.Context(), req.URL, via)
				if err != nil {
					return err
				}
				if cap == nil {
					return ErrRedirectNeedsCapability
				}
				if err := c.verifier.Verify(cap.Token(), req.URL.Path); err != nil {
					return err
				}
				fresh := req.WithContext(WithCapability(req.Context(), cap))
				*req = *fresh
			}
			return nil
		},
	}
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Host == b.Host
}
