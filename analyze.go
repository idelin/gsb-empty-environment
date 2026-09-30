package main

import "sort"

// Bound is an interval estimate. HiKnown=false means the upper bound is
// unknown (open), used when events are missing instead of zero-filling.
type Bound struct {
	Lo      int64
	Hi      int64
	HiKnown bool
}

// Breakdown splits one packet's end-to-end wall-clock delay into phases.
// Phases are attributed along the causal chain from first enqueue to deliver,
// so overlapping parallel work (e.g. the losing copy of a multipath merge) is
// never double-added: Sum == deliver-enqueue whenever the trace is complete.
// Redundant off-chain work still shows up in the DAG-level TotalWork metric.
// Evidence keeps the source event IDs backing each phase.
type Breakdown struct {
	Packet      int
	Delivered   bool
	Queue       int64
	Serial      int64
	Prop        int64
	RetransWait int64
	Other       int64
	Sum         int64 // Queue+Serial+Prop+RetransWait+Other
	Total       Bound
	Consistent  bool // phases fully explain Total (complete, conflict-free)
	Bottleneck  string
	Chain       []int            // causal chain of event IDs behind the phases
	Evidence    map[string][]int // phase -> source event IDs
}

func (b *Breakdown) addEvidence(phase string, ids ...int) {
	b.Evidence[phase] = append(b.Evidence[phase], ids...)
}

// AnalyzePackets groups events per packet and derives the phase breakdown.
func AnalyzePackets(tr *Trace) map[int]*Breakdown {
	byPkt := map[int][]Event{}
	for _, e := range tr.Events() {
		byPkt[e.Packet] = append(byPkt[e.Packet], e)
	}
	out := make(map[int]*Breakdown, len(byPkt))
	for pkt, evs := range byPkt {
		out[pkt] = analyzePacket(pkt, evs, tr)
	}
	return out
}

func analyzePacket(pkt int, evs []Event, tr *Trace) *Breakdown {
	b := &Breakdown{Packet: pkt, Evidence: map[string][]int{}}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].Tick != evs[j].Tick {
			return evs[i].Tick < evs[j].Tick
		}
		return evs[i].ID < evs[j].ID
	})
	byID := make(map[int]*Event, len(evs))
	for i := range evs {
		byID[evs[i].ID] = &evs[i]
	}

	var firstEnq, deliver, last *Event
	complete := true
	for i := range evs {
		e := &evs[i]
		if e.Kind == KindEnqueue && firstEnq == nil {
			firstEnq = e
		}
		if e.Kind == KindDeliver && deliver == nil {
			deliver = e
		}
		if last == nil || e.Tick > last.Tick || (e.Tick == last.Tick && e.ID > last.ID) {
			last = e
		}
		for _, p := range e.Parents {
			if _, ok := tr.Get(p); !ok {
				complete = false // dangling parent reference
				b.addEvidence("missing", e.ID, p)
			}
		}
	}

	// Longest-path DP over the packet's causal subgraph (Kahn's algorithm).
	adj := map[int][]int{}
	indeg := map[int]int{}
	for i := range evs {
		indeg[evs[i].ID] = 0
	}
	for i := range evs {
		e := &evs[i]
		for _, p := range e.Parents {
			if _, ok := byID[p]; ok {
				adj[p] = append(adj[p], e.ID)
				indeg[e.ID]++
			}
		}
	}
	dist := map[int]int64{}
	prev := map[int]int{}
	for i := range evs {
		dist[evs[i].ID] = -1
	}
	var queue []int
	for i := range evs {
		if indeg[evs[i].ID] == 0 {
			dist[evs[i].ID] = 0
			queue = append(queue, evs[i].ID)
		}
	}
	sort.Ints(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, to := range adj[id] {
			w := byID[to].Tick - byID[id].Tick
			if w < 0 {
				w = 0 // contradictory edge; reported by CheckTrace with evidence
				complete = false
			}
			if dist[id]+w > dist[to] {
				dist[to] = dist[id] + w
				prev[to] = id
			}
			indeg[to]--
			if indeg[to] == 0 {
				queue = append(queue, to)
			}
		}
	}

	// Attribute phases along the causal chain ending at deliver (or, for
	// undelivered packets, at the last observed event).
	target := deliver
	if target == nil {
		target = last
	}
	if target != nil {
		for id := target.ID; id != 0; id = prev[id] {
			b.Chain = append(b.Chain, id)
		}
		for i, j := 0, len(b.Chain)-1; i < j; i, j = i+1, j-1 {
			b.Chain[i], b.Chain[j] = b.Chain[j], b.Chain[i]
		}
		for i := 1; i < len(b.Chain); i++ {
			pe, ce := byID[b.Chain[i-1]], byID[b.Chain[i]]
			b.attribute(pe, ce, byID)
		}
	}

	b.Delivered = deliver != nil
	b.Sum = b.Queue + b.Serial + b.Prop + b.RetransWait + b.Other
	chainStart := byID[b.Chain[0]]
	switch {
	case deliver != nil && firstEnq != nil:
		total := deliver.Tick - firstEnq.Tick
		b.Total = Bound{Lo: total, Hi: total, HiKnown: true}
		b.Consistent = complete && chainStart == firstEnq && b.Sum == total
	case firstEnq != nil:
		// No deliver event: report a lower bound, never a zero-filled total.
		b.Total = Bound{Lo: last.Tick - firstEnq.Tick, HiKnown: false}
	}

	b.Bottleneck = "queue"
	best := b.Queue
	for _, kv := range []struct {
		name string
		v    int64
	}{
		{"serial", b.Serial}, {"prop", b.Prop},
		{"retrans", b.RetransWait}, {"other", b.Other},
	} {
		if kv.v > best {
			b.Bottleneck, best = kv.name, kv.v
		}
	}
	return b
}

// attribute classifies one causal chain edge into a phase. Edge durations
// telescope (child.Tick - parent.Tick), so the phases sum to the chain length.
func (b *Breakdown) attribute(pe, ce *Event, byID map[int]*Event) {
	delta := ce.Tick - pe.Tick
	if delta < 0 {
		delta = 0
	}
	switch {
	case ce.Kind == KindDequeue:
		// enqueue->dequeue (waited behind queue) or dequeue->dequeue
		// (server busy with predecessor): both are queueing delay.
		b.Queue += delta
		b.addEvidence("queue", pe.ID, ce.ID)
	case pe.Kind == KindSendStart && ce.Kind == KindSendEnd:
		b.Serial += delta
		b.addEvidence("serial", pe.ID, ce.ID)
	case pe.Kind == KindSendEnd && ce.Kind == KindRecv:
		b.Prop += delta
		b.addEvidence("prop", pe.ID, ce.ID)
	case pe.Kind == KindSendStart && ce.Kind == KindTimeout:
		// A lost attempt: the serialization part is still sending, the
		// remainder of the timer is retransmission wait.
		var se *Event
		for _, e := range byID {
			if e.Kind == KindSendEnd && e.Link == pe.Link && e.Attempt == pe.Attempt &&
				(se == nil || e.Tick < se.Tick) {
				se = e
			}
		}
		if se != nil && se.Tick >= pe.Tick && se.Tick <= ce.Tick {
			b.Serial += se.Tick - pe.Tick
			b.RetransWait += ce.Tick - se.Tick
			b.addEvidence("serial", pe.ID, se.ID)
			b.addEvidence("retrans", se.ID, ce.ID)
		} else {
			b.RetransWait += delta
			b.addEvidence("retrans", pe.ID, ce.ID)
		}
	case pe.Kind == KindTimeout && ce.Kind == KindEnqueue:
		b.RetransWait += delta
		b.addEvidence("retrans", pe.ID, ce.ID)
	default:
		b.Other += delta
		b.addEvidence("other", pe.ID, ce.ID)
	}
}
