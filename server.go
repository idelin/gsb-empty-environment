package netsec

import (
	"crypto/hmac"
	"strings"
	"sync"
)

// Server 是认证服务器（AAA 侧）。外层身份仅用于把会话路由到
// 对应域的策略档案；授权主体永远来自经验证的内层身份。
type Server struct {
	hostKey []byte

	mu       sync.Mutex
	creds    map[string][]byte // 内层身份 -> 凭据密钥
	routes   map[string]string // 外层域 -> 策略档案（仅路由）
	sessions map[string]*serverSession
}

type serverSession struct {
	cookie    []byte
	portID    string
	portInst  []byte
	profile   string // 由外层身份路由得到，仅用于策略选择
	msgs      []EAP
	cNonce    []byte
	sNonce    []byte
	challenge []byte
	done      bool
}

func NewServer(hostKey []byte) *Server {
	return &Server{
		hostKey:  hostKey,
		creds:    make(map[string][]byte),
		routes:   make(map[string]string),
		sessions: make(map[string]*serverSession),
	}
}

// AddCredential 注册内层身份凭据。
func (s *Server) AddCredential(innerID string, key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds[innerID] = append([]byte(nil), key...)
}

// AddRealmRoute 注册外层域到策略档案的路由。该映射只影响策略选择，
// 不会把外层身份提升为授权主体。
func (s *Server) AddRealmRoute(realm, profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[realm] = profile
}

// Handle 处理一个接入请求并返回响应。每次调用都是无连接的一步，
// 会话由 Request.State 中的 Cookie 定位。
func (s *Server) Handle(req Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(req.State) == 0 {
		return s.open(req)
	}
	sess, ok := s.sessions[string(req.State)]
	if !ok || sess.done {
		return s.reject(req.State, "", nil)
	}
	if !s.validPortBind(req) || req.PortID != sess.portID || !hmac.Equal(req.PortInst, sess.portInst) {
		sess.done = true
		return s.reject(sess.cookie, sess.portID, sess.portInst)
	}
	return s.inner(sess, req)
}

func (s *Server) validPortBind(req Request) bool {
	return hmac.Equal(req.PortMAC, portMAC(s.hostKey, req.PortID, req.PortInst))
}

func (s *Server) open(req Request) Response {
	if !s.validPortBind(req) || req.EAP.Phase != PhaseIdentity ||
		req.EAP.OuterID == "" || len(req.EAP.Nonce) == 0 {
		return s.reject(nil, "", nil)
	}
	sess := &serverSession{
		cookie:   randBytes(16),
		portID:   req.PortID,
		portInst: append([]byte(nil), req.PortInst...),
		profile:  s.routes[realmOf(req.EAP.OuterID)], // 仅路由
		cNonce:   append([]byte(nil), req.EAP.Nonce...),
		sNonce:   randBytes(16),
	}
	sess.challenge = randBytes(16)
	sess.msgs = append(sess.msgs, req.EAP)
	out := EAP{Phase: PhaseTunnel, Nonce: sess.sNonce, Body: sess.challenge}
	sess.msgs = append(sess.msgs, out)
	s.sessions[string(sess.cookie)] = sess
	return s.respond(sess, ResultContinue, &out, "", nil, nil)
}

func (s *Server) inner(sess *serverSession, req Request) Response {
	if req.EAP.Phase != PhaseInner {
		sess.done = true
		return s.reject(sess.cookie, sess.portID, sess.portInst)
	}
	sess.msgs = append(sess.msgs, req.EAP)
	d := &decoder{buf: req.EAP.Body}
	innerID := d.str()
	innerMAC := d.bytes()
	cred, ok := s.creds[innerID]
	if d.err != nil || innerID == "" || !ok {
		sess.done = true
		return s.reject(sess.cookie, sess.portID, sess.portInst)
	}
	tk := tunnelKey(sess.cNonce, sess.sNonce)
	want := mac(cred, labelInner, frame(sess.challenge, []byte(innerID), tk))
	if !hmac.Equal(innerMAC, want) {
		sess.done = true
		return s.reject(sess.cookie, sess.portID, sess.portInst)
	}
	// 内层成功：把结果绑定到隧道导出值、交换摘要与端口实例。
	exporter := exporterKey(tk)
	digest := exchangeDigest(sess.portID, sess.portInst, sess.msgs)
	sess.done = true
	return s.respond(sess, ResultSuccess, nil, innerID, exporter, digest)
}

func (s *Server) respond(sess *serverSession, res Result, eap *EAP, innerID string, exporter, digest []byte) Response {
	r := Response{
		State:    sess.cookie,
		Result:   res,
		EAP:      eap,
		InnerID:  innerID,
		Exporter: exporter,
		Digest:   digest,
		PortID:   sess.portID,
		PortInst: sess.portInst,
	}
	r.AuthMAC = mac(s.hostKey, labelRespMAC, r.authBlob())
	return r
}

func (s *Server) reject(state []byte, portID string, inst []byte) Response {
	r := Response{State: state, Result: ResultFailure, PortID: portID, PortInst: inst}
	r.AuthMAC = mac(s.hostKey, labelRespMAC, r.authBlob())
	return r
}

func realmOf(outerID string) string {
	if i := strings.LastIndexByte(outerID, '@'); i >= 0 {
		return outerID[i+1:]
	}
	return ""
}
