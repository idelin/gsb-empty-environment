package sim

// Sender 实现明确的慢启动、拥塞避免与超时回退模型。
//
// 不变式：
//   - 重复/旧 ACK 经过去重，绝不增大有效窗口，也绝不触碰重传计时器；
//   - 累计 ACK 跳跃被钳制到 sndNxt，只确认真实未确认字节；
//   - inflight 与可用窗口永不为负；
//   - 超时按规则回退：ssthresh=cwnd/2，cwnd=1 MSS，RTO 指数退避，回退重传。
type Sender struct {
	sched *Scheduler
	link  *Link
	log   *Logger

	MSS      int
	DataLen  int
	Cwnd     int
	Ssthresh int
	RTO      int // 当前 RTO（tick）
	MaxRTO   int

	SndUna int
	SndNxt int

	onDataDeliver func(Packet) // 由仿真框架填充，把数据包交给接收方

	timerActive   bool
	timerDeadline int
	timerGen      int // 代数计数，使旧定时器事件失效

	DupAcks  int
	Timeouts int
	Done     bool

	cwndTrace []int // 每次窗口变化后的 cwnd，供测试与演示
}

func NewSender(sched *Scheduler, link *Link, log *Logger, mss, dataLen, initCwnd, ssthresh, rto, maxRTO int) *Sender {
	s := &Sender{
		sched: sched, link: link, log: log,
		MSS: mss, DataLen: dataLen,
		Cwnd: initCwnd, Ssthresh: ssthresh,
		RTO: rto, MaxRTO: maxRTO,
		onDataDeliver: func(Packet) {},
	}
	s.cwndTrace = append(s.cwndTrace, s.Cwnd)
	return s
}

// Inflight 返回在途未确认字节数，永不为负。
func (s *Sender) Inflight() int {
	n := s.SndNxt - s.SndUna
	if n < 0 {
		return 0
	}
	return n
}

// Avail 返回有效可用窗口（字节），永不为负。
func (s *Sender) Avail() int {
	a := s.Cwnd - s.Inflight()
	if a < 0 {
		return 0
	}
	return a
}

// CwndTrace 返回 cwnd 变化轨迹。
func (s *Sender) CwndTrace() []int { return append([]int(nil), s.cwndTrace...) }

// TimerDeadline 返回当前重传计时器的到期 tick（-1 表示未启动）。
func (s *Sender) TimerDeadline() int {
	if !s.timerActive {
		return -1
	}
	return s.timerDeadline
}

// Start 开始发送。
func (s *Sender) Start() { s.trySend() }

func (s *Sender) trySend() {
	for s.SndNxt < s.DataLen {
		segLen := s.MSS
		if rem := s.DataLen - s.SndNxt; rem < segLen {
			segLen = rem
		}
		if s.Avail() < segLen {
			return
		}
		p := Packet{Seq: s.SndNxt, Len: segLen}
		s.SndNxt += segLen
		s.log.Logf("send: seq=%d len=%d cwnd=%d", p.Seq, p.Len, s.Cwnd)
		s.link.Send(p, s.onDataDeliver)
		if !s.timerActive {
			s.armTimer()
		}
	}
}

// OnAck 处理到达的 ACK 包。这是 ACK 去重的唯一入口：
// 不推进 sndUna 的 ACK 一律视为重复/旧 ACK，只计数，不改窗口、不动计时器。
func (s *Sender) OnAck(p Packet) {
	if s.Done || !p.Ack {
		return
	}
	ackNum := p.AckNum

	if ackNum <= s.SndUna {
		// 重复或旧 ACK：去重。窗口与重传计时器均不受影响。
		s.DupAcks++
		s.log.Logf("ack: dup/old ack=%d ignored (sndUna=%d)", ackNum, s.SndUna)
		return
	}
	if ackNum > s.SndNxt {
		// 累计 ACK 跳跃：钳制到 sndNxt，只确认真实未确认字节。
		s.log.Logf("ack: ack=%d beyond sndNxt=%d, clamped", ackNum, s.SndNxt)
		ackNum = s.SndNxt
	}

	newly := ackNum - s.SndUna
	s.SndUna = ackNum
	s.DupAcks = 0
	s.log.Logf("ack: ack=%d newly=%d sndUna=%d", ackNum, newly, s.SndUna)

	// 窗口增长：慢启动每 ACK 一个 MSS；拥塞避免每 ACK MSS*MSS/cwnd。
	if s.Cwnd < s.Ssthresh {
		s.Cwnd += s.MSS
		if s.Cwnd > s.Ssthresh {
			s.Cwnd = s.Ssthresh
		}
	} else {
		inc := s.MSS * s.MSS / s.Cwnd
		if inc < 1 {
			inc = 1
		}
		s.Cwnd += inc
	}
	s.cwndTrace = append(s.cwndTrace, s.Cwnd)

	// 重传计时与 ACK 去重分离：只有真实推进的 ACK 才影响计时器。
	if s.SndUna >= s.DataLen && s.SndNxt >= s.DataLen {
		s.timerActive = false
		s.Done = true
		s.log.Logf("send: complete, %d bytes acked", s.SndUna)
	} else {
		s.armTimer() // 有新进展，重启计时
	}
	s.trySend()
}

func (s *Sender) armTimer() {
	s.timerActive = true
	s.timerGen++
	s.timerDeadline = s.sched.Now() + s.RTO
	gen := s.timerGen
	s.sched.At(s.timerDeadline, func() { s.onTimeout(gen) })
}

func (s *Sender) onTimeout(gen int) {
	if !s.timerActive || gen != s.timerGen {
		return // 旧代数或已取消的定时器事件
	}
	s.timerActive = false
	s.Timeouts++

	// 超时回退规则。
	s.Ssthresh = s.Cwnd / 2
	if s.Ssthresh < 2*s.MSS {
		s.Ssthresh = 2 * s.MSS
	}
	s.Cwnd = s.MSS
	s.cwndTrace = append(s.cwndTrace, s.Cwnd)
	s.RTO *= 2
	if s.RTO > s.MaxRTO {
		s.RTO = s.MaxRTO
	}
	s.DupAcks = 0
	s.SndNxt = s.SndUna // 回退重传：从最早未确认字节重发
	s.log.Logf("timeout: backoff cwnd=%d ssthresh=%d rto=%d retransmit-from=%d",
		s.Cwnd, s.Ssthresh, s.RTO, s.SndNxt)

	s.armTimer()
	s.trySend()
}
