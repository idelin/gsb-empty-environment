package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// 能力相关错误。
var (
	ErrCapabilityMalformed = errors.New("netsec: capability token malformed")
	ErrCapabilityForged    = errors.New("netsec: capability signature mismatch")
	ErrCapabilityStale     = errors.New("netsec: capability issued by a previous process generation")
	ErrCapabilityExpired   = errors.New("netsec: capability expired")
	ErrPathOutsidePrefix   = errors.New("netsec: request path escapes capability prefix")
	ErrBadPrefix           = errors.New("netsec: prefix must be an absolute path")
)

// launcherState 保存进程实例身份。Launcher 重启（代次轮换）时整体替换，
// 旧代次签发的能力随之全部失效。
type launcherState struct {
	mu         sync.RWMutex
	instanceID string
	generation uint64
}

func (s *launcherState) snapshot() (string, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.instanceID, s.generation
}

// Launcher 是唯一可信的能力签发方：HMAC 密钥与进程实例状态只由它持有。
// 网络宿主应通过 Verifier 校验能力，而无法自行铸造。
type Launcher struct {
	key [32]byte
	st  *launcherState
	now func() time.Time
}

// NewLauncher 创建一个启动器，随机生成密钥与进程实例 ID，代次从 1 开始。
func NewLauncher() (*Launcher, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate launcher key: %w", err)
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	return &Launcher{
		key: key,
		st:  &launcherState{instanceID: id, generation: 1},
		now: time.Now,
	}, nil
}

// Restart 模拟进程代次轮换：代次递增并更换实例 ID，
// 此前签发的所有能力立即失效。
func (l *Launcher) Restart() error {
	id, err := randomID()
	if err != nil {
		return err
	}
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	l.st.instanceID = id
	l.st.generation++
	return nil
}

// Verifier 返回与启动器共享实例状态的校验器，供网络宿主内嵌使用。
func (l *Launcher) Verifier() *Verifier {
	return &Verifier{key: l.key, st: l.st, now: l.now}
}

// Mint 为给定路径前缀签发能力。前缀在签发时即被规范化，
// 之后校验按规范化后的边界做前缀匹配。
func (l *Launcher) Mint(prefix string, ttl time.Duration) (*Capability, error) {
	norm, err := normalizePrefix(prefix)
	if err != nil {
		return nil, err
	}
	instanceID, generation := l.st.snapshot()
	payload := tokenPayload{
		InstanceID: instanceID,
		Generation: generation,
		Prefix:     norm,
		ExpiresAt:  l.now().Add(ttl).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	mac := l.sign(raw)
	return &Capability{
		Prefix:    norm,
		ExpiresAt: time.Unix(payload.ExpiresAt, 0),
		token:     base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac),
	}, nil
}

func (l *Launcher) sign(raw []byte) []byte {
	h := hmac.New(sha256.New, l.key[:])
	h.Write(raw)
	return h.Sum(nil)
}

// Capability 是绑定进程实例、代次与路径前缀的密封令牌。
// 字段对外只读，令牌本体不可伪造。
type Capability struct {
	Prefix    string
	ExpiresAt time.Time
	token     string
}

// Token 返回可跨上下文传递的令牌字符串。
func (c *Capability) Token() string { return c.token }

type tokenPayload struct {
	InstanceID string `json:"i"`
	Generation uint64 `json:"g"`
	Prefix     string `json:"p"`
	ExpiresAt  int64  `json:"e"`
}

// Verifier 校验能力令牌。它与签发它的 Launcher 共享进程实例状态，
// 因此启动器重启后旧能力自动失效。
type Verifier struct {
	key [32]byte
	st  *launcherState
	now func() time.Time
}

// Verify 校验令牌，并确认规范化后的 reqPath 落在能力前缀之内。
func (v *Verifier) Verify(token, reqPath string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ErrCapabilityMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrCapabilityMalformed
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrCapabilityMalformed
	}
	h := hmac.New(sha256.New, v.key[:])
	h.Write(raw)
	if !hmac.Equal(mac, h.Sum(nil)) {
		return ErrCapabilityForged
	}
	var payload tokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ErrCapabilityMalformed
	}
	instanceID, generation := v.st.snapshot()
	if payload.InstanceID != instanceID || payload.Generation != generation {
		return ErrCapabilityStale
	}
	if v.now().Unix() >= payload.ExpiresAt {
		return ErrCapabilityExpired
	}
	if !prefixMatch(payload.Prefix, cleanPath(reqPath)) {
		return fmt.Errorf("%w: %q not under %q", ErrPathOutsidePrefix, cleanPath(reqPath), payload.Prefix)
	}
	return nil
}

// cleanPath 对请求路径做规范化：补前导斜杠、折叠重复分隔符与点段。
// URL 解码后的路径（如 %2e%2e 已还原为 ..）在此被收敛，
// 使穿越序列无法逃出前缀边界。
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// normalizePrefix 规范化签发前缀，去掉尾部斜杠（根前缀除外）。
func normalizePrefix(prefix string) (string, error) {
	if !strings.HasPrefix(prefix, "/") {
		return "", fmt.Errorf("%w: %q", ErrBadPrefix, prefix)
	}
	return cleanPath(prefix), nil
}

// prefixMatch 按路径段边界匹配：/meta 命中 /meta 与 /meta/x，但不命中 /metadata。
func prefixMatch(prefix, cleaned string) bool {
	if prefix == "/" {
		return true
	}
	return cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/")
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("netsec: generate instance id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
