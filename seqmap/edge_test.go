package seqmap

import "testing"

func TestEdgeBranches(t *testing.T) {
	isn := [4]uint64{10, 40, 20, 0}
	h := newHarness(t, 6, isn)

	// 后端尚未发起（CLOSED）时，任何后向事件都没有上下文。
	h.reject(Event{
		Side:       Back,
		Flags:      FlagACK,
		Seq:        0,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonHandshakeIncomplete)

	// SYN+FIN、RST+SYN、RST+FIN 等标志组合非法。
	h.reject(Event{Side: Front, Flags: FlagSYN | FlagFIN, Seq: isn[0]}, ReasonBadFlags)
	h.reject(Event{Side: Front, Flags: FlagRST | FlagSYN, Seq: isn[0]}, ReasonBadFlags)

	// SYN-RCVD 下，非 SYN、非 ACK 的报文无法推进握手。
	h.reject(Event{Side: Front, Flags: FlagFIN, Seq: isn[0] + 1}, ReasonHandshakeIncomplete)

	// SYN-RCVD 下，落在对端 ISN 上的 RST 可接受并拆除连接。
	v := h.c.Step(Event{Side: Front, Flags: FlagRST, Seq: isn[0]})
	if v.Signals != SignalRstBack || !h.c.Closed() {
		t.Fatalf("SYN-RCVD RST 裁决错误: %+v", v)
	}
}

func TestSynSentRstWithoutAck(t *testing.T) {
	isn := [4]uint64{10, 40, 20, 0}
	h := newHarness(t, 6, isn)
	h.c.Step(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      isn[0] + 1,
		Ack:      isn[1] + 1,
		AckValid: true,
	})

	// SYN-SENT 下不带 ACK 的 RST 同样可接受（RFC 793）。
	v := h.c.Step(Event{Side: Back, Flags: FlagRST, Seq: 9})
	if v.Signals != SignalRstFront || !h.c.Closed() {
		t.Fatalf("无 ACK 的 SYN-SENT RST 裁决错误: %+v", v)
	}
}

func TestBackSynAckBadSeq(t *testing.T) {
	isn := [4]uint64{10, 40, 20, 0}
	h := newHarness(t, 6, isn)
	h.c.Step(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      isn[0] + 1,
		Ack:      isn[1] + 1,
		AckValid: true,
	})

	// 首个 SYN+ACK 建立后端。
	first := h.c.Step(Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      50,
		Ack:      isn[2] + 1,
		AckValid: true,
	})
	if !first.Signals.Has(SignalAckBack) {
		t.Fatalf("首个 SYN+ACK 裁决错误: %+v", first)
	}

	// 重传 SYN+ACK 但序号不一致：拒绝。
	h.reject(Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      51,
		Ack:      isn[2] + 1,
		AckValid: true,
	}, ReasonInvalidSeq)

	// 建立后裸 SYN 属于非法标志。
	h.reject(Event{Side: Back, Flags: FlagSYN, Seq: 50}, ReasonBadFlags)
}

func TestBackPeerISNZero(t *testing.T) {
	// 服务端 ISN 恰好为 0 时，首个 SYN+ACK 仍必须被正确识别。
	isn := [4]uint64{10, 40, 20, 0}
	h := newHarness(t, 6, isn)
	h.c.Step(Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      isn[0] + 1,
		Ack:      isn[1] + 1,
		AckValid: true,
	})

	v := h.c.Step(Event{
		Side:     Back,
		Flags:    FlagSYN | FlagACK,
		Seq:      0,
		Ack:      isn[2] + 1,
		AckValid: true,
	})
	if !v.Signals.Has(SignalAckBack) || v.Retransmit {
		t.Fatalf("ISN=0 的首个 SYN+ACK 被误判: %+v", v)
	}
	if h.c.PeerISN(Back) != 0 || h.c.RcvNxt(Back) != 1 {
		t.Fatalf("ISN=0 状态记录错误: peer=%d rcvNxt=%d",
			h.c.PeerISN(Back), h.c.RcvNxt(Back))
	}
	if !h.c.Established() {
		t.Fatal("ISN=0 握手后应处于 Established")
	}
}

func TestAccessorsAndStrings(t *testing.T) {
	isn := [4]uint64{10, 40, 20, 0}
	h := newHarness(t, 6, isn)

	if h.c.PeerISN(Front) != isn[0] {
		t.Fatalf("PeerISN(Front)=%d", h.c.PeerISN(Front))
	}
	if h.c.State(Front).String() != "syn-rcvd" ||
		h.c.State(Back).String() != "closed" {
		t.Fatal("状态字符串不符合预期")
	}
	if Front.String() != "front" || Back.String() != "back" {
		t.Fatal("Side 字符串不符合预期")
	}
	if ReasonOK.String() != "ok" || FlagRST.String() == "" {
		t.Fatal("Reason/Flags 基础行为异常")
	}

	h.establish(50)
	if h.c.PeerISN(Back) != 50 {
		t.Fatalf("PeerISN(Back)=%d, 期望 50", h.c.PeerISN(Back))
	}
	if !h.c.Established() {
		t.Fatal("握手后应为 Established")
	}
}
