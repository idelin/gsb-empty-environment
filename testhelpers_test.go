package netsec

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// 沙箱不允许回环 TCP/Unix 监听，因此测试用 net.Pipe 构建内存网络：
// 两端仍然是真实的 http.Server 与 http.Transport，HTTP 语义、首部收发、
// 连接池、重定向全部走真实代码路径，只是字节流不经内核套接字。

// memoryListener 实现 net.Listener，Accept 返回由 Dial 注入的管道端。
type memoryListener struct {
	addr string
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newMemoryListener(addr string) *memoryListener {
	return &memoryListener{
		addr: addr,
		ch:   make(chan net.Conn, 16),
		done: make(chan struct{}),
	}
}

func (l *memoryListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memoryListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *memoryListener) Addr() net.Addr { return memoryAddr(l.addr) }

type memoryAddr string

func (a memoryAddr) Network() string { return "memory" }
func (a memoryAddr) String() string  { return string(a) }

// memoryNetwork 是逻辑地址到内存监听器的注册表，供 Transport.DialContext 路由。
type memoryNetwork struct {
	mu        sync.Mutex
	listeners map[string]*memoryListener
}

func newMemoryNetwork() *memoryNetwork {
	return &memoryNetwork{listeners: make(map[string]*memoryListener)}
}

func (n *memoryNetwork) listen(addr string) *memoryListener {
	n.mu.Lock()
	defer n.mu.Unlock()
	l := newMemoryListener(addr)
	n.listeners[addr] = l
	return l
}

// dial 模拟到某逻辑地址的拨号：建立一对管道，服务端交给监听器。
func (n *memoryNetwork) dial(addr string) (net.Conn, error) {
	n.mu.Lock()
	l, ok := n.listeners[addr]
	n.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("memory network: no listener at %q", addr)
	}
	client, server := net.Pipe()
	select {
	case l.ch <- server:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}

// dialContext 适配 http.Transport.DialContext 的签名。
func (n *memoryNetwork) dialContext(_ context.Context, _, addr string) (net.Conn, error) {
	return n.dial(addr)
}

// testServer 把一个内存监听器与真实 http.Server 绑定。
type testServer struct {
	URL      string
	listener *memoryListener
	server   *http.Server
}

func (n *memoryNetwork) serve(t testingT, addr string, handler http.Handler) *testServer {
	ln := n.listen(addr)
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	return &testServer{URL: "http://" + addr, listener: ln, server: srv}
}

// headerRecord 记录一次请求中对端实际收到的两类认证首部。
type headerRecord struct {
	Authorization      string
	ProxyAuthorization string
}

// recorder 线程安全地累计所有请求实际收到的认证首部。
type recorder struct {
	mu      sync.Mutex
	records []headerRecord
}

func (r *recorder) add(h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, headerRecord{
		Authorization:      h.Get("Authorization"),
		ProxyAuthorization: h.Get("Proxy-Authorization"),
	})
}

func (r *recorder) snapshot() []headerRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]headerRecord, len(r.records))
	copy(out, r.records)
	return out
}

// digestVerifier 实现一个最小可用的 Digest 服务器：颁发 nonce、
// 校验 response，并支持通过 rotateNonce 制造 stale=true 重挑战。
type digestVerifier struct {
	mu          sync.Mutex
	realm       string
	validUser   string
	validPass   string
	nonce       string
	rotated     bool
	challenges  int
	acceptCount int
}

func newDigestVerifier(realm, user, pass string) *digestVerifier {
	d := &digestVerifier{realm: realm, validUser: user, validPass: pass}
	d.rotate()
	return d
}

func (d *digestVerifier) rotate() {
	sum := md5.Sum([]byte(fmt.Sprintf("nonce-%s-%d", d.realm, d.challenges+1)))
	d.nonce = hex.EncodeToString(sum[:])
	d.rotated = true
}

func (d *digestVerifier) challenge() string {
	d.challenges++
	return fmt.Sprintf(`Digest realm="%s", qop="auth", nonce="%s", algorithm=MD5`, d.realm, d.nonce)
}

// verify 校验 Digest Authorization 首部；第二个返回值表示是否因 stale 拒绝。
func (d *digestVerifier) verify(h, method, uri string) (ok bool, stale bool) {
	params := parseDigestHeader(h)
	if params["nonce"] != d.nonce {
		return false, true
	}
	ha1 := md5Hex(joinHash(d.validUser, d.realm, d.validPass))
	ha2 := md5Hex(joinHash(method, uri))
	expected := md5Hex(joinHash(ha1, d.nonce, params["nc"], params["cnonce"], "auth", ha2))
	return expected == params["response"], false
}

func parseDigestHeader(h string) map[string]string {
	out := map[string]string{}
	rest := strings.TrimPrefix(h, "Digest ")
	for _, part := range splitDigestParts(rest) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(kv[1], `"`)
		out[key] = val
	}
	return out
}

func splitDigestParts(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// originSpec 描述一个记录型源站的认证要求。
type originSpec struct {
	realm      string
	basicUser  string
	basicPass  string
	useDigest  bool
	digest     *digestVerifier
	rec        *recorder
	redirectTo string // /redirect 端点的跳转目标（绝对 URL）
}

// testingT 是 *testing.T 的最小依赖接口。
type testingT interface {
	Cleanup(func())
}

// newOriginServer 启动一个源站：无认证首部即给 401（Basic 或 Digest），
// /redirect 先做认证再 302，/public 永远放行。
func newOriginServer(t testingT, network *memoryNetwork, addr string, spec *originSpec) *testServer {
	if spec.rec == nil {
		spec.rec = &recorder{}
	}
	if spec.useDigest {
		spec.digest = newDigestVerifier(spec.realm, spec.basicUser, spec.basicPass)
	}
	return network.serve(t, addr, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		spec.rec.add(req.Header)

		if req.URL.Path == "/public" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "public")
			return
		}

		if req.URL.Path == "/redirect" {
			if spec.denyWithoutAuth(w, req) {
				return
			}
			http.Redirect(w, req, spec.redirectTo, http.StatusFound)
			return
		}

		if spec.denyWithoutAuth(w, req) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "ok from %s path=%s", spec.realm, req.URL.Path)
	}))
}

// denyWithoutAuth 校验认证首部；未通过时写 401 并返回 true。
func (s *originSpec) denyWithoutAuth(w http.ResponseWriter, req *http.Request) bool {
	auth := req.Header.Get("Authorization")
	if s.useDigest {
		if strings.HasPrefix(auth, "Digest ") {
			ok, stale := s.digest.verify(auth, req.Method, req.URL.RequestURI())
			if ok {
				s.digest.mu.Lock()
				s.digest.acceptCount++
				s.digest.mu.Unlock()
				return false
			}
			if stale {
				w.Header().Set("Www-Authenticate", strings.Replace(s.digest.challenge(), ", qop", ", stale=true, qop", 1))
			} else {
				w.Header().Set("Www-Authenticate", s.digest.challenge())
			}
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		w.Header().Set("Www-Authenticate", s.digest.challenge())
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}

	if auth == "Basic "+basicToken(s.basicUser, s.basicPass) {
		return false
	}
	w.Header().Set("Www-Authenticate", `Basic realm="`+s.realm+`"`)
	w.WriteHeader(http.StatusUnauthorized)
	return true
}

// proxySpec 描述一个记录型转发代理：无 Proxy-Authorization 即给 407。
type proxySpec struct {
	realm string
	user  string
	pass  string
	rec   *recorder
}

func newProxyServer(t testingT, network *memoryNetwork, addr string, spec *proxySpec) *testServer {
	if spec.rec == nil {
		spec.rec = &recorder{}
	}
	transport := &http.Transport{
		// 代理自身的出站拨号也走内存网络，且禁用代理解析，保证它直接
		// 连接目标源站而不是回环到自己。
		Proxy:       nil,
		DialContext: network.dialContext,
	}
	return network.serve(t, addr, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		spec.rec.add(req.Header)

		if req.Header.Get("Proxy-Authorization") != "Basic "+basicToken(spec.user, spec.pass) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="`+spec.realm+`"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}

		outReq := req.Clone(req.Context())
		// 代理转发前移除逐跳的代理认证首部，源站不应看到它。
		outReq.Header.Del("Proxy-Authorization")
		outReq.RequestURI = ""
		if u, err := url.Parse(req.RequestURI); err == nil {
			outReq.URL = u
		}
		resp, err := transport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
}
