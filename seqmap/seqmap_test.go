package seqmap

import (
	"testing"
)

// 小模数参考模型：用独立的纯算术实现维护“期望”序号空间，
// 与状态机内部实现完全解耦，用于交叉核对 SEQ/ACK 映射与 rcvNxt。

func refMod(v int64, m uint64) uint64 {
	r := v % int64(m)
	if r < 0 {
		r += int64(m)
	}
	return uint64(r)
}

// refXlate 是参考模型的序号/确认号映射公式。
// isn 依次为：前向对端ISN、前向代理ISN、后向代理ISN、后向对端ISN。
func refXlate(bits int, isn [4]uint64, side Side, seq, ack uint64, ackValid bool) Xlate {
	m := uint64(1) << bits
	fpISN, fpxISN := isn[0], isn[1]
	bpxISN, bpISN := isn[2], isn[3]

	x := Xlate{Seq: seq}
	if side == Front {
		off := refMod(int64(bpxISN)-int64(fpISN), m)
		x.Seq = refMod(int64(seq)+int64(off), m)
		if ackValid {
			ao := refMod(int64(bpISN)-int64(fpxISN), m)
			x.AckValid = true
			x.Ack = refMod(int64(ack)+int64(ao), m)
		}
		return x
	}
	off := refMod(int64(fpxISN)-int64(bpISN), m)
	x.Seq = refMod(int64(seq)+int64(off), m)
	if ackValid {
		ao := refMod(int64(fpISN)-int64(bpxISN), m)
		x.AckValid = true
		x.Ack = refMod(int64(ack)+int64(ao), m)
	}
	return x
}

// harness 在参考模型之外额外维护两侧期望的 rcvNxt。
type harness struct {
	t      *testing.T
	c      *Conn
	bits   int
	mod    uint64
	isn    [4]uint64
	expF   uint64 // 前向期望 rcvNxt
	expB   uint64 // 后向期望 rcvNxt
	bKnown bool   // 后向对端 ISN 是否已记录
}

func newHarness(t *testing.T, bits int, isn [4]uint64) *harness {
	t.Helper()
	m := uint64(1) << bits
	c, err := New(Config{
		ModBits:       bits,
		FrontPeerISN:  isn[0],
		FrontProxyISN: isn[1],
		BackProxyISN:  isn[2],
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{
		t:    t,
		c:    c,
		bits: bits,
		mod:  m,
		isn:  isn,
		expF: (isn[0] + 1) % m,
	}
}

func (h *harness) checkRcvNxt() {
	h.t.Helper()
	if got := h.c.RcvNxt(Front); got != h.expF {
		h.t.Fatalf("front rcvNxt = %d, 期望 %d", got, h.expF)
	}
	if h.bKnown {
		if got := h.c.RcvNxt(Back); got != h.expB {
			h.t.Fatalf("back rcvNxt = %d, 期望 %d", got, h.expB)
		}
	}
}

// checkXlate 用参考模型核对裁决中的映射结果。
func (h *harness) checkXlate(side Side, ev Event, v Verdict) {
	h.t.Helper()
	want := refXlate(h.bits, h.isn, side, ev.Seq%h.mod, ev.Ack%h.mod, ev.AckValid)
	if v.Xlate.Seq != want.Seq {
		h.t.Fatalf("%s SEQ 映射 = %d, 参考模型期望 %d (事件 %+v)", side, v.Xlate.Seq, want.Seq, ev)
	}
	if v.Xlate.AckValid != want.AckValid {
		h.t.Fatalf("%s ACK 有效位 = %v, 期望 %v", side, v.Xlate.AckValid, want.AckValid)
	}
	if want.AckValid && v.Xlate.Ack != want.Ack {
		h.t.Fatalf("%s ACK 映射 = %d, 参考模型期望 %d", side, v.Xlate.Ack, want.Ack)
	}
}

func (h *harness) accept(ev Event) Verdict {
	h.t.Helper()
	v := h.c.Step(ev)
	if !v.Accept || v.Reason != ReasonOK {
		h.t.Fatalf("事件 %+v 被拒绝: reason=%s", ev, v.Reason)
	}
	return v
}

func (h *harness) reject(ev Event, want Reason) {
	h.t.Helper()
	v := h.c.Step(ev)
	if v.Accept {
		h.t.Fatalf("事件 %+v 不应被接受", ev)
	}
	if v.Reason != want {
		h.t.Fatalf("事件 %+v reason=%s, 期望 %s", ev, v.Reason, want)
	}
}

// advance 参考模型按“已接受的按序段”推进期望 rcvNxt。
func (h *harness) advance(side Side, ev Event) {
	n := uint64(ev.PayloadLen)
	if ev.Flags.Has(FlagFIN) {
		n++
	}
	if side == Front {
		h.expF = (h.expF + n) % h.mod
	} else {
		h.expB = (h.expB + n) % h.mod
	}
}

// establish 驱动一次完整握手，结束后两条腿都处于 ESTABLISHED。
func (h *harness) establish(backPeerISN uint64) {
	h.t.Helper()
	h.isn[3] = backPeerISN % h.mod

	// 客户端最终 ACK：触发后向 SYN。
	v := h.c.Step(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      h.isn[0] + 1,
		Ack:      h.isn[1] + 1,
		AckValid: true,
	})
	if !v.Signals.Has(SignalSynBack) || h.c.State(Back) != StateSynSent {
		h.t.Fatalf("最终 ACK 未触发后向 SYN: %+v", v)
	}

	// 服务端 SYN+ACK：触发后向最终 ACK，后端建立。
	v = h.c.Step(Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      h.isn[3],
		Ack:      h.isn[2] + 1,
		AckValid: true,
	})
	if !v.Signals.Has(SignalAckBack) {
		h.t.Fatalf("SYN+ACK 未触发后向 ACK: %+v", v)
	}
	h.bKnown = true
	h.expB = (h.isn[3] + 1) % h.mod
	h.checkRcvNxt()
	if !h.c.Established() {
		h.t.Fatal("握手后连接未处于 Established")
	}
}
