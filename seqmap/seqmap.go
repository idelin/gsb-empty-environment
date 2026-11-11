// Package seqmap 实现边界 SYN 代理的纯状态转换层。
//
// 该包不进行任何 IO：调用方（Go 数据面）把已解析的 TCP 报文封装成 Event
// 喂入 Conn.Step，得到 Verdict。Verdict 指示该事件是否允许跨边界转发、需要
// 代理自行发出哪些信号（SYN、SYN+ACK、ACK、RST），以及转发时在对侧序号
// 空间中应当使用的 SEQ 与 ACK。
//
// 所有序号计算都在 2^ModBits 的模空间内进行：生产环境使用 ModBits=32 的
// TCP 序号空间；测试可使用更小的模数，以小模数参考模型快速覆盖回绕等
// 边界行为。窗口始终要求小于模数的一半（32 位模数时即小于 2^31）。
//
// SYN 与 FIN 各消费一个序号，数据按字节消费序号；重传不会重复推进本地
// 接收序号（rcvNxt）。在两侧（前向 Front、后向 Back）都进入 ESTABLISHED
// 之前，携带数据的事件一律被拒绝。
package seqmap

import (
	"errors"
)

// Side 标识事件到达的那一侧。
//
//	Front：客户端 <-> 代理，代理在此侧代答握手
//	Back ：代理   <-> 服务端，代理在此侧主动发起连接
type Side uint8

const (
	Front Side = iota
	Back
)

func (s Side) String() string {
	if s == Back {
		return "back"
	}
	return "front"
}

// Flags 是报文的 TCP 标志位集合，仅保留与状态机相关的四个标志。
type Flags uint8

const (
	FlagSYN Flags = 1 << iota
	FlagACK
	FlagFIN
	FlagRST
)

func (f Flags) Has(v Flags) bool { return f&v == v }

func (f Flags) String() string {
	names := []struct {
		bit Flags
		tag string
	}{
		{FlagSYN, "SYN"},
		{FlagACK, "ACK"},
		{FlagFIN, "FIN"},
		{FlagRST, "RST"},
	}
	out := ""
	for _, n := range names {
		if f.Has(n.bit) {
			if out != "" {
				out += "|"
			}
			out += n.tag
		}
	}
	if out == "" {
		return "none"
	}
	return out
}

// State 是单条半连接（一条腿）的状态。
type State uint8

const (
	// StateClosed：该腿尚未建立或已被 RST 拆除。
	StateClosed State = iota
	// StateSynRcvd：前向腿收到 SYN、已回 SYN+ACK，等待最终 ACK。
	StateSynRcvd
	// StateSynSent：后向腿已发出 SYN，等待 SYN+ACK。
	StateSynSent
	// StateEstablished：三次握手完成，可转发数据。
	StateEstablished
	// StateClosing：已收到 FIN 并接受，继续处理重传与对侧 FIN。
	StateClosing
)

func (s State) String() string {
	switch s {
	case StateSynRcvd:
		return "syn-rcvd"
	case StateSynSent:
		return "syn-sent"
	case StateEstablished:
		return "established"
	case StateClosing:
		return "closing"
	default:
		return "closed"
	}
}

// Reason 是 Verdict 拒绝某事件的原因；ReasonOK 为零值表示接受。
type Reason uint8

const (
	ReasonOK Reason = iota
	// ReasonBadFlags：标志位组合非法（如 RST 与 SYN/FIN 同时出现）。
	ReasonBadFlags
	// ReasonHandshakeIncomplete：握手未完成，不允许数据转发。
	ReasonHandshakeIncomplete
	// ReasonInvalidSeq：序号落在接收窗口之外（前向差距达到半个模数）。
	ReasonInvalidSeq
	// ReasonInvalidAck：握手期间的 ACK 确认号不是对 SYN+ACK 的确认。
	ReasonInvalidAck
	// ReasonOutOfWindow：部分重叠的新段或乱序段，数据面不应直接转发。
	ReasonOutOfWindow
	// ReasonClosed：连接已被 RST 拆除，后续事件全部丢弃。
	ReasonClosed
)

func (r Reason) String() string {
	switch r {
	case ReasonBadFlags:
		return "bad-flags"
	case ReasonHandshakeIncomplete:
		return "handshake-incomplete"
	case ReasonInvalidSeq:
		return "invalid-seq"
	case ReasonInvalidAck:
		return "invalid-ack"
	case ReasonOutOfWindow:
		return "out-of-window"
	case ReasonClosed:
		return "closed"
	default:
		return "ok"
	}
}

// Signals 是代理需要自行发出的信号集合（位图）。
type Signals uint8

const (
	// SignalSynAckFront：向前向（客户端）发 SYN+ACK，重传 SYN 时重复产生。
	SignalSynAckFront Signals = 1 << iota
	// SignalSynBack：向后向（服务端）发 SYN，仅在握手打通后产生一次。
	SignalSynBack
	// SignalAckBack：向后向发最终 ACK，确认服务端的 SYN+ACK。
	SignalAckBack
	// SignalRstFront：向前向发 RST。
	SignalRstFront
	// SignalRstBack：向后向发 RST。
	SignalRstBack
)

func (s Signals) Has(v Signals) bool { return s&v == v }

// Event 是数据面已解析的单个 TCP 报文事件。
//
// Seq 与 Ack 均为线上原始序号（生产环境为 32 位）。PayloadLen 为报文
// 携带的应用数据字节数。AckValid 指示 Ack 是否有效（ACK 标志是否置位）。
type Event struct {
	Side       Side
	Flags      Flags
	Seq        uint64
	Ack        uint64
	AckValid   bool
	PayloadLen int
}

// Xlate 给出转发到对侧时应使用的序号与确认号。
type Xlate struct {
	Seq      uint64
	Ack      uint64
	AckValid bool
}

// Verdict 是一次 Step 的结果。
//
// Accept 为 true 时，数据面应把（按 Xlate 改写后的）报文转发到另一侧；
// 为 false 时丢弃报文并参考 Reason。Signals 与 Accept 相互独立：握手
// 阶段代理产生的信号即使不转发原报文也会出现。Retransmit 标记该事件
// 是已覆盖序号空间的重传——允许转发，但状态机不会因此推进 rcvNxt。
type Verdict struct {
	Accept     bool
	Retransmit bool
	Reason     Reason
	Signals    Signals
	Xlate      Xlate
}

// Config 构造连接所需的参数。四个 ISN 中两个由对端提供、两个由代理选定。
type Config struct {
	// ModBits 选择序号空间模数 2^ModBits，范围 [4,32]，生产环境传 32。
	ModBits int
	// FrontPeerISN：客户端在其 SYN 中通告的 ISN。
	FrontPeerISN uint64
	// FrontProxyISN：代理在前向腿上选用的 ISN（SYN+ACK 的序号）。
	FrontProxyISN uint64
	// BackProxyISN：代理在后向腿上选用的 ISN（发出 SYN 的序号）。
	BackProxyISN uint64
	// BackPeerISN：服务端在 SYN+ACK 中通告的 ISN；未知可先传 0，
	// 实际收到 SYN+ACK 时由状态机记录。
	BackPeerISN uint64
}

// engine 封装固定模数 2^bits 下的模运算。
type engine struct {
	mod  uint64
	mask uint64
	half uint64
}

func newEngine(bits int) (engine, error) {
	if bits < 4 || bits > 32 {
		return engine{}, errors.New("seqmap: ModBits 必须在 [4,32] 范围内")
	}
	mod := uint64(1) << uint(bits)
	return engine{
		mod:  mod,
		mask: mod - 1,
		half: mod >> 1,
	}, nil
}

// modv 把任意序号折叠进模空间。
func (e engine) modv(v uint64) uint64 { return v & e.mask }

// forward 返回从 a 到 b 的前向（逆时针）距离，结果落在 [0,mod)。
func (e engine) forward(a, b uint64) uint64 { return (b - a) & e.mask }

// add 计算模空间加法。
func (e engine) add(a, b uint64) uint64 { return (a + b) & e.mask }

// sub 计算模空间减法。
func (e engine) sub(a, b uint64) uint64 { return (a - b) & e.mask }

// inWindow 判定从 rcvNxt 起算的前向差距 gap 是否落在合法窗口内。
// 窗口必须严格小于模数的一半：差距达到 half 即视为窗外旧/异序号。
func (e engine) inWindow(gap uint64) bool { return gap < e.half }

// leg 描述一条半连接（前向或后向）的接收侧状态。
type leg struct {
	state State
	// rcvNxt：下一个期望接收的序号，SYN/FIN/数据都会推进它。
	rcvNxt uint64
	// peerISN：对端在该腿上的 ISN（SYN 序号）。
	peerISN uint64
	// proxyISN：代理自身在该腿上的 ISN。
	proxyISN uint64
	// finSeen：对端 FIN 是否已被接受（FIN 消费一个序号）。
	finSeen bool
}

// Conn 是一条穿越 SYN 代理的连接状态。零值不可用，须用 New 构造。
type Conn struct {
	eng engine
	f   leg
	b   leg
}

// New 按配置创建连接。初始时前向腿进入 SYN-RCVD（代理将代答 SYN+ACK），
// 后向腿保持 CLOSED，直到客户端的最终 ACK 打通握手后才发起后端连接。
func New(c Config) (*Conn, error) {
	eng, err := newEngine(c.ModBits)
	if err != nil {
		return nil, err
	}
	frontPeer := eng.modv(c.FrontPeerISN)
	frontProxy := eng.modv(c.FrontProxyISN)
	backProxy := eng.modv(c.BackProxyISN)

	cn := &Conn{
		eng: eng,
		f: leg{
			state:    StateSynRcvd,
			rcvNxt:   eng.add(frontPeer, 1), // 消费客户端 SYN
			peerISN:  frontPeer,
			proxyISN: frontProxy,
		},
		b: leg{
			state:    StateClosed,
			proxyISN: backProxy,
			peerISN:  eng.modv(c.BackPeerISN),
		},
	}
	return cn, nil
}

// State 返回指定腿当前的状态，主要供测试与观测使用。
func (c *Conn) State(s Side) State {
	if s == Back {
		return c.b.state
	}
	return c.f.state
}

// Closed 报告连接是否已被 RST 整体拆除。
func (c *Conn) Closed() bool {
	return c.f.state == StateClosed && c.b.state == StateClosed
}

// Established 报告两条腿是否都完成握手——只有此时才允许数据转发。
func (c *Conn) Established() bool {
	return c.f.state == StateEstablished && c.b.state == StateEstablished
}

// RcvNxt 返回指定腿的下一个期望接收序号，供测试与参考模型比对。
func (c *Conn) RcvNxt(s Side) uint64 {
	if s == Back {
		return c.b.rcvNxt
	}
	return c.f.rcvNxt
}

// PeerISN 返回指定腿记录到的对端 ISN，供测试与参考模型比对。
func (c *Conn) PeerISN(s Side) uint64 {
	if s == Back {
		return c.b.peerISN
	}
	return c.f.peerISN
}

// Step 处理一个已解析事件并返回裁决。同一事件重复喂入是安全的：重传
// 不会重复推进任何状态。
func (c *Conn) Step(ev Event) Verdict {
	if c.Closed() {
		return Verdict{Reason: ReasonClosed}
	}
	if ev.Flags.Has(FlagRST) {
		if ev.Flags&(FlagSYN|FlagFIN) != 0 {
			return Verdict{Reason: ReasonBadFlags}
		}
		return c.stepRST(ev)
	}
	if ev.Flags.Has(FlagSYN) && ev.Flags.Has(FlagFIN) {
		return Verdict{Reason: ReasonBadFlags}
	}
	if ev.Side == Front {
		return c.stepFront(ev)
	}
	return c.stepBack(ev)
}

// stepFront 处理前向（客户端 -> 代理）事件。
func (c *Conn) stepFront(ev Event) Verdict {
	switch c.f.state {
	case StateSynRcvd:
		// SYN 重传：重新代答 SYN+ACK，不推进任何序号。
		if ev.Flags == FlagSYN {
			if c.eng.modv(ev.Seq) != c.f.peerISN {
				return Verdict{Reason: ReasonInvalidSeq}
			}
			return Verdict{Signals: SignalSynAckFront, Retransmit: true}
		}
		// 握手完成段：必须是 ACK，确认号精确确认代理的 SYN+ACK。
		if !ev.Flags.Has(FlagACK) || ev.Flags.Has(FlagSYN) {
			return Verdict{Reason: ReasonHandshakeIncomplete}
		}
		wantAck := c.eng.add(c.f.proxyISN, 1)
		if !ev.AckValid || c.eng.modv(ev.Ack) != wantAck {
			return Verdict{Reason: ReasonInvalidAck}
		}
		// 握手未完成期间不允许携带数据（FIN 同样消费序号，一并拒绝）。
		if ev.PayloadLen != 0 || ev.Flags.Has(FlagFIN) {
			return Verdict{Reason: ReasonHandshakeIncomplete}
		}

		// 握手打通：前向进入 ESTABLISHED，并向后向发起 SYN。
		// 最终 ACK 仅确认代理自身的 SYN+ACK，不跨边界转发。
		c.f.state = StateEstablished
		c.b.state = StateSynSent
		c.b.rcvNxt = c.b.proxyISN // SYN-SENT 下尚无对端序号
		return Verdict{Signals: SignalSynBack}

	case StateEstablished, StateClosing:
		// 已建立后又收到客户端重传的 SYN：重新代答 SYN+ACK，状态不变。
		if ev.Flags == FlagSYN {
			if c.eng.modv(ev.Seq) != c.f.peerISN {
				return Verdict{Reason: ReasonInvalidSeq}
			}
			return Verdict{Signals: SignalSynAckFront, Retransmit: true}
		}
		// 后向腿未建立时，数据一律不得转发（纯 ACK 可静默接受）。
		if c.b.state != StateEstablished && c.b.state != StateClosing {
			if ev.PayloadLen != 0 || ev.Flags.Has(FlagFIN) {
				return Verdict{Reason: ReasonHandshakeIncomplete}
			}
			return Verdict{Accept: true}
		}
		return c.stepEstablished(Front, ev)

	default:
		return Verdict{Reason: ReasonHandshakeIncomplete}
	}
}

// stepBack 处理后向（服务端 -> 代理）事件。
func (c *Conn) stepBack(ev Event) Verdict {
	switch c.b.state {
	case StateClosed:
		// 后端连接尚未发起，任何事件都没有合法上下文。
		return Verdict{Reason: ReasonHandshakeIncomplete}

	case StateSynSent:
		// 仅接受服务端的 SYN+ACK；重传的 SYN+ACK 幂等处理。
		if !ev.Flags.Has(FlagSYN) || !ev.Flags.Has(FlagACK) {
			return Verdict{Reason: ReasonHandshakeIncomplete}
		}
		wantAck := c.eng.add(c.b.proxyISN, 1)
		if !ev.AckValid || c.eng.modv(ev.Ack) != wantAck {
			return Verdict{Reason: ReasonInvalidAck}
		}
		peerISN := c.eng.modv(ev.Seq)
		if c.b.state == StateSynSent && c.b.rcvNxt == c.b.proxyISN {
			// 首个 SYN+ACK：记录服务端 ISN，消费其 SYN。
			c.b.peerISN = peerISN
			c.b.rcvNxt = c.eng.add(peerISN, 1)
			c.b.state = StateEstablished
			return Verdict{Signals: SignalAckBack, Retransmit: false}
		}
		// 重传的 SYN+ACK：重新生成最终 ACK，状态不变。
		if peerISN != c.b.peerISN {
			return Verdict{Reason: ReasonInvalidSeq}
		}
		return Verdict{Signals: SignalAckBack, Retransmit: true}

	case StateEstablished, StateClosing:
		// 已建立后又收到服务端重传的 SYN+ACK：重新产生最终 ACK。
		if ev.Flags == FlagSYN|FlagACK {
			wantAck := c.eng.add(c.b.proxyISN, 1)
			if !ev.AckValid || c.eng.modv(ev.Ack) != wantAck ||
				c.eng.modv(ev.Seq) != c.b.peerISN {
				return Verdict{Reason: ReasonInvalidSeq}
			}
			return Verdict{Signals: SignalAckBack, Retransmit: true}
		}
		return c.stepEstablished(Back, ev)

	default:
		return Verdict{Reason: ReasonHandshakeIncomplete}
	}
}

// stepEstablished 处理两条腿都建立后的通用数据/ACK/FIN 语义。
func (c *Conn) stepEstablished(side Side, ev Event) Verdict {
	l := c.leg(side)

	if ev.Flags.Has(FlagSYN) {
		// 正常路径下 SYN+ACK 重传已在 SYN-SENT 分支处理；建立后再出现
		// SYN 属于非法标志组合。
		return Verdict{Reason: ReasonBadFlags}
	}
	if ev.Flags == 0 {
		return Verdict{Reason: ReasonBadFlags}
	}

	// 纯 ACK：不占用序号空间，直接映射确认号后转发。
	if ev.PayloadLen == 0 && !ev.Flags.Has(FlagFIN) {
		v := Verdict{Accept: true}
		c.fillXlate(&v, side, ev, c.eng.modv(ev.Seq))
		return v
	}

	seq := c.eng.modv(ev.Seq)
	gap := c.eng.forward(l.rcvNxt, seq)

	// 差距达到半个模数：属于窗外的旧/异序号段。
	if !c.eng.inWindow(gap) {
		// 完全落在已接收空间内的重传：允许转发，但不推进状态。
		if c.isRetransmit(l, seq, ev) {
			v := Verdict{Accept: true, Retransmit: true}
			c.fillXlate(&v, side, ev, seq)
			return v
		}
		return Verdict{Reason: ReasonInvalidSeq}
	}

	// FIN 已接受后，任何窗内新数据/FIN 都不再合法。
	if l.finSeen {
		return Verdict{Reason: ReasonInvalidSeq}
	}

	span := uint64(ev.PayloadLen)
	if ev.Flags.Has(FlagFIN) {
		span++ // FIN 消费一个序号
	}

	// 部分重叠或乱序（起点超前、或跨过 rcvNxt 却不是重传）不直接转发。
	if gap != 0 {
		return Verdict{Reason: ReasonOutOfWindow}
	}

	// 按序段：推进 rcvNxt，数据字节与 FIN 各自消费序号。
	l.rcvNxt = c.eng.add(l.rcvNxt, span)
	if ev.Flags.Has(FlagFIN) {
		l.finSeen = true
		l.state = StateClosing
	}

	v := Verdict{Accept: true}
	c.fillXlate(&v, side, ev, seq)
	return v
}

// isRetransmit 判定一个窗外序号段是否完全落在已接收的序号空间内。
//
// 调用方已确认 gap（rcvNxt 到 seq 的前向距离）达到半个模数，即 seq
// 落在 rcvNxt 身后。此时 seq 到 rcvNxt 的前向距离 back 就是该段覆盖过
// 的序号量；只要段跨度不超过 back，整段（含可能的 FIN）都已接收过，
// 属于幂等重传。back==0 表示 seq 恰好等于 rcvNxt，按新段路径处理。
func (c *Conn) isRetransmit(l *leg, seq uint64, ev Event) bool {
	back := c.eng.forward(seq, l.rcvNxt)
	// back 必须落在前向半窗内，才说明 seq 确实位于 rcvNxt 身后。
	if back == 0 || !c.eng.inWindow(back) {
		return false
	}
	span := uint64(ev.PayloadLen)
	if ev.Flags.Has(FlagFIN) {
		span++
	}
	return span <= back
}

// stepRST 处理复位。可接受的 RST 拆除整条连接，并指示代理向另一侧发 RST。
func (c *Conn) stepRST(ev Event) Verdict {
	if ev.Side == Front {
		if !c.rstAcceptable(&c.f, ev) {
			return Verdict{Reason: ReasonInvalidSeq}
		}
		c.f.state = StateClosed
		c.b.state = StateClosed
		return Verdict{Signals: SignalRstBack}
	}
	if !c.rstAcceptable(&c.b, ev) {
		return Verdict{Reason: ReasonInvalidSeq}
	}
	c.f.state = StateClosed
	c.b.state = StateClosed
	return Verdict{Signals: SignalRstFront}
}

// rstAcceptable 实现 RST 可接受性检查。
//
// 已建立/关闭中：RST 序号落在以 rcvNxt 为界的前向半空间内即可
// （含恰好等于 rcvNxt，以及重传 SYN 位置上的 RST）。
// SYN-SENT：按 RFC 793，依据 ACK 号是否确认本端发出的 SYN 判定。
func (c *Conn) rstAcceptable(l *leg, ev Event) bool {
	seq := c.eng.modv(ev.Seq)
	switch l.state {
	case StateSynSent:
		wantAck := c.eng.add(l.proxyISN, 1)
		if ev.AckValid && c.eng.modv(ev.Ack) == wantAck {
			return true
		}
		return !ev.AckValid
	case StateEstablished, StateClosing:
		if seq == l.peerISN {
			return true
		}
		return c.eng.inWindow(c.eng.forward(l.rcvNxt, seq))
	case StateSynRcvd:
		return seq == l.peerISN || c.eng.inWindow(c.eng.forward(l.rcvNxt, seq))
	default:
		return false
	}
}

// leg 按侧取对应半连接。
func (c *Conn) leg(s Side) *leg {
	if s == Back {
		return &c.b
	}
	return &c.f
}

// fillXlate 依据两侧 ISN 计算转发报文在对侧序号空间中的 SEQ 与 ACK。
//
// 映射是保持顺序的固定偏移平移，SYN 位置严格对齐，因此数据、FIN 的
// 相对位置与 ACK 确认的进度都能无损衔接。所有运算在模空间内完成。
func (c *Conn) fillXlate(v *Verdict, side Side, ev Event, seq uint64) {
	v.Xlate.Seq = seq
	if ev.AckValid {
		v.Xlate.AckValid = true
		v.Xlate.Ack = c.eng.modv(ev.Ack)
	}
	if side == Front {
		// 前向 -> 后向：客户端序号空间映射到代理后向发送空间。
		off := c.eng.sub(c.b.proxyISN, c.f.peerISN)
		v.Xlate.Seq = c.eng.add(seq, off)
		if ev.AckValid {
			ackOff := c.eng.sub(c.b.peerISN, c.f.proxyISN)
			v.Xlate.Ack = c.eng.add(c.eng.modv(ev.Ack), ackOff)
		}
		return
	}
	// 后向 -> 前向：服务端序号空间映射到代理前向发送空间。
	off := c.eng.sub(c.f.proxyISN, c.b.peerISN)
	v.Xlate.Seq = c.eng.add(seq, off)
	if ev.AckValid {
		ackOff := c.eng.sub(c.f.peerISN, c.b.proxyISN)
		v.Xlate.Ack = c.eng.add(c.eng.modv(ev.Ack), ackOff)
	}
}
