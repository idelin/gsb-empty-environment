package sim

import (
	"bytes"
	"math/big"
	"testing"
)

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func buildNet(dropData, dropAck func(n uint64) bool, cwnd uint64) (*Scheduler, *Sender, *Receiver, *Pipe, *Pipe) {
	sched := NewScheduler()
	// Extended sequence space: base just below 2^64, so the transfer
	// crosses the 64-bit boundary.
	base := SeqFromBig(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(500)))
	snd := NewSender(sched, base, 1000, cwnd, 30)
	rcv := NewReceiver(base, 1<<20)
	dataPipe := NewPipe(sched, 2, rcv)
	ackPipe := NewPipe(sched, 2, snd)
	dataPipe.Drop = dropData
	ackPipe.Drop = dropAck
	snd.Out = dataPipe
	rcv.Out = ackPipe
	return sched, snd, rcv, dataPipe, ackPipe
}

func TestEndToEndLossyLink(t *testing.T) {
	sched, snd, rcv, dataPipe, _ := buildNet(func(n uint64) bool { return n%6 == 0 }, nil, 16000)
	payload := pattern(15000)
	snd.Enqueue(payload)
	sched.Run(1_000_000)

	if !bytes.Equal(rcv.Delivered, payload) {
		t.Fatalf("delivered %d bytes, match=%v", len(rcv.Delivered), bytes.Equal(rcv.Delivered, payload))
	}
	if snd.BytesSent != 15000 {
		t.Fatalf("BytesSent = %d, want 15000", snd.BytesSent)
	}
	// Every dropped packet is retransmitted exactly once: only the gaps.
	if want := dataPipe.Dropped() * 1000; snd.BytesRetransmitted != want {
		t.Fatalf("BytesRetransmitted = %d, want %d (dropped=%d)",
			snd.BytesRetransmitted, want, dataPipe.Dropped())
	}
	if snd.RejectedAcks != 0 {
		t.Fatalf("RejectedAcks = %d, want 0", snd.RejectedAcks)
	}
	if rcv.DuplicateBytes != 0 {
		t.Fatalf("DuplicateBytes = %d, want 0 (no spurious retransmission)", rcv.DuplicateBytes)
	}
}

func TestEndToEndAckLossAndDupAcks(t *testing.T) {
	// Lose every 5th ACK: the sender sees duplicate/stale cumulative ACKs
	// and must still complete without corrupting state.
	sched, snd, rcv, _, _ := buildNet(nil, func(n uint64) bool { return n%5 == 0 }, 8000)
	payload := pattern(12000)
	snd.Enqueue(payload)
	sched.Run(1_000_000)

	if !bytes.Equal(rcv.Delivered, payload) {
		t.Fatalf("delivered %d bytes, match=%v", len(rcv.Delivered), bytes.Equal(rcv.Delivered, payload))
	}
	if snd.RejectedAcks != 0 {
		t.Fatalf("RejectedAcks = %d, want 0", snd.RejectedAcks)
	}
	if !snd.SndUna().Eq(snd.SndNxt()) {
		t.Fatalf("sndUna=%s sndNxt=%s: transfer not fully acked", snd.SndUna(), snd.SndNxt())
	}
}

func TestEndToEndWindowShrinkMidTransfer(t *testing.T) {
	sched, snd, rcv, _, _ := buildNet(func(n uint64) bool { return n%9 == 0 }, nil, 16000)
	payload := pattern(10000)
	snd.Enqueue(payload)
	// Shrink to 2000 at tick 10, restore at tick 500.
	sched.At(10, func() { snd.SetCwnd(2000) })
	sched.At(500, func() { snd.SetCwnd(16000) })
	sched.Run(1_000_000)

	if !bytes.Equal(rcv.Delivered, payload) {
		t.Fatalf("delivered %d bytes, match=%v", len(rcv.Delivered), bytes.Equal(rcv.Delivered, payload))
	}
}
