package earlydata

import (
	"context"
	"errors"
	"time"
)

// 混淆票龄以毫秒为单位、按 32 位无符号整数取模（RFC 8446 第 4.2.11.1 节）。
const (
	ticketAgeModulus int64 = 1 << 32
	maxWindowMillis  int64 = 1<<31 - 1
)

// Reason 是 0-RTT 被拒绝、退回 1-RTT 的具体原因，便于握手路径记录指标与日志。
type Reason int

const (
	// ReasonNone 表示准入通过。
	ReasonNone Reason = iota
	// ReasonInvalidRequest 表示请求字段不完整（如缺少 binder 或票据签发时间）。
	ReasonInvalidRequest
	// ReasonAgeOverflow 表示服务端实测票龄已达到 2^32 毫秒，
	// 32 位混淆票龄必然发生回绕错位，无法可靠比较。
	ReasonAgeOverflow
	// ReasonAgeSkew 表示混淆票龄与实测票龄的偏差超出调用方给定窗口。
	ReasonAgeSkew
	// ReasonReplay 表示 PSK binder 已存在于重放记录中，属于重放尝试。
	ReasonReplay
	// ReasonStoreUnavailable 表示重放记录库不可用，按失败关闭原则退回 1-RTT。
	ReasonStoreUnavailable
)

// Request 是握手路径在收到带 early_data 的 ClientHello 后提交的准入请求。
type Request struct {
	// TicketIssuedAt 是解密会话票据得到的票据签发时间（服务端时钟口径）。
	TicketIssuedAt time.Time
	// TicketAgeAdd 是会话票据中携带的 ticket_age_add。
	TicketAgeAdd uint32
	// ObfuscatedAge 是 ClientHello 中 pre_shared_key 扩展携带的
	// obfuscated_ticket_age（客户端实测票龄与 ticket_age_add 之和模 2^32）。
	ObfuscatedAge uint32
	// Binder 是被选中 PSK 的 binder 值；若不便保存原值，可传入其稳定哈希。
	// 同一 binder 在整个集群中的取值必须一致。
	Binder []byte
}

// Result 是准入结论。Allowed 为 false 时握手路径必须退回 1-RTT，
// 但连接本身保持不变、继续完成握手。
type Result struct {
	Allowed bool
	Reason  Reason
}

// Config 描述 Gate 的可调参数。
type Config struct {
	// Window 是允许的客户端票龄与服务端实测票龄之间的最大偏差（含边界），
	// 必须为正且毫秒值小于 2^31，以保证 32 位模差可解释。
	Window time.Duration
	// RecordTTL 是 binder 重放记录的保留时长，应覆盖票据仍可被接受的
	// 剩余生命周期；为零时回退为 Window。
	RecordTTL time.Duration
	// Store 是重放记录库，跨节点部署时必须使用集群共享实现。
	Store ReplayStore
	// Now 用于注入时钟，默认 time.Now。
	Now func() time.Time
}

// Gate 是早期数据准入器，可供握手路径并发调用。
// Gate 本身无状态，所有重放状态集中在 ReplayStore 中。
type Gate struct {
	store     ReplayStore
	window    time.Duration
	recordTTL time.Duration
	now       func() time.Time
}

// NewGate 校验配置并构造准入器。
func NewGate(cfg Config) (*Gate, error) {
	if cfg.Store == nil {
		return nil, errors.New("earlydata: ReplayStore is required")
	}
	if cfg.Window <= 0 {
		return nil, errors.New("earlydata: Window must be positive")
	}
	if cfg.Window.Milliseconds() > maxWindowMillis {
		return nil, errors.New("earlydata: Window is too large for 32-bit ticket age arithmetic")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	recordTTL := cfg.RecordTTL
	if recordTTL <= 0 {
		recordTTL = cfg.Window
	}
	return &Gate{
		store:     cfg.Store,
		window:    cfg.Window,
		recordTTL: recordTTL,
		now:       now,
	}, nil
}

// Admit 执行一次 0-RTT 准入判断。
//
// 本函数永不因策略原因返回错误或中断连接：任何不满足条件的情形都体现为
// Allowed=false 的普通结论，握手路径据此退回 1-RTT 即可。
func (g *Gate) Admit(ctx context.Context, req Request) Result {
	if len(req.Binder) == 0 || req.TicketIssuedAt.IsZero() {
		return Result{Allowed: false, Reason: ReasonInvalidRequest}
	}

	if reason := g.checkTicketAge(req); reason != ReasonNone {
		return Result{Allowed: false, Reason: reason}
	}

	// 仅在本地校验通过后才写重放记录，避免无效握手污染记录库。
	// 写入（原子的“不存在则创建”）成功才放行，库不可用或 binder 已存在都退回。
	firstObserved, err := g.store.CheckAndMark(ctx, req.Binder, g.recordTTL)
	if err != nil {
		return Result{Allowed: false, Reason: ReasonStoreUnavailable}
	}
	if !firstObserved {
		return Result{Allowed: false, Reason: ReasonReplay}
	}
	return Result{Allowed: true, Reason: ReasonNone}
}

// checkTicketAge 按 RFC 8446 第 8.3.10 节比较混淆票龄。
func (g *Gate) checkTicketAge(req Request) Reason {
	observedAgeMs := g.now().Sub(req.TicketIssuedAt).Milliseconds()

	// 实测票龄达到 32 位毫秒量程时，混淆票龄已经完整回绕过一圈，
	// 模 2^32 的比较无法区分真实票龄与回绕后的错位值，直接拒绝。
	if observedAgeMs >= ticketAgeModulus {
		return ReasonAgeOverflow
	}

	// 负的实测票龄（票据时间略超前于本机时钟）转成 uint64 后按位掩码，
	// 在数学上仍等于模 2^32 的结果，因此统一用同一表达式计算期望值。
	expectedObfuscated := uint32((uint64(observedAgeMs) + uint64(req.TicketAgeAdd)) & 0xFFFFFFFF)

	// 两个 uint32 相减后转 int32，得到落在 [-2^31, 2^31-1] 的有符号模差，
	// 自动吸收 2^32 回绕；窗口远小于 2^31 毫秒时该差值即真实偏差。
	deltaMillis := int64(int32(req.ObfuscatedAge - expectedObfuscated))
	skew := time.Duration(absInt64(deltaMillis)) * time.Millisecond
	if skew > g.window {
		return ReasonAgeSkew
	}
	return ReasonNone
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
