// Command demo runs a simulated TCP-style transfer over lossy links in
// a discrete-event engine. All time is in integer ticks; no real
// network traffic is generated.
package main

import (
	"fmt"

	"tcpsim"
)

func main() {
	const (
		total        = 60000 // payload bytes to transfer
		mss          = 1000
		initSsthresh = 8000
		rtoBase      = 400 // ticks
	)

	eng := tcpsim.NewEngine()

	snd := tcpsim.NewSender(eng, total, mss, initSsthresh, rtoBase)
	rcv := tcpsim.NewReceiver()

	// Forward link: 260 bytes/tick, 40 tick propagation delay.
	fwd := tcpsim.NewLink(eng, "data", 260, 40, rcv)
	// Reverse link: ACKs are cheap, same delay.
	ack := tcpsim.NewLink(eng, "ack", 2000, 40, snd)
	snd.Out = fwd
	rcv.Out = ack

	// Deterministic loss pattern: segment 10000 lost once (fast
	// retransmit recovers), segment 25000 lost twice (fast retransmit
	// is lost too, so the RTO timer must recover with backoff).
	drops := map[int]int{}
	fwd.Drop = func(p tcpsim.Packet) bool {
		want := 0
		switch p.Seq {
		case 10000:
			want = 1
		case 25000:
			want = 2
		}
		if drops[p.Seq] < want {
			drops[p.Seq]++
			return true
		}
		return false
	}
	fwd.OnDrop = func(p tcpsim.Packet) {
		fmt.Printf("[%6d] DROP  data seq=%d len=%d\n", eng.Now(), p.Seq, p.Len)
	}
	snd.Log = func(tick int, msg string) {
		fmt.Printf("[%6d] %s\n", tick, msg)
	}

	snd.Start()
	eng.Run()

	fmt.Println("--- summary ---")
	fmt.Printf("completed at tick      %d\n", eng.Now())
	fmt.Printf("bytes received in order %d / %d\n", rcv.Received, total)
	fmt.Printf("sender done             %v\n", snd.Done())
	fmt.Printf("timeouts                %d\n", snd.Timeouts)
	fmt.Printf("fast retransmits        %d\n", snd.FastRetransmits)
	fmt.Printf("total retransmits       %d\n", snd.Retransmits)
	fmt.Printf("duplicate ACKs seen     %d\n", snd.DupAcks)
	fmt.Printf("stale ACKs seen         %d\n", snd.StaleAcks)
	fmt.Printf("packets dropped (fwd)   %d\n", fwd.Dropped)
	fmt.Printf("final cwnd / ssthresh   %d / %d\n", snd.Cwnd(), snd.Ssthresh())
	fmt.Printf("final rto               %d (base %d)\n", snd.RTO(), rtoBase)
}
