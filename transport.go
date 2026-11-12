package netsec

import (
	"net/http"
	"net/url"
)

// maxAuthRetries 限制单次 RoundTrip 内由 401/407 触发的重试次数。
// 两次重试足以覆盖“代理 407 + 源站 401”或“Digest stale 换 nonce”，
// 同时保证口令错误时不会无限循环。
const maxAuthRetries = 2

// ProxyFunc 按请求返回当前要使用的代理 URL；返回 nil 表示直连。
// 允许动态切换代理：切换后代理身份改变，旧代理会话不会被复用。
type ProxyFunc func(req *http.Request) (*url.URL, error)

// AuthTransportConfig 配置认证分区传输层。两个凭据提供方分别面向
// 源站与代理角色，nil 表示该角色没有任何受信凭据、永不发送对应首部。
type AuthTransportConfig struct {
	Origins OriginCredentials
	Proxies ProxyCredentials
	Proxy   ProxyFunc

	// Base 是承载连接池与实际收发的底层 Transport；
	// 为空时使用默认配置的新 Transport。
	Base http.RoundTripper

	// OnChallenge 可选审计回调，每次收到 401/407 时触发，
	// 便于测试与日志核对“实际收到的挑战”。
	OnChallenge func(role authRole, id Identity, resp *http.Response)
}

// AuthTransport 是实现认证首部分区的 http.RoundTripper。
// 它不维护任何与具体 TCP 连接绑定的认证状态：连接池只负责复用字节流，
// 认证决策完全基于请求身份与两个角色保险库，因此复用连接不可能扩大范围。
type AuthTransport struct {
	originVault *credentialVault
	proxyVault  *credentialVault
	proxyFunc   ProxyFunc
	base        http.RoundTripper
	onChallenge func(authRole, Identity, *http.Response)
}

// NewAuthTransport 按配置组装传输层，并接管底层 Transport 的代理解析。
func NewAuthTransport(cfg AuthTransportConfig) *AuthTransport {
	base := cfg.Base
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	}

	t := &AuthTransport{
		proxyFunc:   cfg.Proxy,
		base:        base,
		onChallenge: cfg.OnChallenge,
	}
	if cfg.Origins != nil {
		t.originVault = newVault(roleOrigin, cfg.Origins.OriginCredential)
	}
	if cfg.Proxies != nil {
		t.proxyVault = newVault(roleProxy, cfg.Proxies.ProxyCredential)
	}

	// 把代理解析收口到本组件：一方面剥离 URL 内嵌 userinfo，杜绝
	// net/http 隐式发送 Proxy-Authorization；另一方面保证连接缓存键
	// 与我们的代理身份判定完全一致，换代理必然新建连接、重新挑战。
	if bt, ok := base.(*http.Transport); ok && cfg.Proxy != nil {
		bt.Proxy = func(req *http.Request) (*url.URL, error) {
			pu, err := cfg.Proxy(req)
			if err != nil || pu == nil {
				return pu, err
			}
			sanitized := *pu
			sanitized.User = nil
			return &sanitized, nil
		}
	}
	return t
}

// Client 返回绑定本传输层的 http.Client。重定向由标准库发起，
// 但每一跳都会重新进入 RoundTrip，认证首部在那里被剥离并重评估。
func (t *AuthTransport) Client() *http.Client {
	return &http.Client{Transport: t}
}

// RoundTrip 实现 http.RoundTripper。
func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. 无条件剥离调用方带来的认证首部。重定向时标准库可能把上一跳的
	// Authorization 复制过来；这里是唯一的认证首部出口，一切以
	// 本组件的角色保险库为准。
	req.Header.Del("Authorization")
	req.Header.Del("Proxy-Authorization")

	originID := OriginIdentity(req)

	// 2. 解析当前代理身份并附加已缓存的代理会话。
	var proxyID Identity
	var haveProxy bool
	if t.proxyFunc != nil && t.proxyVault != nil {
		if pu, err := t.proxyFunc(req); err != nil {
			return nil, err
		} else if pu != nil {
			proxyID = ProxyIdentity(pu)
			haveProxy = true
			t.proxyVault.applyProactive(req, proxyID)
		}
	}

	// 3. 附加已缓存的源站会话。
	if t.originVault != nil {
		t.originVault.applyProactive(req, originID)
	}

	// 4. 发送并在 401/407 上做有界重试。
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusProxyAuthRequired {
			return resp, nil
		}
		if attempt >= maxAuthRetries {
			return resp, nil
		}

		role, id, vault := t.challengeTarget(resp.StatusCode, originID, proxyID, haveProxy)
		if vault == nil {
			// 该角色没有配置任何凭据提供方：原样交还挑战响应。
			return resp, nil
		}
		if t.onChallenge != nil {
			t.onChallenge(role, id, resp)
		}

		sess, err := vault.answerChallenge(id, resp)
		if err != nil {
			// 身份不受信或方案不支持：不发送任何首部，把最近一次挑战
			// 原样（含可读 body）交还给调用方。
			return resp, nil
		}

		// 决定重试后，旧挑战响应体不再需要：排空关闭，使底层连接可复用。
		drainClose(resp)

		// 带请求体的重试需要可重放的 Body。
		if req.Body != nil && req.GetBody == nil {
			return nil, ErrNonReplayableBody
		}
		if req.Body != nil {
			newBody, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = newBody
		}

		// 只设置本角色的首部，另一角色的已发首部保持不变。
		req.Header.Set(sess.HeaderKey, mustHeaderValue(req, sess))
	}
}

func (t *AuthTransport) challengeTarget(status int, originID, proxyID Identity, haveProxy bool) (authRole, Identity, *credentialVault) {
	if status == http.StatusProxyAuthRequired && haveProxy {
		return roleProxy, proxyID, t.proxyVault
	}
	return roleOrigin, originID, t.originVault
}

// mustHeaderValue 让方案对象按当前请求生成最终首部值，
// 保证 Digest 的 nc/cnonce 与本次重发一致。
func mustHeaderValue(req *http.Request, sess *Session) string {
	scheme := registeredSchemesByName[sess.Scheme]
	if scheme == nil {
		return sess.HeaderVal
	}
	scheme.Apply(req, sess)
	return req.Header.Get(sess.HeaderKey)
}
