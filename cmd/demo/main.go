// Demo: out-of-order packets cross a delayed link into a reorder releaser.
// Everything runs in a discrete-event model over integer ticks; no real
// network traffic is generated.
package main

import (
	"fmt"

	"example.com/reordersim"
)

func main() {
	sim := reordersim.NewSimulator()
	rel := reordersim.NewReleaser(reordersim.ReleaserConfig{
		WaitLimit:  4, // ticks to wait for a missing seq before skipping
		CacheLimit: 3, // max buffered out-of-order packets
	})
	sim.AddTickable(rel)

	link := &reordersim.Link{
		Sim: sim,
		// Deterministic delays scramble the arrival order. Seq 2 is
		// delayed past the wait limit, so it ends up late.
		Delay: func(p reordersim.Packet) int64 {
			if p.Epoch == 0 && p.Seq == 2 {
				return 30
			}
			return (int64(p.Seq) * 3) % 5
		},
		To: rel.Receive,
	}
	src := &reordersim.Source{Sim: sim, Out: link}

	// Epoch 0: six packets, event time == sequence number.
	for seq := uint64(0); seq < 6; seq++ {
		src.SendAt(int64(seq), reordersim.Packet{
			Epoch: 0, Seq: seq, EventTime: int64(seq),
			Payload: fmt.Sprintf("e0-%d", seq),
		})
	}

	// Tick 8: reset to epoch 1 while seq 3,4,5 are still buffered waiting
	// for the lost seq 2. The old queue is discarded (counted as dropped);
	// stale epoch-0 traffic afterwards goes to the late channel, never the
	// new stream.
	sim.Schedule(8, func() { rel.Reset(1, 100) })
	src.SendAt(9, reordersim.Packet{Epoch: 0, Seq: 9, EventTime: 9, Payload: "stale"})
	src.SendAt(10, reordersim.Packet{Epoch: 1, Seq: 101, EventTime: 51, Payload: "e1-101"})
	src.SendAt(11, reordersim.Packet{Epoch: 1, Seq: 100, EventTime: 50, Payload: "e1-100"})

	sim.Run(40)

	fmt.Println("released (in order):")
	order := 0
	for {
		select {
		case p := <-rel.Released():
			order++
			fmt.Printf("  #%d  epoch=%d seq=%3d eventTime=%2d  %s\n",
				order, p.Epoch, p.Seq, p.EventTime, p.Payload)
		default:
			goto late
		}
	}
late:
	fmt.Println("late (separate channel):")
	for {
		select {
		case p := <-rel.Late():
			fmt.Printf("  epoch=%d seq=%3d eventTime=%2d  %s\n",
				p.Epoch, p.Seq, p.EventTime, p.Payload)
		default:
			goto done
		}
	}
done:
	fmt.Printf("stats: %+v\n", rel.Stats())
}
