package sim

import (
	"strings"
	"testing"
)

const testMSS = 1000

func newUnitSender() (*Scheduler, *Sender) {
	sched := NewScheduler()
	link := NewLink(sched, 5, nil)
	log := NewLogger(sched, false)
	s := NewSender(sched, link, log, testMSS, 100*testMSS, testMSS, 64*testMSS, 40, 320)
	return sched, s
}

// 慢启动：每个新 ACK 使 cwnd 增加一个 MSS，每 RTT 翻倍，直到 ssthresh。
func TestSlowStartGrowth(t *testing.T) {
	s := New(Config{
		MSS: testMSS, DataLen: 64 * testMSS,
		InitCwnd: testMSS, Ssthresh: 8 * testMSS,
		RTO: 1000, MaxRTO: 1000, LinkDelay: 5,
	})
	s.Run(100000)
	if !s.Sender.Done {
		t.Fatal("transfer should complete")
	}
	trace := s.Sender.CwndTrace()
	want := []int{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}
	if len(trace) < len(want) {
		t.Fatalf("trace too short: %v", trace)
	}
	for i, w := range want {
		if trace[i] != w {
			t.Fatalf("slow start trace[%d]=%d, want %d (trace=%v)", i, trace[i], w, trace)
		}
	}
	// 到达 ssthresh 后不得继续按 MSS 增长。
	if trace[len(want)] != 8000+testMSS*testMSS/8000 {
		t.Fatalf("after ssthresh should enter congestion avoidance, trace=%v", trace)
	}
}

// 拥塞避免：cwnd >= ssthresh 后每 ACK 增加 MSS*MSS/cwnd（加性增）。
func TestCongestionAvoidanceAdditive(t *testing.T) {
	_, s := newUnitSender()
	s.Ssthresh = 2 * testMSS
	s.Cwnd = 2 * testMSS
	s.SndUna = 0
	s.SndNxt = 4 * testMSS // 4 段在途

	prev := s.Cwnd
	for i := 1; i <= 4; i++ {
		s.OnAck(Packet{Ack: true, AckNum: i * testMSS})
		wantInc := testMSS * testMSS / prev
		if got := s.Cwnd - prev; got != wantInc {
			t.Fatalf("ack %d: cwnd inc=%d, want additive %d", i, got, wantInc)
		}
		if s.Cwnd-prev >= testMSS {
			t.Fatalf("ack %d: congestion avoidance must grow slower than 1 MSS/ack", i)
		}
		prev = s.Cwnd
	}
}

// 超时回退规则：ssthresh=cwnd/2（下限 2 MSS），cwnd=1 MSS，RTO 指数退避且有上限。
func TestTimeoutBackoffRules(t *testing.T) {
	dropAll := func(Packet, int) bool { return true }
	s := New(Config{
		MSS: testMSS, DataLen: 64 * testMSS,
		InitCwnd: 4 * testMSS, Ssthresh: 64 * testMSS,
		RTO: 40, MaxRTO: 320, LinkDelay: 5,
		DropData: dropAll,
	})
	s.Run(100000)

	if s.Sender.Timeouts < 4 {
		t.Fatalf("expected several timeouts, got %d", s.Sender.Timeouts)
	}
	if s.Sender.Cwnd != testMSS {
		t.Fatalf("cwnd after timeout = %d, want 1 MSS", s.Sender.Cwnd)
	}
	if s.Sender.Ssthresh != 2*testMSS {
		t.Fatalf("ssthresh = %d, want max(cwnd/2, 2 MSS) = %d", s.Sender.Ssthresh, 2*testMSS)
	}
	if s.Sender.RTO != 320 {
		t.Fatalf("rto = %d, want capped exponential backoff 40*2^n -> 320", s.Sender.RTO)
	}
	if s.Sender.SndNxt != s.Sender.SndUna+testMSS {
		t.Fatalf("after timeout retransmit from sndUna: sndNxt=%d sndUna=%d",
			s.Sender.SndNxt, s.Sender.SndUna)
	}
}

// 重复/旧 ACK 去重：不增大 cwnd，不增大有效窗口。
func TestDupAckDoesNotGrowWindow(t *testing.T) {
	_, s := newUnitSender()
	s.Cwnd = 4 * testMSS
	s.SndUna = 1000
	s.SndNxt = 5000
	availBefore := s.Avail()

	for i := 0; i < 5; i++ {
		s.OnAck(Packet{Ack: true, AckNum: 500})  // 旧 ACK
		s.OnAck(Packet{Ack: true, AckNum: 1000}) // 重复 ACK（== sndUna）
	}
	if s.Cwnd != 4*testMSS {
		t.Fatalf("dup/old ACK changed cwnd to %d", s.Cwnd)
	}
	if s.Avail() != availBefore {
		t.Fatalf("dup/old ACK changed avail window: %d -> %d", availBefore, s.Avail())
	}
	if s.DupAcks != 10 {
		t.Fatalf("dup acks counted = %d, want 10", s.DupAcks)
	}
}

// 累计 ACK 跳跃：钳制到 sndNxt，只确认真实未确认字节。
func TestCumulativeAckClampedToRealBytes(t *testing.T) {
	_, s := newUnitSender()
	s.SndUna = 1000
	s.SndNxt = 5000

	s.OnAck(Packet{Ack: true, AckNum: 99999})
	if s.SndUna != 5000 {
		t.Fatalf("sndUna = %d, want clamped to sndNxt 5000", s.SndUna)
	}
	if s.SndUna > s.SndNxt {
		t.Fatalf("sndUna %d exceeds sndNxt %d", s.SndUna, s.SndNxt)
	}
}

// 旧 ACK 不使窗口为负：inflight 与 avail 始终 >= 0。
func TestOldAckNeverNegativeWindow(t *testing.T) {
	_, s := newUnitSender()
	s.Cwnd = testMSS
	s.SndUna = 3000
	s.SndNxt = 3000

	for _, ack := range []int{0, 500, 1000, 2999, 3000} {
		s.OnAck(Packet{Ack: true, AckNum: ack})
		if s.Inflight() < 0 || s.Avail() < 0 {
			t.Fatalf("negative window after old ack %d: inflight=%d avail=%d",
				ack, s.Inflight(), s.Avail())
		}
	}
	if s.SndUna != 3000 || s.Cwnd != testMSS {
		t.Fatalf("old ACKs mutated state: sndUna=%d cwnd=%d", s.SndUna, s.Cwnd)
	}
}

// ACK 去重与重传计时分离：重复 ACK 不重置、不取消重传计时器，
// 超时仍在原定 tick 触发。
func TestDupAckDoesNotTouchRetransmitTimer(t *testing.T) {
	sched := NewScheduler()
	link := NewLink(sched, 5, nil)
	log := NewLogger(sched, true)
	s := NewSender(sched, link, log, testMSS, 100*testMSS, testMSS, 64*testMSS, 40, 320)

	s.Start() // tick 0：发出首段并启动计时器
	deadline := s.TimerDeadline()
	if deadline != 40 {
		t.Fatalf("timer deadline = %d, want 40", deadline)
	}
	// tick 10 注入一批重复 ACK。
	sched.At(10, func() {
		for i := 0; i < 3; i++ {
			s.OnAck(Packet{Ack: true, AckNum: 0})
		}
	})
	sched.Run(100) // 只覆盖第一次超时（tick 40），第二次在 tick 120

	if s.Timeouts != 1 {
		t.Fatalf("timeouts = %d, want exactly 1", s.Timeouts)
	}
	if s.DupAcks != 0 {
		t.Fatalf("timeout resets dup ack counter, got %d", s.DupAcks)
	}
	found := false
	for _, line := range log.Lines {
		if strings.Contains(line, "timeout") {
			if !strings.HasPrefix(line, "tick=40") {
				t.Fatalf("timeout fired at wrong tick: %q (dup ACKs must not shift timer)", line)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no timeout logged")
	}
}

// 无丢包完整传输：全部字节按序交付并确认。
func TestFullTransferInOrder(t *testing.T) {
	s := New(Config{
		MSS: testMSS, DataLen: 10 * testMSS,
		InitCwnd: testMSS, Ssthresh: 64 * testMSS,
		RTO: 1000, MaxRTO: 1000, LinkDelay: 5,
	})
	s.Run(100000)
	if !s.Sender.Done {
		t.Fatal("transfer incomplete")
	}
	if s.Receiver.Delivered != 10*testMSS || s.Sender.SndUna != 10*testMSS {
		t.Fatalf("delivered=%d sndUna=%d, want %d",
			s.Receiver.Delivered, s.Sender.SndUna, 10*testMSS)
	}
	if s.Sender.Timeouts != 0 || s.Receiver.DupAcksSent != 0 {
		t.Fatalf("clean run should have no timeouts/dup acks: timeouts=%d dupAcks=%d",
			s.Sender.Timeouts, s.Receiver.DupAcksSent)
	}
}

// 丢包恢复：失序段触发重复 ACK（不增窗），超时回退后重传完成传输。
func TestLossRecovery(t *testing.T) {
	seen := map[int]int{}
	dropOnce := func(p Packet, tick int) bool {
		seen[p.Seq]++
		return p.Seq == 2000 && seen[p.Seq] == 1
	}
	s := New(Config{
		MSS: testMSS, DataLen: 8 * testMSS,
		InitCwnd: testMSS, Ssthresh: 64 * testMSS,
		RTO: 40, MaxRTO: 320, LinkDelay: 5,
		DropData: dropOnce,
	})
	s.Run(100000)

	if !s.Sender.Done || s.Receiver.Delivered != 8*testMSS {
		t.Fatalf("transfer incomplete: done=%v delivered=%d", s.Sender.Done, s.Receiver.Delivered)
	}
	if s.Sender.Timeouts < 1 {
		t.Fatal("expected at least one timeout for the lost segment")
	}
	if s.Receiver.DupAcksSent == 0 {
		t.Fatal("out-of-order segments should trigger duplicate ACKs")
	}
	// 超时后 cwnd 必须回退到 1 MSS，且出现在轨迹中。
	trace := s.Sender.CwndTrace()
	backedOff := false
	for _, w := range trace {
		if w == testMSS {
			backedOff = true
		}
		if w < testMSS {
			t.Fatalf("cwnd dropped below 1 MSS: trace=%v", trace)
		}
	}
	if !backedOff {
		t.Fatalf("cwnd never backed off to 1 MSS: trace=%v", trace)
	}
}

// 不变式压测：随机化的确定性丢包下，全程 sndUna <= sndNxt、窗口非负。
func TestInvariantsUnderLoss(t *testing.T) {
	drop := func(p Packet, tick int) bool {
		// 确定性伪随机：丢弃约 1/7 的数据段。
		return !p.Ack && (p.Seq/testMSS+tick)%7 == 3
	}
	s := New(Config{
		MSS: testMSS, DataLen: 32 * testMSS,
		InitCwnd: 2 * testMSS, Ssthresh: 16 * testMSS,
		RTO: 30, MaxRTO: 240, LinkDelay: 4,
		DropData: drop,
	})
	s.Run(1000000)
	if s.Sender.SndUna > s.Sender.SndNxt {
		t.Fatalf("sndUna %d > sndNxt %d", s.Sender.SndUna, s.Sender.SndNxt)
	}
	if s.Sender.Avail() < 0 || s.Sender.Inflight() < 0 {
		t.Fatal("negative window")
	}
	if !s.Sender.Done {
		t.Fatalf("transfer did not complete: sndUna=%d dataLen=%d", s.Sender.SndUna, 32*testMSS)
	}
	if s.Receiver.Delivered != 32*testMSS {
		t.Fatalf("delivered=%d, want %d", s.Receiver.Delivered, 32*testMSS)
	}
}
