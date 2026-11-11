package seqmap

import (
	"math/rand/v2"
	"testing"
)

// TestRandomReference 用固定种子随机驱动双向流量与重传，
// 每个接受的报文都由独立参考模型核对映射，rcvNxt 全程对账。
func TestRandomReference(t *testing.T) {
	const bits = 5
	isn := [4]uint64{3, 17, 28, 0}
	h := newHarness(t, bits, isn)
	h.establish(9)

	rng := rand.New(rand.NewPCG(99, 7))
	fSeq, bSeq := h.expF, h.expB
	fAck := (isn[1] + 1) % h.mod
	bAck := (isn[2] + 1) % h.mod

	for i := 0; i < 200; i++ {
		side := Side(rng.IntN(2))
		seq, ack := fSeq, fAck
		if side == Back {
			seq, ack = bSeq, bAck
		}
		n := rng.IntN(5) // 含 0：纯 ACK
		ev := Event{
			Side:       side,
			Flags:      FlagACK,
			Seq:        seq,
			Ack:        ack,
			AckValid:   true,
			PayloadLen: n,
		}
		v := h.c.Step(ev)
		if !v.Accept {
			t.Fatalf("迭代 %d 随机按序报文被拒: %+v reason=%s", i, ev, v.Reason)
		}
		h.checkXlate(side, ev, v)
		if n > 0 {
			if v.Retransmit {
				t.Fatalf("按序新报文不应标记重传: %+v", ev)
			}
			h.advance(side, ev)
			if side == Front {
				fSeq = (fSeq + uint64(n)) % h.mod
			} else {
				bSeq = (bSeq + uint64(n)) % h.mod
			}
		}
		h.checkRcvNxt()

		// 偶发重传上一段，验证不重复推进。
		if n > 0 && rng.IntN(3) == 0 {
			old := ev
			old.Seq = (seq - uint64(n) + h.mod) % h.mod
			rv := h.c.Step(old)
			if !rv.Accept || !rv.Retransmit {
				t.Fatalf("迭代 %d 重传裁决错误: %+v", i, rv)
			}
			h.checkXlate(side, old, rv)
			h.checkRcvNxt()
		}

		// ACK 号也随接收进度随机前进，并验证 ACK 映射。
		if rng.IntN(4) == 0 {
			if side == Front {
				fAck = (fAck + uint64(rng.IntN(3))) % h.mod
			} else {
				bAck = (bAck + uint64(rng.IntN(3))) % h.mod
			}
		}
	}
}

// TestPureAckMapping 建立后纯 ACK 不消费序号，但 ACK 映射仍须正确。
func TestPureAckMapping(t *testing.T) {
	const bits = 6
	isn := [4]uint64{10, 40, 20, 50}
	h := newHarness(t, bits, isn)
	h.establish(50)

	ev := Event{
		Side:     Front,
		Flags:    FlagACK,
		Seq:      h.expF,
		Ack:      (isn[1] + 1) % h.mod,
		AckValid: true,
	}
	v := h.accept(ev)
	h.checkXlate(Front, ev, v)
	h.checkRcvNxt() // rcvNxt 不应变化

	// 无任何标志的裸段拒绝。
	h.reject(Event{
		Side:       Front,
		Seq:        h.expF,
		PayloadLen: 1,
	}, ReasonBadFlags)
}
