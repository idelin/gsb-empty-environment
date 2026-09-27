// Command demo runs a discrete-event fragmentation scenario: a datagram is
// fragmented, the path MTU drops mid-flight, oversized fragments are dropped,
// and the source re-plans with a fresh epoch until delivery. It also shows
// the joint header/alignment optimizer and the infeasible-MTU failure modes.
package main

import (
	"bytes"
	"fmt"

	"mtusim"
)

func makeData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return b
}

func main() {
	sim := &mtusim.Sim{}
	link := mtusim.NewLink("L1", 128, 3)
	link.PerFrag = 2
	rx := mtusim.NewReceiver("R1")
	src := mtusim.NewSource("S1", link, rx, 128, 20, 8, 25)
	rx.OnDone = func(s *mtusim.Sim, id int) { src.Ack(id) }

	pkt1 := makeData(300)
	pkt2 := makeData(60)
	pkt3 := makeData(100)
	pkt4 := makeData(40)

	src.Send(sim, 0, 1, pkt1)                       // sent under mtu 128
	mtusim.ScheduleMTUChange(sim, 1, link, src, 68) // mtu drops mid-flight
	src.Send(sim, 50, 2, pkt2)                      // steady state under mtu 68
	mtusim.ScheduleMTUChange(sim, 60, link, src, 24)
	src.Send(sim, 61, 3, pkt3) // mtu 24, align 8: infeasible
	mtusim.ScheduleMTUChange(sim, 70, link, src, 10)
	src.Send(sim, 71, 4, pkt4) // mtu 10 < header 20: fails directly

	sim.Run()

	for _, line := range sim.Log {
		fmt.Println(line)
	}

	fmt.Println()
	fmt.Printf("pkt1 reassembled == original: %v (%d bytes, %d link drops)\n",
		bytes.Equal(rx.Done[1], pkt1), len(rx.Done[1]), link.Dropped)
	fmt.Printf("pkt2 reassembled == original: %v (%d bytes)\n",
		bytes.Equal(rx.Done[2], pkt2), len(rx.Done[2]))
	_, pkt3Done := rx.Done[3]
	_, pkt4Done := rx.Done[4]
	fmt.Printf("pkt3 delivered: %v (expected false, infeasible mtu)\n", pkt3Done)
	fmt.Printf("pkt4 delivered: %v (expected false, mtu below header)\n", pkt4Done)

	fmt.Println()
	plan, err := mtusim.Optimize(300, 128, 8, 40, 16)
	if err != nil {
		fmt.Println("optimize:", err)
		return
	}
	fmt.Printf("optimizer: len=300 mtu=128 header in [8,40] align in [1,16] -> header=%d align=%d fragments=%d\n",
		plan.Params.HeaderLen, plan.Params.Align, plan.Count)
	frags, _ := plan.Params.Fragmentize(99, 1, 300)
	fmt.Printf("coverage verifier on plan: %v\n", mtusim.VerifyCoverage(plan.Params, 300, frags))
}
