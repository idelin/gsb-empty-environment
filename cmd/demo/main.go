// Command demo runs the same traffic through a waiting-style shaper and a
// dropping-style policer with identical token parameters, entirely inside a
// discrete-event model (integer ticks, no real network traffic).
package main

import (
	"fmt"
	"sort"

	"tbsim"
)

func buildTree() ([]*tbsim.Flow, []*tbsim.Bucket) {
	root := tbsim.NewBucket("root", tbsim.Rat(3, 2), 30, 0) // shared: 1.5 B/tick, burst 30
	leafA := tbsim.NewBucket("leafA", tbsim.Rat(1, 1), 12, 0)
	leafB := tbsim.NewBucket("leafB", tbsim.Rat(1, 1), 12, 0)
	rootN := &tbsim.Node{Name: "root", Bucket: root}
	leafAN := &tbsim.Node{Name: "leafA", Bucket: leafA, Parent: rootN}
	leafBN := &tbsim.Node{Name: "leafB", Bucket: leafB, Parent: rootN}
	flows := []*tbsim.Flow{
		tbsim.NewFlow("A", leafAN, 1, 2, 48, 0), // high priority, weight 2
		tbsim.NewFlow("B", leafBN, 0, 1, 48, 1), // low priority, weight 1
	}
	return flows, []*tbsim.Bucket{root, leafA, leafB}
}

func script() []tbsim.Arrival {
	return []tbsim.Arrival{
		{Time: 0, Pkt: tbsim.Packet{ID: 1, Flow: "A", Size: 12}},
		{Time: 0, Pkt: tbsim.Packet{ID: 2, Flow: "A", Size: 12}},
		{Time: 0, Pkt: tbsim.Packet{ID: 3, Flow: "B", Size: 12}},
		{Time: 0, Pkt: tbsim.Packet{ID: 4, Flow: "B", Size: 12}},
		{Time: 0, Pkt: tbsim.Packet{ID: 5, Flow: "B", Size: 12}},
		{Time: 2, Pkt: tbsim.Packet{ID: 6, Flow: "A", Size: 8}},
		{Time: 5, Pkt: tbsim.Packet{ID: 7, Flow: "B", Size: 10}},
		{Time: 40, Pkt: tbsim.Packet{ID: 8, Flow: "A", Size: 12}}, // after idle
		{Time: 40, Pkt: tbsim.Packet{ID: 9, Flow: "B", Size: 12}},
	}
}

func waitsString(w map[string]int64) string {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%d", k, w[k])
	}
	return out
}

func main() {
	fmt.Println("== waiting-style SHAPER (delays, never exceeds cache) ==")
	flows, buckets := buildTree()
	s := tbsim.NewShaper(flows)
	for _, a := range script() {
		s.AddArrival(a)
	}
	// Mid-run controls: weight hot-swap and a cancel. Neither refills tokens.
	s.AddControl(tbsim.ControlOp{Time: 3, Kind: "setweight", Flow: "B", Weight: 3})
	s.AddControl(tbsim.ControlOp{Time: 3, Kind: "cancel", Flow: "B", PktID: 5})
	s.Run()

	fmt.Println("pkt flow  arr emit delay | wait decomposition (per ancestor bucket)")
	var shapedBytes, maxDelay, sumDelay int64
	for _, e := range s.Emitted {
		fmt.Printf("%3d %-4s %4d %4d %5d | %s\n",
			e.Pkt.ID, e.Pkt.Flow, e.Arrival, e.Emit, e.Delay(), waitsString(e.Waits))
		shapedBytes += e.Pkt.Size
		sumDelay += e.Delay()
		if e.Delay() > maxDelay {
			maxDelay = e.Delay()
		}
	}
	var dropBytes int64
	for _, d := range s.Dropped {
		fmt.Printf("%3d %-4s %4d    -     - | dropped: %s\n", d.Pkt.ID, d.Pkt.Flow, d.Time, d.Reason)
		dropBytes += d.Pkt.Size
	}
	fmt.Printf("shaper: passed=%dB dropped=%dB avgDelay=%.1f maxDelay=%d ticks\n",
		shapedBytes, dropBytes, float64(sumDelay)/float64(len(s.Emitted)), maxDelay)

	fmt.Println()
	fmt.Println("== dropping-style POLICER (never delays, same token params) ==")
	pflows, pbuckets := buildTree()
	p := tbsim.NewPolicer(pflows)
	for _, a := range script() {
		p.Arrive(a.Time, a.Pkt)
	}
	var passBytes, polDropBytes int64
	for _, pr := range p.Passed {
		fmt.Printf("pass pkt %d flow %s at tick %d (size %d)\n", pr.Pkt.ID, pr.Pkt.Flow, pr.Time, pr.Pkt.Size)
		passBytes += pr.Pkt.Size
	}
	for _, d := range p.Dropped {
		fmt.Printf("DROP pkt %d flow %s at tick %d (size %d, %s)\n", d.Pkt.ID, d.Pkt.Flow, d.Time, d.Pkt.Size, d.Reason)
		polDropBytes += d.Pkt.Size
	}
	fmt.Printf("policer: passed=%dB dropped=%dB delay=0 always\n", passBytes, polDropBytes)

	fmt.Println()
	fmt.Println("== conservation evidence (consumed + remaining + overflow == cap + generated) ==")
	for _, b := range buckets {
		a := b.Audit(100)
		fmt.Printf("shaper  bucket %-6s generated=%8s consumed=%8s remaining=%8s overflow=%8s conserved=%v\n",
			a.Name, a.Generated, a.Consumed, a.Remaining, a.Overflow, a.Conserved)
	}
	for _, b := range pbuckets {
		a := b.Audit(100)
		fmt.Printf("policer bucket %-6s generated=%8s consumed=%8s remaining=%8s overflow=%8s conserved=%v\n",
			a.Name, a.Generated, a.Consumed, a.Remaining, a.Overflow, a.Conserved)
	}

	fmt.Println()
	fmt.Println("== clock rewind & recovery: no double refill ==")
	b := tbsim.NewBucket("clk", tbsim.Rat(1, 1), 100, 0)
	b.Consume(0, 100)
	b.Advance(50)
	before := b.Avail(50)
	b.Advance(10) // clock jumps backwards
	b.Advance(49)
	b.Advance(50) // clock recovers
	fmt.Printf("tokens at tick 50 before rewind: %v, after rewind+recovery: %v (unchanged=%v)\n",
		before, b.Avail(50), before.Cmp(b.Avail(50)) == 0)
}
