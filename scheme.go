package netsec

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// Session 是一次成功挑战应答后缓存下来的“认证会话”。
// 它与身份、挑战范围（scope）成对存放，范围变化即视为不可复用。
type Session struct {
	Scheme    string // 方案名：basic / digest
	Scope     string // 创建该会话的挑战范围
	HeaderKey string // 要设置的请求首部，如 Authorization
	HeaderVal string // 预先格式化的首部值；Digest 还需按请求自增 nc

	// Digest 专用的可变状态；Basic 方案留空。
	mu       *sync.Mutex
	nc       uint32
	digest   *digestState
	cred     Credential
	username string
}

type digestState struct {
	realm     string
	nonce     string
	qop       string // auth 或空
	opaque    string
	algorithm string
}

// AuthScheme 把某一种挑战方案（Basic、Digest……）抽象成统一应答器。
// Scope 决定会话在哪些挑战下仍然有效；Answer 依据当前请求生成首部值。
type AuthScheme interface {
	Name() string
	// Scope 从挑战中提取复用范围。同一身份下 scope 相同的新挑战
	// （例如服务器重复抛出同一 realm/nonce）允许复用旧会话。
	Scope(ch Challenge) string
	// Answer 针对一条挑战和一份受信凭据生成新会话。
	Answer(ch Challenge, cred Credential) (*Session, error)
	// Apply 把（可能已缓存的）会话写入请求；Digest 在此自增 nc。
	Apply(req *http.Request, sess *Session)
}

var registeredSchemes = map[string]AuthScheme{
	"basic":  basicScheme{},
	"digest": digestScheme{},
}

func selectScheme(challenges []Challenge) (Challenge, AuthScheme, bool) {
	// 按注册顺序优先选择更强的方案：digest 优先于 basic。
	for _, preferred := range []string{"digest", "basic"} {
		for _, ch := range challenges {
			if ch.Scheme == preferred {
				return ch, registeredSchemes[preferred], true
			}
		}
	}
	return Challenge{}, nil, false
}

// ---- Basic ----

type basicScheme struct{}

func (basicScheme) Name() string { return "basic" }

func (basicScheme) Scope(ch Challenge) string {
	return "basic:realm=" + ch.Realm
}

func (basicScheme) Answer(ch Challenge, cred Credential) (*Session, error) {
	return &Session{
		Scheme:    "basic",
		Scope:     basicScheme{}.Scope(ch),
		HeaderKey: defaultHeaderKey(),
		HeaderVal: "Basic " + basicToken(cred.Username, cred.Password),
		cred:      cred,
		username:  cred.Username,
	}, nil
}

func (basicScheme) Apply(req *http.Request, sess *Session) {
	req.Header.Set(sess.HeaderKey, sess.HeaderVal)
}

// ---- Digest ----

type digestScheme struct{}

func (digestScheme) Name() string { return "digest" }

func (digestScheme) Scope(ch Challenge) string {
	// nonce 是服务器给本次挑战颁发的一次性范围；realm 变化同样失效。
	// stale=true 时服务器会更换 nonce，scope 随之改变，强制重新应答。
	return "digest:realm=" + ch.Realm + ":nonce=" + ch.Params["nonce"]
}

func (digestScheme) Answer(ch Challenge, cred Credential) (*Session, error) {
	st := &digestState{
		realm:     ch.Realm,
		nonce:     ch.Params["nonce"],
		qop:       ch.Params["qop"],
		opaque:    ch.Params["opaque"],
		algorithm: strings.ToUpper(ch.Params["algorithm"]),
	}
	return &Session{
		Scheme:    "digest",
		Scope:     digestScheme{}.Scope(ch),
		HeaderKey: defaultHeaderKey(),
		mu:        &sync.Mutex{},
		digest:    st,
		cred:      cred,
		username:  cred.Username,
	}, nil
}

func (digestScheme) Apply(req *http.Request, sess *Session) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.nc++
	req.Header.Set(sess.HeaderKey, sess.authorization(req, sess.nc))
}

func (sess *Session) authorization(req *http.Request, nc uint32) string {
	st := sess.digest
	algo := st.algorithm
	if algo == "" {
		algo = "MD5"
	}
	cnonce := randomHex(8)
	uri := req.URL.RequestURI()

	ha1 := md5Hex(joinHash(sess.username, st.realm, sess.cred.Password))
	ha2 := md5Hex(joinHash(req.Method, uri))

	var response string
	ncHex := leftPad8(nc)
	if strings.Contains(st.qop, "auth") {
		response = md5Hex(joinHash(ha1, st.nonce, ncHex, cnonce, "auth", ha2))
	} else {
		response = md5Hex(joinHash(ha1, st.nonce, ha2))
	}

	var b strings.Builder
	b.WriteString(`Digest username="`)
	b.WriteString(sess.username)
	b.WriteString(`", realm="`)
	b.WriteString(st.realm)
	b.WriteString(`", nonce="`)
	b.WriteString(st.nonce)
	b.WriteString(`", uri="`)
	b.WriteString(uri)
	b.WriteString(`", response="`)
	b.WriteString(response)
	b.WriteString(`"`)
	if st.opaque != "" {
		b.WriteString(`, opaque="`)
		b.WriteString(st.opaque)
		b.WriteString(`"`)
	}
	if strings.Contains(st.qop, "auth") {
		b.WriteString(`, qop=auth, nc=`)
		b.WriteString(ncHex)
		b.WriteString(`, cnonce="`)
		b.WriteString(cnonce)
		b.WriteString(`"`)
	}
	if algo != "MD5" {
		b.WriteString(`, algorithm=`)
		b.WriteString(algo)
	}
	return b.String()
}

// defaultHeaderKey 是会话创建时的默认首部名；保险库在缓存时按角色覆写。
func defaultHeaderKey() string {
	return "Authorization"
}

func joinHash(parts ...string) string {
	return strings.Join(parts, ":")
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func leftPad8(n uint32) string {
	h := hex.EncodeToString([]byte{
		byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n),
	})
	return h
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// rand 失败在实际环境中几乎不可能；退化为全零仍能保证格式合法。
		return strings.Repeat("00", n)
	}
	return hex.EncodeToString(buf)
}

func basicToken(username, password string) string {
	return base64Std(username + ":" + password)
}
