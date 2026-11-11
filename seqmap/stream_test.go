package seqmap

import (
	"math/rand/v2"
	"testing"
)

// TestWraparound 在小模数上用参考模型逐段驱动序号多次回绕，
// 验证 SEQ/ACK 映射与 rcvNxt 在模空间边界上始终一致。
func TestWraparound(t *testing.T) {
	const bits = 5 // 模数 32，半窗口 16
	isn := [4]uint64{30, 7, 21, 0}
	h := newHarness(t, bits, isn)
	h.establish(13)

	rng := rand.New(rand.NewPCG(1297, 42))

	// 前向：随机长度按序发送，强制跨越多次回绕。
	seq := h.expF
	ack := (isn[1] + 1) % h.mod
	for i := 0; i < 40; i++ {
		n := 1 + rng.IntN(6)
		ev := Event{
			Side:       Front,
			Flags:      FlagACK,
			Seq:        seq % h.mod,
			Ack:        ack,
			AckValid:   true,
			PayloadLen: n,
		}
		v := h.accept(ev)
		h.checkXlate(Front, ev, v)
		h.advance(Front, ev)
		h.checkRcvNxt()
		seq = (seq + uint64(n)) % h.mod
	}

	// 后向同样多轮回绕，并验证映射后的 ACK。
	bseq := h.expB
	back := (isn[2] + 1) % h.mod
	for i := 0; i < 40; i++ {
		n := 1 + rng.IntN(6)
		ev := Event{
			Side:       Back,
			Flags:      FlagACK,
			Seq:        bseq % h.mod,
			Ack:        back,
			AckValid:   true,
			PayloadLen: n,
		}
		v := h.accept(ev)
		h.checkXlate(Back, ev, v)
		h.advance(Back, ev)
		h.checkRcvNxt()
		bseq = (bseq + uint64(n)) % h.mod
	}
}

// TestWindowBoundary 验证窗口严格小于模数一半：差距恰为 half 时拒绝。
func TestWindowBoundary(t *testing.T) {
	const bits = 5 // half = 16
	isn := [4]uint64{0, 8, 16, 0}
	h := newHarness(t, bits, isn)
	h.establish(24)

	// 先消费一个字节，使 rcvNxt 前移。
	ev := Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        h.expF,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 1,
	}
	h.accept(ev)
	h.advance(Front, ev)

	// 差距 15 < 16：乱序但在窗内，按 OutOfWindow 拒绝（不转发）。
	inside := (h.expF + 15) % h.mod
	h.reject(Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        inside,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonOutOfWindow)

	// 差距恰为 16 = half：判定为窗外异序号。
	atEdge := (h.expF + 16) % h.mod
	h.reject(Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        atEdge,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 1,
	}, ReasonInvalidSeq)

	// 完全落在已接收空间的重传仍被接受且不推进。
	dup := Event{
		Side:       Front,
		Flags:      FlagACK,
		Seq:        (h.expF - 1) % h.mod,
		Ack:        (isn[1] + 1) % h.mod,
		AckValid:   true,
		PayloadLen: 1,
	}
	rv := h.accept(dup)
	if !rv.Retransmit {
		t.Fatalf("重传未标记 Retransmit: %+v", rv)
	}
	h.checkRcvNxt()
}
