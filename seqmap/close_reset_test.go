package seqmap

import "testing"

// TestSimultaneousClose 两侧独立发送 FIN，各自消费一个序号并进入 closing，
// 映射后的 FIN 位置必须与数据流严格对齐；FIN 之后的新数据被拒绝。
func TestSimultaneousClose(t *testing.T) {
	const bits = 6
	isn := [4]uint64{10, 40, 20, 50}
	h := newHarness(t, bits, isn)
	h.establish(50)

	// 前向先发 3 字节数据再 FIN。
	fdata := Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        h.expF,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 3,
	}
	fv := h.accept(fdata)
	h.checkXlate(Front, fdata, fv)
	h.advance(Front, fdata)

	ffin := Event{
		Side:     Front,
		Flags:    FlagACK | FlagFIN,
		Seq:      h.expF,
		Ack:      (isn[1] + 1) % h.mod,
		AckValid: true,
	}
	ffv := h.accept(ffin)
	h.checkXlate(Front, ffin, ffv)
	h.advance(Front, ffin)
	h.checkRcvNxt()
	if h.c.State(Front) != StateClosing {
		t.Fatalf("前向状态 = %s, 期望 closing", h.c.State(Front))
	}

	// 后向并发独立关闭：2 字节数据后 FIN。
	bdata := Event{
		Side:       Back,
		Flags:      FlagACK,
		Seq:        h.expB,
		Ack:        (isn[2] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 2,
	}
	bv := h.accept(bdata)
	h.checkXlate(Back, bdata, bv)
	h.advance(Back, bdata)

	bfin := Event{
		Side:     Back,
		Flags:    FlagACK | FlagFIN,
		Seq:      h.expB,
		Ack:      (isn[2] + 1) % h.mod,
		AckValid: true,
	}
	bfv := h.accept(bfin)
	h.checkXlate(Back, bfin, bfv)
	h.advance(Back, bfin)
	h.checkRcvNxt()
	if h.c.State(Back) != StateClosing {
		t.Fatalf("后向状态 = %s, 期望 closing", h.c.State(Back))
	}

	// FIN 之后再发新数据必须拒绝。
	h.reject(Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        h.expF,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonInvalidSeq)

	// 两条 FIN 的重传仍可转发，且不推进 rcvNxt。
	frv := h.c.Step(ffin)
	if !frv.Accept || !frv.Retransmit {
		t.Fatalf("前向 FIN 重传裁决错误: %+v", frv)
	}
	brv := h.c.Step(bfin)
	if !brv.Accept || !brv.Retransmit {
		t.Fatalf("后向 FIN 重传裁决错误: %+v", brv)
	}
	h.checkRcvNxt()
}

// TestReset 覆盖握手期与建立后的 RST 语义。
func TestReset(t *testing.T) {
	t.Run("established-valid-rst", func(t *testing.T) {
		isn := [4]uint64{10, 40, 20, 0}
		h := newHarness(t, 6, isn)
		h.establish(50)

		// 前向合法 RST：拆除连接并要求向后向发 RST。
		v := h.c.Step(Event{Side: Front, Flags: FlagRST, Seq: h.expF})
		if v.Signals != SignalRstBack || !h.c.Closed() {
			t.Fatalf("合法 RST 裁决错误: %+v closed=%v", v, h.c.Closed())
		}
		// 拆除后所有事件直接丢弃。
		h.reject(Event{Side: Front, Flags: FlagRST, Seq: h.expF}, ReasonClosed)
	})

	t.Run("established-invalid-rst", func(t *testing.T) {
		isn := [4]uint64{10, 40, 20, 0}
		h := newHarness(t, 6, isn)
		h.establish(50)

		// 差距达到 half(32) 的 RST 不可接受，连接保持。
		bad := (h.expF + 32) % h.mod
		h.reject(Event{Side: Front, Flags: FlagRST, Seq: bad}, ReasonInvalidSeq)
		if h.c.Closed() {
			t.Fatal("非法 RST 不应拆除连接")
		}
		// RST 与其他标志组合非法。
		h.reject(Event{Side: Front, Flags: FlagRST | FlagFIN, Seq: h.expF}, ReasonBadFlags)
	})

	t.Run("back-rst-signals-front-rst", func(t *testing.T) {
		isn := [4]uint64{10, 40, 20, 0}
		h := newHarness(t, 6, isn)
		h.establish(50)

		v := h.c.Step(Event{Side: Back, Flags: FlagRST, Seq: h.expB})
		if v.Signals != SignalRstFront || !h.c.Closed() {
			t.Fatalf("后向 RST 裁决错误: %+v", v)
		}
	})

	t.Run("rst-in-syn-sent", func(t *testing.T) {
		isn := [4]uint64{10, 40, 20, 0}
		h := newHarness(t, 6, isn)
		// 只完成前向，后向停在 SYN-SENT。
		ack := h.c.Step(Event{
			Side:     Front,
			Flags:    FlagACK,
			Seq:      isn[0] + 1,
			Ack:      isn[1] + 1,
			AckValid: true,
		})
		if !ack.Signals.Has(SignalSynBack) || h.c.State(Back) != StateSynSent {
			t.Fatalf("最终 ACK 裁决错误: %+v", ack)
		}

		// ACK 号确认代理 SYN 的 RST 可接受。
		v := h.c.Step(Event{
			Side:     Back,
			Flags:    FlagRST | FlagACK,
			Seq:      5,
			Ack:      isn[2] + 1,
			AckValid: true,
		})
		if v.Signals != SignalRstFront || !h.c.Closed() {
			t.Fatalf("SYN-SENT RST 裁决错误: %+v", v)
		}
	})

	t.Run("rst-at-syn-position", func(t *testing.T) {
		isn := [4]uint64{10, 40, 20, 0}
		h := newHarness(t, 6, isn)
		h.establish(50)

		// 落在对端 ISN（SYN 位置）上的 RST 也可接受。
		v := h.c.Step(Event{Side: Front, Flags: FlagRST, Seq: isn[0]})
		if v.Signals != SignalRstBack || !h.c.Closed() {
			t.Fatalf("SYN 位置 RST 裁决错误: %+v", v)
		}
	})
}
