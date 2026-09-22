// Command demo runs one controlled scenario in the single-process event
// simulator: B causally depends on A, a reader on r3 sees the new B while A
// is partitioned away, blocks reporting the missing cause, and closes the
// consistent slice after the partition heals.
package main

import (
	"fmt"

	"causalslice"
)

type script struct {
	sim *causalslice.Sim
	fn  map[int]func()
}

type timer struct{ at int }

func (s *script) ID() string { return "script" }
func (s *script) at(t int, f func()) {
	s.fn[t] = f
	s.sim.Schedule("script", t, timer{at: t})
}
func (s *script) Receive(sim *causalslice.Sim, env causalslice.Envelope) {
	if tm, ok := env.Msg.(causalslice.Timer); ok {
		if t, ok := tm.Tag.(timer); ok {
			s.fn[t.at]()
		}
	}
}

func main() {
	net := causalslice.NewNetwork().Seed(7).Delay(1, 3)
	sim := causalslice.NewSim(net, func(t int, msg string) {
		fmt.Printf("t=%3d  %s\n", t, msg)
	})

	causalslice.NewReplica(sim, "r1", 10)
	causalslice.NewReplica(sim, "r2", 10)
	causalslice.NewReplica(sim, "r3", 10)

	writer := causalslice.NewClient(sim, "writer", func(int, causalslice.Result) {})
	var reader *causalslice.Client

	results := map[int]causalslice.Result{}
	reader = causalslice.NewClient(sim, "reader", func(id int, res causalslice.Result) {
		results[id] = res
	})
	sc := &script{sim: sim, fn: map[int]func(){}}
	sim.Register(sc)

	sc.at(2, func() { writer.Put("r1", "A", "1", nil) })
	sc.at(20, func() {
		writer.Put("r2", "B", "2", []causalslice.VersionID{{Origin: "r1", Seq: 1}})
	})

	partition := true
	net.Drop(func(env causalslice.Envelope) bool {
		if !partition {
			return false
		}
		if env.To == "r3" {
			switch m := env.Msg.(type) {
			case causalslice.Replicate:
				if m.Ver.ID.Origin == "r1" {
					return true
				}
			case causalslice.GossipResp, causalslice.GossipReq:
				return true
			}
		}
		if _, ok := env.Msg.(causalslice.PutReq); ok {
			return false
		}
		if _, ok := env.Msg.(causalslice.PutAck); ok {
			return false
		}
		if _, ok := env.Msg.(causalslice.FetchReq); ok && env.From == "reader" && env.To != "r3" {
			return true
		}
		if _, ok := env.Msg.(causalslice.FetchResp); ok && env.To == "reader" && env.From != "r3" {
			return true
		}
		return false
	})

	var snap int
	sc.at(25, func() {
		snap = reader.Read("r3", []string{"A", "B"}, 8)
		fmt.Println(">>> reader opens snapshot at r3 (r3 may have new B, but A is partitioned away)")
	})
	sc.at(42, func() {
		peek := reader.Peek(snap)
		fmt.Printf(">>> mid-flight peek: closed=%v selected=%v blocked=%v\n",
			peek.Closed, peek.Selected, blocked(peek.Blocked))
	})
	sc.at(45, func() {
		partition = false
		fmt.Println(">>> partition healed: all replicas reachable again")
	})

	sim.Run(90)

	res := results[snap]
	fmt.Println()
	fmt.Println("=== consistent slice ===")
	fmt.Printf("closed : %v\n", res.Closed)
	fmt.Printf("versions selected: %v\n", res.Selected)
	fmt.Printf("version vector  : %v\n", res.Vector)
}

func blocked(bs []causalslice.BlockedDep) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, fmt.Sprintf("%s(needed-by:%s tried:%v)", b.Missing, b.RequiredBy, b.Tried))
	}
	return out
}
