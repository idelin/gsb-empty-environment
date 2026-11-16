// Package netsec 提供 IKEv2 响应方的 IKE_SA_INIT 准入控制：
// 当半开连接数达到阈值后，对未携带 COOKIE 通知的请求只回应
// COOKIE（不创建状态、不执行 DH），仅当重发请求带回与
// Ni、源地址、SPIi 匹配的有效 COOKIE 时才继续处理。
package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"
)

const (
	// DefaultHalfOpenThreshold 是触发 COOKIE 质询的默认半开连接数阈值。
	DefaultHalfOpenThreshold = 200
	// DefaultRotationInterval 是 COOKIE 签名秘密的默认轮换周期。
	DefaultRotationInterval = time.Minute

	secretSize = 32
	cookieSize = 16
)

// Request 描述一次待准入的 IKE_SA_INIT 请求。
type Request struct {
	SourceAddr netip.Addr // 请求源地址
	SPIi       [8]byte    // 发起方 SPI
	Ni         []byte     // 发起方 nonce
	Cookie     []byte     // 请求携带的 COOKIE 通知数据，无则为 nil
}

// Action 是准入判定结果。
type Action int

const (
	// Process 表示放行，继续正常的 IKE_SA_INIT 处理（创建状态、执行 DH）。
	Process Action = iota
	// SendCookie 表示只回应 COOKIE 通知，不创建状态、不执行 DH。
	SendCookie
	// Drop 表示请求携带了无效 COOKIE，直接丢弃。
	Drop
)

// Decision 是 Admit 的判定输出。
type Decision struct {
	Action Action
	// Cookie 仅在 Action 为 SendCookie 时有效，为应回传给发起方的 COOKIE 值。
	Cookie []byte
}

// Config 配置 Gatekeeper，零值字段使用默认值。
type Config struct {
	// HalfOpenThreshold 为半开连接数阈值，默认 200。
	HalfOpenThreshold int
	// RotationInterval 为秘密轮换周期，默认 1 分钟。
	RotationInterval time.Duration
	// Rand 为秘密的随机源，默认 crypto/rand.Reader。
	Rand io.Reader
	// Now 为时钟，默认 time.Now，测试可注入假时钟。
	Now func() time.Time
}

// Gatekeeper 是 IKE_SA_INIT 准入控制器，可安全并发使用。
type Gatekeeper struct {
	mu               sync.Mutex
	threshold        int
	rotationInterval time.Duration
	rand             io.Reader
	now              func() time.Time

	halfOpen int

	currentSecret  []byte
	previousSecret []byte // 轮换后上一把秘密仍可用一个周期
	rotatedAt      time.Time
}

// NewGatekeeper 创建准入控制器。
func NewGatekeeper(cfg Config) (*Gatekeeper, error) {
	if cfg.HalfOpenThreshold <= 0 {
		cfg.HalfOpenThreshold = DefaultHalfOpenThreshold
	}
	if cfg.RotationInterval <= 0 {
		cfg.RotationInterval = DefaultRotationInterval
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	g := &Gatekeeper{
		threshold:        cfg.HalfOpenThreshold,
		rotationInterval: cfg.RotationInterval,
		rand:             cfg.Rand,
		now:              cfg.Now,
	}
	secret, err := g.newSecret()
	if err != nil {
		return nil, err
	}
	g.currentSecret = secret
	g.rotatedAt = cfg.Now()
	return g, nil
}

// Admit 对一个 IKE_SA_INIT 请求作准入判定。
func (g *Gatekeeper) Admit(req Request) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maybeRotateLocked()

	if req.Cookie != nil {
		if g.validCookieLocked(req) {
			return Decision{Action: Process}
		}
		return Decision{Action: Drop}
	}

	if g.halfOpen >= g.threshold {
		return Decision{
			Action: SendCookie,
			Cookie: computeCookie(g.currentSecret, req),
		}
	}
	return Decision{Action: Process}
}

// HalfOpenBegin 在半开 SA 状态创建时调用。
func (g *Gatekeeper) HalfOpenBegin() {
	g.mu.Lock()
	g.halfOpen++
	g.mu.Unlock()
}

// HalfOpenEnd 在半开 SA 状态销毁（建立完成或超时）时调用。
func (g *Gatekeeper) HalfOpenEnd() {
	g.mu.Lock()
	if g.halfOpen > 0 {
		g.halfOpen--
	}
	g.mu.Unlock()
}

// HalfOpenCount 返回当前半开连接数。
func (g *Gatekeeper) HalfOpenCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.halfOpen
}

// maybeRotateLocked 在轮换周期到达时轮换秘密，旧秘密降级为上一把。
func (g *Gatekeeper) maybeRotateLocked() {
	if g.now().Sub(g.rotatedAt) < g.rotationInterval {
		return
	}
	if secret, err := g.newSecret(); err == nil {
		g.previousSecret = g.currentSecret
		g.currentSecret = secret
		g.rotatedAt = g.now()
	}
}

func (g *Gatekeeper) newSecret() ([]byte, error) {
	secret := make([]byte, secretSize)
	if _, err := io.ReadFull(g.rand, secret); err != nil {
		return nil, errors.New("netsec: generate cookie secret: " + err.Error())
	}
	return secret, nil
}

// validCookieLocked 依次用当前秘密和上一把秘密校验 COOKIE。
func (g *Gatekeeper) validCookieLocked(req Request) bool {
	if hmac.Equal(req.Cookie, computeCookie(g.currentSecret, req)) {
		return true
	}
	if g.previousSecret != nil &&
		hmac.Equal(req.Cookie, computeCookie(g.previousSecret, req)) {
		return true
	}
	return false
}

// computeCookie 计算 COOKIE = HMAC-SHA256(secret, Ni ‖ 源地址 ‖ SPIi) 的前 16 字节。
// Ni 为变长字段，显式长度前缀以避免拼接歧义。
func computeCookie(secret []byte, req Request) []byte {
	mac := hmac.New(sha256.New, secret)
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(req.Ni)))
	mac.Write(lenBuf[:])
	mac.Write(req.Ni)
	addr := req.SourceAddr.As16()
	mac.Write(addr[:])
	mac.Write(req.SPIi[:])
	return mac.Sum(nil)[:cookieSize]
}
