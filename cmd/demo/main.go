// Demo: hand-crafted protobuf wire codec + locality-aware shard router.
package main

import (
	"fmt"

	"wireshard/shard"
	"wireshard/wire"
)

// A three-level nested message with packed repeated fields.
type Sample struct {
	Timestamp int64   `pb:"1,sint"`
	Values    []int64 `pb:"2,sint,packed"`
}

type Node struct {
	ID      uint64   `pb:"1,varint"`
	Name    string   `pb:"2,string"`
	Weight  uint32   `pb:"3,fixed32"`
	Samples []Sample `pb:"4,message,repeated"`
}

type Heartbeat struct {
	Seq    uint64  `pb:"1,varint"`
	Alive  bool    `pb:"2,varint"`
	Nodes  []Node  `pb:"3,message,repeated"`
	Score  float64 `pb:"4,fixed64"`
}

func main() {
	fmt.Println("=== 1. Protobuf wire-format round-trip (hand-written parser) ===")
	hb := &Heartbeat{
		Seq:   42,
		Alive: true,
		Nodes: []Node{
			{ID: 7, Name: "z0-r0-n0", Weight: 100, Samples: []Sample{
				{Timestamp: -123456789, Values: []int64{-3, 0, 5, 999999}},
				{Timestamp: 2, Values: []int64{1}},
			}},
			{ID: 8, Name: "z0-r0-n1", Weight: 200, Samples: []Sample{
				{Timestamp: 0, Values: []int64{}},
			}},
		},
		Score: 99.5,
	}
	data, err := wire.Marshal(hb)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded %d bytes: %x...\n", len(data), data[:24])

	var back Heartbeat
	if err := wire.Unmarshal(data, &back); err != nil {
		panic(err)
	}
	fmt.Printf("decoded: seq=%d alive=%v score=%.1f nodes=%d\n",
		back.Seq, back.Alive, back.Score, len(back.Nodes))
	fmt.Printf("  node[0]: id=%d name=%s weight=%d samples=%d first-values=%v\n",
		back.Nodes[0].ID, back.Nodes[0].Name, back.Nodes[0].Weight,
		len(back.Nodes[0].Samples), back.Nodes[0].Samples[0].Values)

	fmt.Println("\n=== 2. Safe out-of-bounds protection on truncated input ===")
	cut := len(data) - 3 // slice into the trailing fixed64
	var broken Heartbeat
	err = wire.Unmarshal(data[:cut], &broken)
	fmt.Printf("truncated at %d/%d bytes -> %v\n", cut, len(data), err)

	fmt.Println("\n=== 3. Locality-aware routing over hierarchical vshards ===")
	r := shard.NewRouter(4096, 3)
	for z := 0; z < 3; z++ {
		for rk := 0; rk < 2; rk++ {
			for n := 0; n < 4; n++ {
				id := fmt.Sprintf("z%d-r%d-n%d", z, rk, n)
				if err := r.AddNode(shard.Node{
					ID:       id,
					Locality: shard.Locality{Zone: fmt.Sprintf("z%d", z), Rack: fmt.Sprintf("r%d", rk), Node: id},
				}); err != nil {
					panic(err)
				}
			}
		}
	}
	fmt.Printf("cluster: %d nodes, %d vshards, 3 replicas/shard\n", len(r.Nodes()), r.NumShards())

	client := shard.Locality{Zone: "z0", Rack: "r0", Node: "z0-r0-n0"}
	const requests = 100000
	for i := 0; i < requests; i++ {
		d, err := r.Route(fmt.Sprintf("user-session-%d", i), client)
		if err != nil {
			panic(err)
		}
		r.Acquire(d.Node.ID)
		r.Done(d.Node.ID)
	}
	st := r.Stats()
	pct := func(v uint64) float64 { return 100 * float64(v) / float64(st.Total) }
	fmt.Printf("routed %d requests from %s:\n", st.Total, client.Node)
	fmt.Printf("  same-node: %6.2f%%  same-rack: %6.2f%%  same-zone: %6.2f%%  remote: %6.2f%%\n",
		pct(st.SameNode), pct(st.SameRack), pct(st.SameZone), pct(st.Remote))
	fmt.Printf("  cross-node traffic: %.2f%%  cross-zone traffic: %.2f%%\n",
		pct(st.CrossNode()), pct(st.CrossZone()))

	dist := r.Distribution()
	lo, hi, ideal := minMax(dist)
	fmt.Printf("  vshard primary distribution across %d nodes: min=%d max=%d (ideal=%d)\n",
		len(dist), lo, hi, ideal)
}

func minMax(dist map[string]int) (int, int, int) {
	min, max := -1, 0
	total := 0
	for _, v := range dist {
		if min == -1 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
		total += v
	}
	return min, max, total / len(dist)
}
