package netsec

import (
	"bytes"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

// SAE 有限循环群编号（IEEE 802.11）。
const (
	GroupP256 uint16 = 19
	GroupP384 uint16 = 20
	GroupP521 uint16 = 21
)

// Decision 是前置过滤对一条 SAE commit 的处置结论。
type Decision int

const (
	// DecisionAccept 表示通过前置检查，可进入耗时的曲线计算。
	DecisionAccept Decision = iota
	// DecisionReject 表示报文非法（scalar/element 越界、反射等），直接丢弃。
	DecisionReject
	// DecisionAntiClogging 表示未完成事务超阈值且未持有效 token，
	// 只回 anti-clogging token，不做任何曲线运算。
	DecisionAntiClogging
)

func (d Decision) String() string {
	switch d {
	case DecisionAccept:
		return "accept"
	case DecisionReject:
		return "reject"
	case DecisionAntiClogging:
		return "anti-clogging"
	}
	return "unknown"
}

// Config 配置 Filter。
type Config struct {
	// MaxPending 是允许并发存在的未完成 SAE 事务上限；
	// 达到上限后新的 commit 必须携带有效 anti-clogging token。
	MaxPending int
	// TokenTTL 是单个 token 密钥窗口的时长；
	// token 在当前窗口与上一窗口内有效（密钥随窗口自动轮换）。
	TokenTTL time.Duration
	// Now 可注入时钟便于测试；为空则用 time.Now。
	Now func() time.Time
}

// Filter 是 SAE commit 的前置过滤器，供 Authentication 帧处理调用。
// 它在任何曲线运算之前完成全部廉价校验，抵御 commit 洪泛与反射攻击。
type Filter struct {
	maxPending int
	ttl        time.Duration
	now        func() time.Time

	mu      sync.Mutex
	secret  [32]byte // token 主密钥，窗口密钥由此派生
	pending map[string]*pendingTx
}

type pendingTx struct {
	group      uint16
	ownElement []byte // 己方已发出的 commit element，用于反射检测
}

// NewFilter 创建过滤器。secret 为 token 主密钥（建议 32 字节，
// 多 AP 组网可共享）；传 nil 则随机生成。
func NewFilter(cfg Config, secret []byte) (*Filter, error) {
	if cfg.MaxPending < 1 {
		return nil, errors.New("netsec: MaxPending must be >= 1")
	}
	if cfg.TokenTTL <= 0 {
		return nil, errors.New("netsec: TokenTTL must be > 0")
	}
	f := &Filter{
		maxPending: cfg.MaxPending,
		ttl:        cfg.TokenTTL,
		now:        cfg.Now,
		pending:    make(map[string]*pendingTx),
	}
	if f.now == nil {
		f.now = time.Now
	}
	if secret == nil {
		if _, err := rand.Read(f.secret[:]); err != nil {
			return nil, err
		}
	} else {
		if len(secret) < 16 {
			return nil, errors.New("netsec: secret too short, need >= 16 bytes")
		}
		h := sha256.Sum256(secret)
		copy(f.secret[:], h[:])
	}
	return f, nil
}

// CheckCommit 对收到的 SAE commit 做前置过滤，不做任何曲线点乘。
// token 为对端随 commit 附带的 anti-clogging token（可为空）。
// 返回 DecisionAntiClogging 时同时返回应回给站点的 token。
func (f *Filter) CheckCommit(sta net.HardwareAddr, group uint16, scalar, element, token []byte) (Decision, []byte) {
	curve, ok := curveForGroup(group)
	if !ok {
		return DecisionReject, nil
	}
	if !validScalar(curve, scalar) {
		return DecisionReject, nil
	}
	if !validElement(curve, element) {
		return DecisionReject, nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// 反射检测：攻击者把 AP 自己的 commit 回投，element 与己方相同。
	if tx, ok := f.pending[string(sta)]; ok && bytes.Equal(tx.ownElement, element) {
		return DecisionReject, nil
	}

	if len(f.pending) >= f.maxPending && !f.validTokenLocked(sta, group, token) {
		return DecisionAntiClogging, f.tokenLocked(sta, group, f.window())
	}
	return DecisionAccept, nil
}

// TrackStart 在 AP 接受 commit 并发出己方 commit 后登记未完成事务，
// ownElement 为己方发出的 commit element（用于反射检测）。
func (f *Filter) TrackStart(sta net.HardwareAddr, group uint16, ownElement []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[string(sta)] = &pendingTx{group: group, ownElement: bytes.Clone(ownElement)}
}

// TrackEnd 在事务完成（confirm 成功）或超时后注销。
func (f *Filter) TrackEnd(sta net.HardwareAddr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, string(sta))
}

// Pending 返回当前未完成事务数。
func (f *Filter) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

func curveForGroup(group uint16) (elliptic.Curve, bool) {
	switch group {
	case GroupP256:
		return elliptic.P256(), true
	case GroupP384:
		return elliptic.P384(), true
	case GroupP521:
		return elliptic.P521(), true
	}
	return nil, false
}

// validScalar 要求 scalar 为定长编码且 2 <= scalar <= r-1（r 为群阶）。
func validScalar(curve elliptic.Curve, scalar []byte) bool {
	n := curve.Params().N
	if len(scalar) != byteLen(curve) {
		return false
	}
	s := new(big.Int).SetBytes(scalar)
	return s.Cmp(big.NewInt(2)) >= 0 && s.Cmp(new(big.Int).Sub(n, big.NewInt(1))) <= 0
}

// validElement 要求 element 为 x||y 定长编码、是曲线上非无穷远点。
func validElement(curve elliptic.Curve, element []byte) bool {
	l := byteLen(curve)
	if len(element) != 2*l {
		return false
	}
	x := new(big.Int).SetBytes(element[:l])
	y := new(big.Int).SetBytes(element[l:])
	if x.Sign() == 0 && y.Sign() == 0 { // 无穷远点
		return false
	}
	return curve.IsOnCurve(x, y)
}

func byteLen(curve elliptic.Curve) int {
	return (curve.Params().BitSize + 7) / 8
}

func (f *Filter) window() int64 {
	return f.now().UnixNano() / f.ttl.Nanoseconds()
}

// windowKey 由主密钥按时间窗口派生轮换密钥。
func (f *Filter) windowKey(window int64) []byte {
	mac := hmac.New(sha256.New, f.secret[:])
	mac.Write([]byte("sae-act-key"))
	var w [8]byte
	binary.BigEndian.PutUint64(w[:], uint64(window))
	mac.Write(w[:])
	return mac.Sum(nil)
}

// tokenLocked 计算指定窗口下 (MAC, group) 的 anti-clogging token。
func (f *Filter) tokenLocked(sta net.HardwareAddr, group uint16, window int64) []byte {
	mac := hmac.New(sha256.New, f.windowKey(window))
	mac.Write([]byte("sae-act"))
	mac.Write(sta)
	var g [2]byte
	binary.BigEndian.PutUint16(g[:], group)
	mac.Write(g[:])
	return mac.Sum(nil)
}

// validTokenLocked 校验 token，接受当前与上一密钥窗口（轮换宽限）。
func (f *Filter) validTokenLocked(sta net.HardwareAddr, group uint16, token []byte) bool {
	if len(token) != sha256.Size {
		return false
	}
	w := f.window()
	for _, win := range []int64{w, w - 1} {
		if subtle.ConstantTimeCompare(token, f.tokenLocked(sta, group, win)) == 1 {
			return true
		}
	}
	return false
}

// String 实现 fmt.Stringer，便于日志记录。
func (f *Filter) String() string {
	return fmt.Sprintf("SAEFilter(pending=%d/%d)", f.Pending(), f.maxPending)
}
