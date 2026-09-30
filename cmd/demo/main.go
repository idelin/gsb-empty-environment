// Command demo runs a scripted scenario over the discrete-event model and
// prints the reconstructed streams, gaps, conflicts and drops.
package main

import (
	"fmt"

	"streamsim"
)

func main() {
	sim := streamsim.NewSimulator()
	table := streamsim.NewTable()
	link := streamsim.NewLink(sim, 5, 1, func(p streamsim.Packet) {
		fmt.Printf("tick=%-3d deliver %s\n", sim.Now(), p)
		table.Handle(p)
	})

	client := streamsim.Tuple{SrcIP: "10.0.0.1", SrcPort: 5000, DstIP: "10.0.0.2", DstPort: 80}
	server := client.Reverse()

	send := func(tick uint64, p streamsim.Packet) {
		sim.At(tick, func() {
			fmt.Printf("tick=%-3d send    %s data=%q\n", sim.Now(), p, p.Data)
			link.Send(p)
		})
	}
	pkt := func(t streamsim.Tuple, epoch uint32, seq uint32, data string, syn, fin bool) streamsim.Packet {
		return streamsim.Packet{Tuple: t, Epoch: epoch, Seq: seq, Data: []byte(data), Syn: syn, Fin: fin}
	}

	// --- epoch 0: original connection ---
	send(0, pkt(client, 0, 1000, "", true, false)) // SYN, ISN 1000
	send(1, pkt(client, 0, 1001, "hello ", false, false))
	send(2, pkt(client, 0, 1013, "end", false, false))    // leaves gap 1007..1013
	send(3, pkt(client, 0, 1001, "hello ", false, false)) // identical retransmit
	send(4, pkt(client, 0, 1013, "eNd", false, false))    // conflicting retransmit
	send(6, pkt(client, 0, 1016, "", false, true))        // FIN: client half-closes
	send(7, pkt(server, 0, 2000, "200 OK", false, false)) // server may still send
	send(8, pkt(server, 0, 2006, "", false, true))        // server FIN: fully closed
	send(9, pkt(client, 0, 1016, "late", false, false))   // data beyond FIN: dropped

	// --- epoch 1: same 4-tuple, new incarnation ---
	send(10, pkt(client, 1, 5000, "", true, false)) // new SYN, new ISN
	send(11, pkt(client, 1, 5001, "again", false, false))
	send(12, pkt(client, 0, 1001, "hello ", false, false)) // stale epoch-0 retransmit

	sim.Run()

	fmt.Println("\n=== reconstructed streams ===")
	for _, c := range table.Conns() {
		for _, d := range []streamsim.Direction{streamsim.ClientToServer, streamsim.ServerToClient} {
			fmt.Printf("conn{%s epoch=%d} %s\n", c.Tuple, c.Epoch, d)
			if b := c.Deliver(d); b != nil {
				fmt.Printf("  delivered: %q\n", b)
			}
			for _, ch := range c.Finalize(d) {
				if ch.Gap != nil {
					fmt.Printf("  %s (missing, not fabricated)\n", ch.Gap)
				} else {
					fmt.Printf("  data: %q\n", ch.Data)
				}
			}
			for _, cf := range c.Conflicts(d) {
				fmt.Printf("  CONFLICT at off=%d: kept %#02x, rejected %#02x\n", cf.Offset, cf.Existing, cf.Incoming)
			}
			if n := c.DataAfterFin(d); n > 0 {
				fmt.Printf("  dropped %d payload byte(s) beyond FIN\n", n)
			}
			fmt.Printf("  fin=%v\n", c.FinReceived(d))
		}
	}
	fmt.Printf("\nstale epoch packets dropped: %d\n", table.StaleDrops)
	fmt.Printf("link queue drops: %d\n", link.Dropped)
}
