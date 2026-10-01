// Demo: 10000-byte transfer over a lossy simulated link, entirely inside a
// discrete-event model (integer ticks, no real network traffic).
package main

import (
	"fmt"
	"math/big"
	"os"

	"sacksim/sim"
)

func main() {
	q := &sim.Queue{}
	log := &sim.Logger{W: os.Stdout, Enabled: true}

	dataLink := sim.NewLink(q, 10, 7) // 10-tick delay, every 7th data packet lost
	ackLink := sim.NewLink(q, 10, 0)

	isn := new(big.Int).Lsh(big.NewInt(1), 70) // extended-integer ISN (2^70)
	sender := sim.NewSender(q, dataLink, isn, big.NewInt(10000), big.NewInt(1000), big.NewInt(4000), 120, log)
	receiver := sim.NewReceiver(q, ackLink, isn, log)
	sender.OnData = receiver.OnSegment
	receiver.OnAck = sender.OnAck

	// Mid-transfer events: window shrink, bogus ACK, stale ACK.
	q.Schedule(25, func() { sender.SetWindow(big.NewInt(2000)) })
	q.Schedule(115, func() {
		bogus := new(big.Int).Add(sender.SndNxt(), big.NewInt(5000))
		sender.OnAck(sim.AckPacket{Ack: bogus}) // ACK of unsent bytes: rejected
	})
	q.Schedule(125, func() {
		sender.OnAck(sim.AckPacket{Ack: new(big.Int).Set(isn)}) // stale: ignored
	})

	sender.Start()
	q.Run(100000)

	end := new(big.Int).Add(isn, big.NewInt(10000))
	fmt.Println("---- final state ----")
	fmt.Printf("ISN (extended int)      : %s\n", isn)
	fmt.Printf("sender una / nxt        : %s / %s\n", sender.SndUna(), sender.SndNxt())
	fmt.Printf("receiver rcvNxt         : %s\n", receiver.RcvNxt())
	fmt.Printf("data pkts sent/dropped  : %d / %d\n", dataLink.Sent, dataLink.Dropped)
	fmt.Printf("bytes on wire (total)   : %s\n", sender.SentTotal)
	fmt.Printf("bytes retransmitted     : %s\n", sender.Retransmitted)
	fmt.Printf("receiver good bytes     : %s\n", receiver.ReceivedBytes)
	fmt.Printf("receiver dup bytes      : %s\n", receiver.DupBytes)
	fmt.Printf("rejected ACKs           : %d\n", sender.RejectedAcks)
	if sender.SndUna().Cmp(end) == 0 && receiver.RcvNxt().Cmp(end) == 0 {
		fmt.Println("RESULT: OK - all 10000 bytes delivered and acknowledged")
	} else {
		fmt.Println("RESULT: INCOMPLETE")
		os.Exit(1)
	}
}
