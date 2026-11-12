package netsec

import (
	"net/http"
	"strings"
	"sync"
)

// roleHeader 把认证角色映射到对应的请求/响应首部名。
func roleHeader(role authRole) (reqHeader, challengeHeader string) {
	if role == roleProxy {
		return "Proxy-Authorization", "Proxy-Authenticate"
	}
	return "Authorization", "Www-Authenticate"
}

// identityState 保存某个受信身份下按挑战范围分区的全部会话。
// 同一身份的不同 realm/nonce 各自独立，范围变化绝不复用旧会话。
type identityState struct {
	scopes   []string
	sessions map[string]*Session
}

// credentialVault 是单一角色的认证保险库。源站与代理各持一个实例，
// 且只接受本角色的凭据提供方，从结构上消除跨角色错发首部的可能。
type credentialVault struct {
	role    authRole
	reqHdr  string
	chalHdr string

	lookup func(id Identity, realm string) (Credential, bool)

	mu     sync.Mutex
	states map[Identity]*identityState
}

func newVault(role authRole, lookup func(Identity, string) (Credential, bool)) *credentialVault {
	reqHdr, chalHdr := roleHeader(role)
	return &credentialVault{
		role:    role,
		reqHdr:  reqHdr,
		chalHdr: chalHdr,
		lookup:  lookup,
		states:  make(map[Identity]*identityState),
	}
}

// applyProactive 在发送请求前附加该身份已缓存的会话。
// 没有缓存会话时什么也不发——绝不对陌生或尚未通过挑战的身份猜测凭据。
// 连接复用与否完全不影响判断：依据只有身份与保险库内容。
func (v *credentialVault) applyProactive(req *http.Request, id Identity) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.states[id]
	if st == nil {
		return
	}
	for _, scope := range st.scopes {
		if sess := st.sessions[scope]; sess != nil {
			scheme := registeredSchemesByName[sess.Scheme]
			if scheme != nil {
				scheme.Apply(req, sess)
			}
			return
		}
	}
}

// answerChallenge 依据 401/407 响应里的挑战重新评估凭据。
// 返回的 sentinel 让传输层区分“该身份不受信/方案不支持”与普通网络错误。
func (v *credentialVault) answerChallenge(id Identity, resp *http.Response) (*Session, error) {
	challenges := ParseChallenges(resp.Header[http.CanonicalHeaderKey(v.chalHdr)])
	ch, scheme, ok := selectScheme(challenges)
	if !ok {
		return nil, ErrUnsupportedChallenge
	}
	cred, ok := v.lookup(id, ch.Realm)
	if !ok {
		return nil, ErrUntrustedIdentity
	}
	sess, err := scheme.Answer(ch, cred)
	if err != nil {
		return nil, err
	}
	// 服务器明确宣告旧 nonce 作废（Digest stale=true）：精确摘除同身份的
	// 旧 nonce 范围，再按新挑战建立新范围；其它范围不受影响。
	if ch.Scheme == "digest" && ch.Params["stale"] == "true" {
		v.dropStaleDigest(id, sess.Scope)
	}
	sess.HeaderKey = v.reqHdr
	v.store(id, scheme.Scope(ch), sess)
	return sess, nil
}

func (v *credentialVault) dropStaleDigest(id Identity, keepScope string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.states[id]
	if st == nil {
		return
	}
	filtered := st.scopes[:0]
	for _, scope := range st.scopes {
		if strings.HasPrefix(scope, "digest:") && scope != keepScope {
			delete(st.sessions, scope)
			continue
		}
		filtered = append(filtered, scope)
	}
	st.scopes = filtered
}

// store 按 (身份, 挑战范围) 存放会话；同范围覆盖，新范围追加。
func (v *credentialVault) store(id Identity, scope string, sess *Session) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.states[id]
	if st == nil {
		st = &identityState{sessions: make(map[string]*Session)}
		v.states[id] = st
	}
	if _, exists := st.sessions[scope]; !exists {
		st.scopes = append(st.scopes, scope)
	}
	st.sessions[scope] = sess
}

var registeredSchemesByName = map[string]AuthScheme{
	"basic":  basicScheme{},
	"digest": digestScheme{},
}
