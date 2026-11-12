package netsec

import (
	"fmt"
	"strings"
)

// 模型使用的事件标识。宿主通过 Event.Name 区分语义。
const (
	// EvAuthRequest 是会话发起时发送的首个认证请求（零耗时消息）。
	EvAuthRequest = "auth.request"
	// EvAuthResponse 是对端返回的成功认证响应（零耗时消息）。
	EvAuthResponse = "auth.response"
	// EvRetransmit 是重传定时器到期事件，持续重传直到整体截止。
	EvRetransmit = "auth.retransmit"
	// EvDeadline 是整体认证截止事件，到期必须拒绝会话。
	EvDeadline = "auth.deadline"
	// EvJunk 是对端/攻击者注入的无关或畸形消息（零耗时消息）。
	EvJunk = "net.junk"
)

// SessionState 是认证会话的状态。
type SessionState uint8

const (
	// Idle 是尚未开始的初始状态。
	Idle SessionState = iota
	// Waiting 表示已发起认证、正在等待对端响应。
	Waiting
	// Accepted 表示在截止前收到合法响应，会话被接受。
	Accepted
	// Rejected 表示整体截止到达，会话被拒绝。
	Rejected
)

// String 返回状态的可读名称。
func (st SessionState) String() string {
	switch st {
	case Idle:
		return "idle"
	case Waiting:
		return "waiting"
	case Accepted:
		return "accepted"
	case Rejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Config 是认证模型的时序参数（单位为 tick）。
type Config struct {
	// Deadline 是整体认证截止时间，相对会话开始时刻。
	Deadline Time
	// RetransmitInterval 是两次重传之间的定时间隔，必须为正。
	RetransmitInterval Time
}

// Session 是被验证的认证会话状态。
type Session struct {
	cfg Config

	state       SessionState
	reflect     bool
	deadlineSeq uint64
	retransSeq  uint64
	sent        uint64
	junkDropped uint64
}

// State 返回当前会话状态。
func (s *Session) State() SessionState { return s.state }

// Sent 返回已发送的认证请求/重传次数。
func (s *Session) Sent() uint64 { return s.sent }

// JunkDropped 返回被丢弃的无关消息数。
func (s *Session) JunkDropped() uint64 { return s.junkDropped }

// World 把认证会话与计时调度器装配在一起，实现 Handler 接口，
// 是 Go 验证宿主直接驱动的小模型。
type World struct {
	sched   *Scheduler
	session *Session
}

// NewWorld 构造验证世界。reflect 为 true 时，每条注入的无关消息都会
// 在同一时刻再生产一条无关消息，用于制造零耗时循环。
func NewWorld(maxZeroSteps uint64, cfg Config, reflect bool) *World {
	if cfg.Deadline == 0 {
		panic("netsec: deadline must be positive")
	}
	if cfg.RetransmitInterval == 0 {
		panic("netsec: retransmit interval must be positive")
	}
	w := &World{
		session: &Session{cfg: cfg, reflect: reflect},
	}
	w.sched = NewScheduler(HandlerFunc(w.Handle), maxZeroSteps)
	return w
}

// Scheduler 暴露底层调度器，供宿主推进时钟与检查事件。
func (w *World) Scheduler() *Scheduler { return w.sched }

// Session 暴露认证会话。
func (w *World) Session() *Session { return w.session }

// Start 在当前时刻发起认证：安排整体截止定时器、首个重传定时器，
// 并零耗时发送首个认证请求。
func (w *World) Start() {
	if w.session.state != Idle {
		panic("netsec: session already started")
	}
	w.session.state = Waiting
	w.session.deadlineSeq = w.sched.After(EvDeadline, w.sched.Clock()+w.session.cfg.Deadline)
	w.session.retransSeq = w.sched.After(EvRetransmit, w.sched.Clock()+w.session.cfg.RetransmitInterval)
	w.sched.Post(EvAuthRequest)
}

// InjectJunk 在当前时刻注入一条无关/畸形消息（零耗时）。
func (w *World) InjectJunk() { w.sched.Post(EvJunk) }

// Respond 在当前时刻注入一条合法认证响应（零耗时）。
func (w *World) Respond() { w.sched.Post(EvAuthResponse) }

// Handle 解释调度器派发的事件并驱动会话转移。
func (w *World) Handle(ev Event) {
	s := w.session
	switch ev.Name {
	case EvDeadline:
		// 截止点到达：超时动作优先，任何尚未处理的普通消息都不能再挽留会话。
		if s.state == Waiting {
			s.state = Rejected
			w.sched.Cancel(s.retransSeq)
		}
	case EvRetransmit:
		if s.state == Waiting {
			s.sent++
			// 持续重传：只要还在等待，就安排下一个重传定时器，
			// 但整体截止仍然由 EvDeadline 独立保证。
			s.retransSeq = w.sched.After(EvRetransmit, w.sched.Clock()+s.cfg.RetransmitInterval)
		}
	case EvAuthRequest:
		if s.state == Waiting {
			s.sent++
		}
	case EvAuthResponse:
		if s.state == Waiting {
			s.state = Accepted
			w.sched.Cancel(s.deadlineSeq)
			w.sched.Cancel(s.retransSeq)
		}
	case EvJunk:
		if s.state == Waiting {
			s.junkDropped++
			if s.reflect {
				// 攻击者把无关消息反射回来：同一时刻再生产一条零耗时消息，
				// 若不加约束就会无限推迟截止定时器。
				w.sched.Post(EvJunk)
			}
		}
	}
}

// Fingerprint 返回与可达性相关的状态指纹：会话状态、反射开关、
// 冻结时钟下的待处理事件多重集。计数器（Sent/JunkDropped）刻意排除，
// 因为它们不影响未来的可选转移。
func (w *World) Fingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "state=%s reflect=%t clock=%d pending=[", w.session.state, w.session.reflect, w.sched.Clock())
	for i, ev := range w.sched.Pending() {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s@%d", ev.Name, ev.At)
	}
	b.WriteByte(']')
	return b.String()
}
