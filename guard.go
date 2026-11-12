package netsec

import (
	"errors"
	"fmt"
	"time"
)

// Action 告诉会话读取循环对当前报文应执行的动作。
type Action int

const (
	// ActionAccept 序号合法，继续业务处理。
	ActionAccept Action = iota
	// ActionResend 检测到跳号，需发送 ResendRequest；当前报文不投递给业务层。
	ActionResend
	// ActionLogout 协议或重放违规，需发送 Logout 并关闭连接。
	ActionLogout
)

// Reason 是守卫给出判定的具体原因，便于日志审计与测试断言。
type Reason int

const (
	ReasonOK Reason = iota
	ReasonGap
	ReasonMissingMsgSeqNum
	ReasonInvalidMsgSeqNum
	ReasonSeqTooLowNoPossDup
	ReasonPossDupInvalidFlag
	ReasonMissingSendingTime
	ReasonMissingOrigSendingTime
	ReasonInvalidSendingTime
	ReasonInvalidOrigSendingTime
	ReasonOrigAfterSending
	ReasonResetRejected
	ReasonResetOnNonLogon
	ReasonResetInvalidFlag
)

// String 返回原因的稳定标识符。
func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonGap:
		return "gap"
	case ReasonMissingMsgSeqNum:
		return "missing_msg_seq_num"
	case ReasonInvalidMsgSeqNum:
		return "invalid_msg_seq_num"
	case ReasonSeqTooLowNoPossDup:
		return "seq_too_low_without_poss_dup"
	case ReasonPossDupInvalidFlag:
		return "invalid_poss_dup_flag"
	case ReasonMissingSendingTime:
		return "missing_sending_time"
	case ReasonMissingOrigSendingTime:
		return "missing_orig_sending_time"
	case ReasonInvalidSendingTime:
		return "invalid_sending_time"
	case ReasonInvalidOrigSendingTime:
		return "invalid_orig_sending_time"
	case ReasonOrigAfterSending:
		return "orig_sending_time_after_sending_time"
	case ReasonResetRejected:
		return "seq_num_reset_rejected"
	case ReasonResetOnNonLogon:
		return "reset_flag_on_non_logon"
	case ReasonResetInvalidFlag:
		return "invalid_reset_seq_num_flag"
	default:
		return fmt.Sprintf("reason(%d)", int(r))
	}
}

// ResetPolicy 决定守卫是否接受 Logon 报文中的 ResetSeqNumFlag(141)=Y。
type ResetPolicy int

const (
	// ResetReject 拒绝一切序号重置请求（默认安全策略）。
	ResetReject ResetPolicy = iota
	// ResetAllow 允许重置，仅在调用方明确授权时使用。
	ResetAllow
)

// Guard 是单个 FIX 会话的序号重放守卫，供读取循环逐报文调用。
// 零值可用，默认期望序号为 1 且拒绝序号重置；Guard 可被并发安全使用。
type Guard struct {
	mu          mu
	nextSeqNum  int
	resetPolicy ResetPolicy
}

// NewGuard 创建守卫，nextSeqNum 为期望收到的首个 MsgSeqNum(34)
// （FIX 新建会话通常为 1），resetPolicy 控制是否接受 141=Y。
func NewGuard(nextSeqNum int, resetPolicy ResetPolicy) *Guard {
	if nextSeqNum < 1 {
		nextSeqNum = 1
	}
	return &Guard{nextSeqNum: nextSeqNum, resetPolicy: resetPolicy}
}

// NextSeqNum 返回当前期望的下一个入站序号。
func (g *Guard) NextSeqNum() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.nextSeqNum
}

// Verdict 是守卫对单个报文的判定结果。
type Verdict struct {
	// Action 是读取循环要执行的动作。
	Action Action
	// Reason 解释判定原因。
	Reason Reason
	// GotSeq 是报文携带的 MsgSeqNum，缺失或无法解析时为 -1。
	GotSeq int
	// WantSeq 是判定时守卫期望的序号。
	WantSeq int
	// BeginSeqNo 仅在 ActionResend 时有意义，即 ResendRequest(2) 的 BeginSeqNo(7)。
	BeginSeqNo int
	// Err 携带解析/校验错误，Action 为 ActionLogout 或报文无法解析时非空。
	Err error
}

// Handle 校验一条入站 FIX 报文并推进会话序号状态：
//
//   - ResetSeqNumFlag(141)=Y 只允许出现在 Logon(35=A) 上，且仅在调用方
//     通过 ResetAllow 授权时才接受，重置为报文中的 MsgSeqNum（正常应为 1）。
//   - MsgSeqNum 偏小且 PossDupFlag(43)!=Y：判定为重放，Logout 断开。
//   - PossDupFlag=Y 时必须带 OrigSendingTime(122)，且 122 不得晚于
//     SendingTime(52)，否则 Logout 断开。落后于期望序号的旧重放不推进
//     序号；ResendRequest 恢复期间按序到达的补发命中期望序号，正常推进。
//   - MsgSeqNum 跳号：返回 ActionResend，读取循环据此发送 ResendRequest，
//     守卫状态保持不变以等待补齐。
//   - 序号精确匹配：接受并把期望序号推进到 seq+1。
func (g *Guard) Handle(raw []byte) Verdict {
	msg, err := ParseMessage(raw)
	if err != nil {
		return Verdict{
			Action: ActionLogout,
			Reason: ReasonInvalidMsgSeqNum,
			GotSeq: -1,
			Err:    err,
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if isResetRequested(msg) {
		return g.handleReset(msg)
	}

	seq, verdict := requireSeqNum(msg, g.nextSeqNum)
	if verdict.Action == ActionLogout {
		return verdict
	}

	switch {
	case seq < g.nextSeqNum:
		return g.handleLowSeqNum(msg, seq)
	case seq > g.nextSeqNum:
		return Verdict{
			Action:     ActionResend,
			Reason:     ReasonGap,
			GotSeq:     seq,
			WantSeq:    g.nextSeqNum,
			BeginSeqNo: g.nextSeqNum,
			Err: fmt.Errorf("netsec: expected seq %d got %d",
				g.nextSeqNum, seq),
		}
	default:
		if isPossDup(msg) {
			if v := g.handleLowSeqNum(msg, seq); v.Action == ActionLogout {
				return v
			}
		}
		g.nextSeqNum = seq + 1
		return Verdict{Action: ActionAccept, Reason: ReasonOK, GotSeq: seq, WantSeq: seq}
	}
}

// handleReset 必须在持有 g.mu 的情况下调用。
func (g *Guard) handleReset(msg *Message) Verdict {
	if raw, _ := msg.Get(TagResetSeqNumFlag); raw != "Y" {
		return logout(msg, g.nextSeqNum, ReasonResetInvalidFlag,
			fmt.Errorf("netsec: invalid ResetSeqNumFlag(141)=%q", raw))
	}
	msgType, _ := msg.Get(TagMsgType)
	if msgType != "A" {
		return logout(msg, g.nextSeqNum, ReasonResetOnNonLogon,
			errors.New("netsec: ResetSeqNumFlag only valid on Logon(35=A)"))
	}
	if g.resetPolicy != ResetAllow {
		return logout(msg, g.nextSeqNum, ReasonResetRejected,
			errors.New("netsec: ResetSeqNumFlag(141)=Y rejected by policy"))
	}

	seq, verdict := requireSeqNum(msg, g.nextSeqNum)
	if verdict.Action == ActionLogout {
		return verdict
	}
	g.nextSeqNum = seq + 1
	return Verdict{Action: ActionAccept, Reason: ReasonOK, GotSeq: seq, WantSeq: seq}
}

// handleLowSeqNum 必须在持有 g.mu 的情况下调用。
func (g *Guard) handleLowSeqNum(msg *Message, seq int) Verdict {
	flag, ok := msg.Get(TagPossDupFlag)
	if !ok {
		return logout(msg, g.nextSeqNum, ReasonSeqTooLowNoPossDup,
			fmt.Errorf("netsec: seq %d < expected %d without PossDupFlag",
				seq, g.nextSeqNum))
	}
	if flag != "Y" && flag != "N" {
		return logout(msg, g.nextSeqNum, ReasonPossDupInvalidFlag,
			fmt.Errorf("netsec: invalid PossDupFlag(43)=%q", flag))
	}
	if flag != "Y" {
		return logout(msg, g.nextSeqNum, ReasonSeqTooLowNoPossDup,
			fmt.Errorf("netsec: seq %d < expected %d with PossDupFlag=N",
				seq, g.nextSeqNum))
	}

	sendingRaw, ok := msg.Get(TagSendingTime)
	if !ok {
		return logout(msg, g.nextSeqNum, ReasonMissingSendingTime,
			errors.New("netsec: duplicate missing SendingTime(52)"))
	}
	sendingTime, err := parseFIXUTCTimestamp(sendingRaw)
	if err != nil {
		return logout(msg, g.nextSeqNum, ReasonInvalidSendingTime,
			fmt.Errorf("netsec: invalid SendingTime(52)=%q: %w", sendingRaw, err))
	}

	origRaw, ok := msg.Get(TagOrigSendingTime)
	if !ok {
		return logout(msg, g.nextSeqNum, ReasonMissingOrigSendingTime,
			errors.New("netsec: PossDupFlag=Y missing OrigSendingTime(122)"))
	}
	origTime, err := parseFIXUTCTimestamp(origRaw)
	if err != nil {
		return logout(msg, g.nextSeqNum, ReasonInvalidOrigSendingTime,
			fmt.Errorf("netsec: invalid OrigSendingTime(122)=%q: %w", origRaw, err))
	}

	if origTime.After(sendingTime) {
		return logout(msg, g.nextSeqNum, ReasonOrigAfterSending,
			fmt.Errorf("netsec: OrigSendingTime %s after SendingTime %s",
				origTime.UTC().Format(time.RFC3339),
				sendingTime.UTC().Format(time.RFC3339)))
	}

	// 合法重放：序号状态保持不变。
	return Verdict{Action: ActionAccept, Reason: ReasonOK, GotSeq: seq, WantSeq: g.nextSeqNum}
}

// requireSeqNum 提取并校验 MsgSeqNum(34)；失败时返回 Logout 判定。
// 必须在持有 g.mu 的情况下调用。
func requireSeqNum(msg *Message, want int) (int, Verdict) {
	raw, ok := msg.Get(TagMsgSeqNum)
	if !ok {
		v := logout(msg, want, ReasonMissingMsgSeqNum,
			errors.New("netsec: missing MsgSeqNum(34)"))
		return -1, v
	}
	seq, err := parsePositiveInt(raw)
	if err != nil {
		v := logout(msg, want, ReasonInvalidMsgSeqNum,
			fmt.Errorf("netsec: invalid MsgSeqNum(34)=%q: %w", raw, err))
		return -1, v
	}
	return seq, Verdict{}
}

func isResetRequested(msg *Message) bool {
	raw, ok := msg.Get(TagResetSeqNumFlag)
	if !ok {
		return false
	}
	if raw != "Y" && raw != "N" {
		// 非法值交给 handleReset 按协议违规处理。
		return true
	}
	return raw == "Y"
}

func isPossDup(msg *Message) bool {
	flag, ok := msg.Get(TagPossDupFlag)
	return ok && flag == "Y"
}

func logout(msg *Message, want int, reason Reason, err error) Verdict {
	got := -1
	if raw, ok := msg.Get(TagMsgSeqNum); ok {
		if seq, parseErr := parsePositiveInt(raw); parseErr == nil {
			got = seq
		}
	}
	return Verdict{Action: ActionLogout, Reason: reason, GotSeq: got, WantSeq: want, Err: err}
}

func parsePositiveInt(raw string) (int, error) {
	value, err := atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("expected positive integer, got %q", raw)
	}
	return value, nil
}

// parseFIXUTCTimestamp 解析 FIX UTCTimestamp，接受秒级
// (YYYYMMDD-HH:MM:SS) 与毫秒级 (YYYYMMDD-HH:MM:SS.sss) 两种形式，
// 时间一律按 UTC 解释。
func parseFIXUTCTimestamp(raw string) (time.Time, error) {
	switch len(raw) {
	case len("20060102-15:04:05"):
		return time.Parse("20060102-15:04:05", raw)
	case len("20060102-15:04:05.000"):
		return time.Parse("20060102-15:04:05.000", raw)
	default:
		return time.Time{}, fmt.Errorf("unexpected timestamp length %d", len(raw))
	}
}
