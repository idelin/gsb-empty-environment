// Command demo runs a SACK-based reliable transfer inside the discrete-event
// simulator: a lossy link, a mid-transfer window shrink, and a sequence
// space starting far beyond 2^32 (extended integers).
package main

import (
	"bytes"
	"fmt"

	"sacksim/sim"
)

func main() {
	sched := sim.NewScheduler()

	// Extended sequence space: start near 2^62.
	base := sim.NewSeq(uint64(1) << 62)

	snd := sim.NewSender(sched, base, 1000, 8000, 25)
	rcv := sim.NewReceiver(base, 1<<20)

	dataPipe := sim.NewPipe(sched, 3, rcv)
	ackPipe := sim.NewPipe(sched, 3, snd)
	snd.Out = dataPipe
	rcv.Out = ackPipe

	// Deterministic loss: every 7th data packet and every 11th ACK vanish.
	dataPipe.Drop = func(n uint64) bool { return n%7 == 0 }
	ackPipe.Drop = func(n uint64) bool { return n%11 == 0 }

	snd.Log = func(msg string) { fmt.Printf("[tick %5d] SND %s\n", sched.Now(), msg) }
	rcv.Log = func(msg string) { fmt.Printf("[tick %5d] RCV %s\n", sched.Now(), msg) }

	const total = 20000
	payload := make([]byte, total)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}

	snd.Enqueue(payload)

	// Shrink the congestion window mid-transfer, grow it back later.
	sched.At(20, func() { snd.SetCwnd(3000) })
	sched.At(200, func() { snd.SetCwnd(8000) })

	sched.Run(1_000_000)

	fmt.Println("---- result ----")
	fmt.Printf("delivered:        %d/%d bytes, in-order match: %v\n",
		len(rcv.Delivered), total, bytes.Equal(rcv.Delivered, payload))
	fmt.Printf("final tick:       %d\n", sched.Now())
	fmt.Printf("sent (new):       %d bytes\n", snd.BytesSent)
	fmt.Printf("retransmitted:    %d bytes (gaps only)\n", snd.BytesRetransmitted)
	fmt.Printf("data pkts dropped: %d, ack pkts dropped: %d\n",
		dataPipe.Dropped(), ackPipe.Dropped())
	fmt.Printf("timeouts:         %d\n", snd.Timeouts)
	fmt.Printf("stale ACKs:       %d, rejected ACKs: %d\n", snd.StaleAcks, snd.RejectedAcks)
	fmt.Printf("duplicate bytes at receiver: %d\n", rcv.DuplicateBytes)
	fmt.Printf("acked union (SACK set): %s bytes\n", totalString(snd))
}

func totalString(s *sim.Sender) string {
	sum := int64(0)
	for _, r := range s.Sacked() {
		sum += r.Len().Int64()
	}
	return fmt.Sprint(sum)
}
