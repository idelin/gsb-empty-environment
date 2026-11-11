package seqmap

import "testing"

func TestNewErrors(t *testing.T) {
	for _, bits := range []int{0, 3, 33, -1} {
		if _, err := New(Config{ModBits: bits}); err == nil {
			t.Fatalf("ModBits=%d 应当报错", bits)
		}
	}
}

func TestHandshake32(t *testing.T) {
	// 32 位真实模数，ISN 选在靠近回绕边界以验证 uint32 语义。
	isn := [4]uint64{0xFFFFFFF0, 0x10, 0x20, 0}
	h := newHarness(t, 32, isn)

	if h.c.State(Front) != StateSynRcvd {
		t.Fatalf("初始前向状态 = %s", h.c.State(Front))
	}
	if h.c.RcvNxt(Front) != 0xFFFFFFF1 {
		t.Fatalf("SYN 未正确消费序号: rcvNxt=%#x", h.c.RcvNxt(Front))
	}

	h.establish(0xFFFFFFE0)

	// 建立后转发一段跨 0 回绕的数据，核对 SEQ/ACK 映射。
	ev := Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        0xFFFFFFF1,
		Ack:        0x11,
		AckValid:   true,
		PayloadLen: 32,
	}
	v := h.accept(ev)
	h.advance(Front, ev)
	h.checkRcvNxt()
	h.checkXlate(Front, ev, v)

	bev := Event{
		Side:       Back,
		Flags:      FlagACK,
		Seq:        0xFFFFFFE1,
		Ack:        0x21,
		AckValid:   true,
		PayloadLen: 5,
	}
	bv := h.accept(bev)
	h.advance(Back, bev)
	h.checkRcvNxt()
	h.checkXlate(Back, bev, bv)
}

func TestDataRejectedBeforeEstablished(t *testing.T) {
	isn := [4]uint64{100, 200, 300, 0}
	h := newHarness(t, 6, isn)

	// 握手完成前，前向数据与 FIN 一律拒绝。
	h.reject(Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        101,
		Ack:        201,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonHandshakeIncomplete)
	h.reject(Event{
		Side:     Front,
		Flags:    FlagACK | FlagFIN,
		Seq:      101,
		Ack:      201,
		AckValid: true,
	}, ReasonHandshakeIncomplete)

	// 错误确认号无法完成握手。
	h.reject(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      101,
		Ack:      202,
		AckValid: true,
	}, ReasonInvalidAck)

	// 前向打通但后向仍在 SYN-SENT 时，数据仍被拒绝。
	finAck := h.c.Step(Event{Side: Front, Flags: FlagACK, Seq: 101, Ack: 201, AckValid: true})
	if !finAck.Signals.Has(SignalSynBack) || h.c.State(Back) != StateSynSent {
		t.Fatalf("最终 ACK 裁决错误: %+v", finAck)
	}
	if h.c.State(Back) != StateSynSent {
		t.Fatalf("后向状态 = %s, 期望 syn-sent", h.c.State(Back))
	}
	h.reject(Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        101,
		Ack:        201,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonHandshakeIncomplete)

	// 后向在 SYN-SENT 收到数据同样拒绝。
	h.reject(Event{
		Side:       Back,
		Flags:      FlagACK,
		Seq:        7,
		Ack:        301,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonHandshakeIncomplete)

	// 后向 SYN+ACK 确认号错误也被拒绝。
	h.reject(Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      400 % 64,
		Ack:      302,
		AckValid: true,
	}, ReasonInvalidAck)
}

func TestHandshakeRetransmission(t *testing.T) {
	isn := [4]uint64{100 % 64, 200 % 64, 300 % 64, 0}
	h := newHarness(t, 6, isn)

	// 客户端 SYN 重传：重复产生 SYN+ACK 信号，rcvNxt 不动。
	for i := 0; i < 3; i++ {
		v := h.c.Step(Event{Side: Front, Flags: FlagSYN, Seq: isn[0]})
		if !v.Signals.Has(SignalSynAckFront) || !v.Retransmit {
			t.Fatalf("第 %d 次 SYN 重传裁决错误: %+v", i, v)
		}
	}
	h.checkRcvNxt()

	// 序号不符的 SYN 视为无效。
	h.reject(Event{Side: Front, Flags: FlagSYN, Seq: isn[0] + 1}, ReasonInvalidSeq)

	// 进入后向 SYN-SENT。
	finAck := h.c.Step(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      isn[0] + 1,
		Ack:      isn[1] + 1,
		AckValid: true,
	})
	if !finAck.Signals.Has(SignalSynBack) {
		t.Fatalf("最终 ACK 未触发后向 SYN: %+v", finAck)
	}

	// 服务端 SYN+ACK 重传：重复产生最终 ACK，rcvNxt 不重复推进。
	synack := Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      400 % 64,
		Ack:      isn[2] + 1,
		AckValid: true,
	}
	v := h.c.Step(synack)
	if !v.Signals.Has(SignalAckBack) || v.Retransmit {
		t.Fatalf("首个 SYN+ACK 裁决错误: %+v", v)
	}
	h.bKnown = true
	h.isn[3] = synack.Seq
	h.expB = (synack.Seq + 1) % h.mod
	h.checkRcvNxt()

	for i := 0; i < 2; i++ {
		rv := h.c.Step(synack)
		if !rv.Signals.Has(SignalAckBack) || !rv.Retransmit {
			t.Fatalf("第 %d 次 SYN+ACK 重传裁决错误: %+v", i, rv)
		}
	}
	h.checkRcvNxt()

	// 建立后数据段重传：转发但 rcvNxt 不重复推进。
	data := Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        (isn[0] + 1) % h.mod,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 10,
	}
	dv := h.accept(data)
	h.advance(Front, data)
	h.checkXlate(Front, data, dv)
	h.checkRcvNxt()

	rv := h.c.Step(data)
	if !rv.Accept || !rv.Retransmit {
		t.Fatalf("数据重传裁决错误: %+v", rv)
	}
	h.checkRcvNxt()

	// FIN 重传同样不重复消费序号。
	finSeq := data.Seq + 10
	fin := Event{
		Side:     Front,
		Flags:    FlagACK | FlagFIN,
		Seq:      finSeq % h.mod,
		Ack:      data.Ack,
		AckValid: true,
	}
	fv := h.accept(fin)
	h.advance(Front, fin)
	h.checkXlate(Front, fin, fv)
	h.checkRcvNxt()

	frv := h.c.Step(fin)
	if !frv.Accept || !frv.Retransmit {
		t.Fatalf("FIN 重传裁决错误: %+v", frv)
	}
	h.checkRcvNxt()
	if h.c.State(Front) != StateClosing {
		t.Fatalf("FIN 后前向状态 = %s", h.c.State(Front))
	}
}
