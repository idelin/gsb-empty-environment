package netsec

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// 认证角色：源站认证与代理认证在协议上就是两条独立的首部通道
// （Authorization 对 Proxy-Authorization，401 对 407）。
// 组件内部同样强制使用两个互不相通的保险库实例，任何代码路径都无法
// 把一侧的会话当作另一侧的凭据发出。
type authRole string

const (
	roleOrigin authRole = "origin"
	roleProxy  authRole = "proxy"
)

// 认证尝试在受信身份之外被中止时返回的哨兵错误。
var (
	// ErrUntrustedIdentity 表示凭据提供方没有为该身份登记任何凭据，
	// 调用方必须放弃，而不是向陌生主机试探性发送凭据。
	ErrUntrustedIdentity = errors.New("netsec: no credential is trusted for this identity")
	// ErrUnsupportedChallenge 表示对端给出的挑战方案（如 Negotiate）
	// 没有已注册的应答器，调用方必须把 401/407 原样交还给用户。
	ErrUnsupportedChallenge = errors.New("netsec: unsupported authentication challenge")
)

// Identity 是认证分区的主键：协议 + 规范化主机端口。
// 凭据、会话与连接复用都以它为边界，绝不按“当前 TCP 连接”隐式继承。
type Identity struct {
	Scheme string // http 或 https
	Host   string // 小写 host:port，默认端口已补全
}

// OriginIdentity 从请求 URL 提取源站身份。
// 即使请求经过代理（URL 为绝对形式），源站身份仍只取目标 URL，
// 与代理身份分别计算。
func OriginIdentity(req *http.Request) Identity {
	return identityOf(req.URL)
}

// ProxyIdentity 从代理 URL 提取代理身份，并丢弃其中的 userinfo：
// 代理 URL 内嵌口令只允许作为配置来源，不允许走 net/http 的隐式通道。
func ProxyIdentity(proxyURL *url.URL) Identity {
	return identityOf(proxyURL)
}

func identityOf(u *url.URL) Identity {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		hostname, port = host, ""
	}
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return Identity{Scheme: scheme, Host: net.JoinHostPort(hostname, port)}
}

// String 返回 scheme://host:port 形式，便于日志与测试断言。
func (id Identity) String() string {
	return id.Scheme + "://" + id.Host
}

// Credential 是一份明文凭据及其可接受的挑战域。
// TrustedRealms 为空表示：只要主机身份受信，该主机出示的任意 realm 都可应答；
// 非空时只有名单内 realm 可应答，防止同主机多应用之间越界。
type Credential struct {
	Username      string
	Password      string
	TrustedRealms []string
}

func (c Credential) acceptsRealm(realm string) bool {
	if len(c.TrustedRealms) == 0 {
		return true
	}
	for _, r := range c.TrustedRealms {
		if r == realm {
			return true
		}
	}
	return false
}

// OriginCredentials 只回答源站身份的凭据查询。
// 接口按角色拆分，使源站与代理提供方在编译期就无法互相顶替。
type OriginCredentials interface {
	OriginCredential(id Identity, realm string) (Credential, bool)
}

// ProxyCredentials 只回答代理身份的凭据查询。
type ProxyCredentials interface {
	ProxyCredential(id Identity, realm string) (Credential, bool)
}

// StaticCredentials 是按受信身份登记的静态凭据表。
// 未登记的身份一律返回 false，组件据此拒绝发送任何首部。
type StaticCredentials struct {
	entries map[Identity]Credential
}

// NewStaticCredentials 创建空凭据表。
func NewStaticCredentials() *StaticCredentials {
	return &StaticCredentials{entries: make(map[Identity]Credential)}
}

// Add 把凭据绑定到一个明确受信的身份上。
func (s *StaticCredentials) Add(id Identity, cred Credential) {
	s.entries[id] = cred
}

// OriginCredential 实现 OriginCredentials。
func (s *StaticCredentials) OriginCredential(id Identity, realm string) (Credential, bool) {
	return s.lookup(id, realm)
}

// ProxyCredential 实现 ProxyCredentials。
func (s *StaticCredentials) ProxyCredential(id Identity, realm string) (Credential, bool) {
	return s.lookup(id, realm)
}

func (s *StaticCredentials) lookup(id Identity, realm string) (Credential, bool) {
	cred, ok := s.entries[id]
	if !ok || !cred.acceptsRealm(realm) {
		return Credential{}, false
	}
	return cred, true
}

// Challenge 是一条 WWW-Authenticate / Proxy-Authenticate 挑战的结构化表示。
type Challenge struct {
	Scheme string            // 小写方案名，如 basic、digest
	Realm  string            // realm 参数，可能为空
	Params map[string]string // 其余参数，键统一小写，值已去引号
	Raw    string            // 原始首部值，用于审计
}

// ParseChallenges 解析若干认证挑战首部，每个首部值承载一个挑战。
// 参数解析支持带引号字符串（引号内逗号不分割）与反斜杠转义。
func ParseChallenges(headers []string) []Challenge {
	challenges := make([]Challenge, 0, len(headers))
	for _, line := range headers {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		scheme, rest := splitToken(line)
		params := parseAuthParams(rest)
		challenges = append(challenges, Challenge{
			Scheme: strings.ToLower(scheme),
			Realm:  params["realm"],
			Params: params,
			Raw:    line,
		})
	}
	return challenges
}

func splitToken(s string) (string, string) {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i], strings.TrimLeft(s[i:], " \t")
		}
	}
	return s, ""
}

func parseAuthParams(s string) map[string]string {
	params := make(map[string]string)
	for len(s) > 0 {
		// 跳过分隔符与空白
		for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == ',') {
			s = s[1:]
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var value string
		if len(s) > 0 && s[0] == '"' {
			value, s = consumeQuoted(s)
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				value, s = strings.TrimSpace(s), ""
			} else {
				value, s = strings.TrimSpace(s[:end]), s[end:]
			}
		}
		params[key] = value
	}
	return params
}

func consumeQuoted(s string) (string, string) {
	var b strings.Builder
	s = s[1:] // 跳过开引号
	for len(s) > 0 {
		switch s[0] {
		case '\\':
			if len(s) > 1 {
				b.WriteByte(s[1])
				s = s[2:]
			} else {
				s = s[1:]
			}
		case '"':
			return b.String(), s[1:]
		default:
			b.WriteByte(s[0])
			s = s[1:]
		}
	}
	return b.String(), s
}
