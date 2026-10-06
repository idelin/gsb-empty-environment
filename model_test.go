package agg

import (
	"math/rand"
	"reflect"
	"testing"
)

// runAggregator feeds a trace through the Aggregator. Well-formed traces
// (per-partition FIFO, replays carry original labels) must never produce a
// protocol error.
func runAggregator(t *testing.T, initial []string, ops []Op) []Publication {
	t.Helper()
	a := NewAggregator(initial...)
	for i, op := range ops {
		switch op.Kind {
		case OpEvent:
			if err := a.OnEvent(op.Event); err != nil {
				t.Fatalf("op %d %+v: unexpected error: %v", i, op, err)
			}
		case OpBarrier:
			a.OnBarrier(op.Partition, op.Gen)
		case OpExit:
			a.RemovePartition(op.Partition)
		case OpJoin:
			a.AddPartition(op.Partition)
		}
	}
	return a.Publications()
}

// genTrace builds per-partition programs (events labeled with the current
// generation, then a barrier), interleaves them randomly, and injects
// replays (event/barrier redelivery with identical labels), duplicate
// barriers and partition exits. Late messages from exited partitions are
// delivered with probability 1/2. The interleaving respects the delivery
// discipline: a partition's barrier for g is only delivered once
// generation g is open, so a live partition never runs more than one
// barrier ahead of the aggregator.
func genTrace(r *rand.Rand, nParts, nGens int) ([]string, []Op) {
	parts := make([]string, nParts)
	queues := make(map[string][]Op, nParts)
	for i := range parts {
		parts[i] = string(rune('A' + i))
		var q []Op
		seq := 0
		for g := 0; g < nGens; g++ {
			for n := r.Intn(4); n > 0; n-- {
				seq++
				q = append(q, Op{Kind: OpEvent, Event: Event{
					Partition: parts[i], Gen: g, Seq: seq, Value: int64(r.Intn(100)),
				}})
			}
			q = append(q, Op{Kind: OpBarrier, Partition: parts[i], Gen: g})
		}
		queues[parts[i]] = q
	}

	alive := make(map[string]bool, nParts)
	crossed := make(map[string]int, nParts)
	for _, p := range parts {
		alive[p] = true
	}
	closedGen := 0
	var ops []Op

	closeCheck := func() {
		if len(alive) == 0 {
			return
		}
		for p := range alive {
			if crossed[p] != closedGen+1 {
				return
			}
		}
		closedGen++
	}
	// A live partition's head op is blocked only while it is a barrier
	// for a generation the aggregator has not opened yet.
	blocked := func(p string) bool {
		if !alive[p] {
			return false
		}
		head := queues[p][0]
		return head.Kind == OpBarrier && head.Gen > closedGen
	}

	open := nParts
	for open > 0 {
		var cands []string
		for _, p := range parts {
			if len(queues[p]) > 0 && !blocked(p) {
				cands = append(cands, p)
			}
		}
		if len(cands) == 0 {
			break // cannot happen: the laggard of the open generation is never blocked
		}
		p := cands[r.Intn(len(cands))]
		op := queues[p][0]
		queues[p] = queues[p][1:]
		if len(queues[p]) == 0 {
			open--
		}
		if !alive[p] {
			if r.Intn(2) == 0 { // late message from an exited partition
				ops = append(ops, op)
			}
			continue
		}
		ops = append(ops, op)
		if op.Kind == OpBarrier {
			crossed[p] = closedGen + 1
			closeCheck()
		}

		// Inject a replay of an earlier op from this partition.
		if r.Intn(6) == 0 {
			var earlier []Op
			for _, o := range ops {
				if o.Kind == OpEvent && o.Event.Partition == p {
					earlier = append(earlier, o)
				}
				if o.Kind == OpBarrier && o.Partition == p {
					earlier = append(earlier, o)
				}
			}
			if len(earlier) > 0 {
				ops = append(ops, earlier[r.Intn(len(earlier))])
			}
		}
		// Random exit, keeping at least one partition alive.
		if r.Intn(20) == 0 && len(alive) > 1 {
			ops = append(ops, Op{Kind: OpExit, Partition: p})
			delete(alive, p)
			delete(crossed, p)
			closeCheck()
		}
	}
	return parts, ops
}

// The Aggregator (buffering, rotating) and the Model (pure label algebra)
// must publish identical sequences for every randomized trace.
func TestModelCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))
		nParts := 1 + r.Intn(4)
		nGens := 1 + r.Intn(5)
		initial, ops := genTrace(r, nParts, nGens)

		want := Model(initial, ops)
		got := runAggregator(t, initial, ops)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d parts=%d gens=%d\naggregator: %+v\nmodel:      %+v",
				seed, nParts, nGens, got, want)
		}
		// Generations publish sequentially, each exactly once.
		for i, p := range got {
			if p.Gen != i {
				t.Fatalf("seed=%d: publication %d has gen %d, want %d", seed, i, p.Gen, i)
			}
		}
	}
}
