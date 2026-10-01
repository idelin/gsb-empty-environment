package main

import (
	"fmt"
	"sort"
	"strings"
)

// packet is one discrete unit moving hop by hop through the event model.
type packet struct {
	flow int // index into []resolvedFlow
	hop  int // index of the usage key the packet is currently queued for
}

// simLink is one capacity budget with a FIFO queue, serviced each tick.
type simLink struct {
	key        string
	capacity   float64 // packets per tick
	queue      []packet
	serveAcc   float64 // fractional service accumulator
	received   int     // packets enqueued (from sources and upstream links)
	forwarded  int
	maxBacklog int
	midBacklog int // backlog at half of the simulation
	backlog    int // backlog at end of simulation
}

// congested reports sustained backlog growth: a link whose offered rate
// exceeds capacity accumulates backlog roughly linearly, while a healthy
// link only carries a small constant in-flight residue.
func (s *simLink) congested() bool { return s.backlog > s.midBacklog }

type simResult struct {
	ticks     int
	links     map[string]*simLink
	injected  map[int]int
	delivered map[int]int
}

// simulate runs the discrete event model: integer ticks only, no real
// network traffic. Each tick: sources inject packets according to demand
// (fractional rates via an accumulator), then every link serves up to its
// capacity from its FIFO queue. Forwarded packets advance to the next hop's
// queue. Unreachable flows are excluded: they inject nothing.
func simulate(resolved []resolvedFlow, ticks int) *simResult {
	links := map[string]*simLink{}
	paths := map[int][]string{}
	for i, rf := range resolved {
		if rf.unreachable {
			continue
		}
		keys := make([]string, 0, len(rf.hops))
		for _, h := range rf.hops {
			k := usageKey(h.link, h.dir)
			keys = append(keys, k)
			if _, ok := links[k]; !ok {
				links[k] = &simLink{key: k, capacity: h.link.Capacity}
			}
		}
		paths[i] = keys
	}
	res := &simResult{
		ticks:     ticks,
		links:     links,
		injected:  map[int]int{},
		delivered: map[int]int{},
	}
	injectAcc := map[int]float64{}
	linkKeys := make([]string, 0, len(links))
	for k := range links {
		linkKeys = append(linkKeys, k)
	}
	sort.Strings(linkKeys) // deterministic service order

	enqueue := func(k string, p packet) {
		sl := links[k]
		sl.queue = append(sl.queue, p)
		sl.received++
	}

	for tick := 0; tick < ticks; tick++ {
		// 1. Sources inject.
		for i, rf := range resolved {
			if rf.unreachable {
				continue
			}
			injectAcc[i] += rf.flow.Demand
			n := int(injectAcc[i] + eps)
			injectAcc[i] -= float64(n)
			for j := 0; j < n; j++ {
				enqueue(paths[i][0], packet{flow: i, hop: 0})
				res.injected[i]++
			}
		}
		// 2. Links serve their queues.
		for _, k := range linkKeys {
			sl := links[k]
			sl.serveAcc += sl.capacity
			n := int(sl.serveAcc + eps)
			sl.serveAcc -= float64(n)
			if n > len(sl.queue) {
				n = len(sl.queue)
			}
			for j := 0; j < n; j++ {
				p := sl.queue[j]
				sl.forwarded++
				nextHop := p.hop + 1
				if nextHop >= len(paths[p.flow]) {
					res.delivered[p.flow]++
				} else {
					enqueue(paths[p.flow][nextHop], packet{flow: p.flow, hop: nextHop})
				}
			}
			sl.queue = sl.queue[n:]
			if len(sl.queue) > sl.maxBacklog {
				sl.maxBacklog = len(sl.queue)
			}
		}
		if tick == ticks/2 {
			for _, sl := range links {
				sl.midBacklog = len(sl.queue)
			}
		}
	}
	for _, sl := range links {
		sl.backlog = len(sl.queue)
	}
	return res
}

func renderSim(res *simResult, resolved []resolvedFlow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  Simulation (%d ticks, discrete event model):\n", res.ticks)
	keys := make([]string, 0, len(res.links))
	for k := range res.links {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sl := res.links[k]
		note := ""
		if sl.congested() {
			note = "  CONGESTED (backlog keeps growing)"
		}
		fmt.Fprintf(&b, "    link %-12s received=%d forwarded=%d maxBacklog=%d endBacklog=%d%s\n",
			k, sl.received, sl.forwarded, sl.maxBacklog, sl.backlog, note)
	}
	for i, rf := range resolved {
		if rf.unreachable {
			fmt.Fprintf(&b, "    flow %s: unreachable, 0 packets injected (not a success)\n", rf.flow.ID)
			continue
		}
		inj := res.injected[i]
		del := res.delivered[i]
		inflight := inj - del
		fmt.Fprintf(&b, "    flow %s: injected=%d delivered=%d inFlightOrQueued=%d\n",
			rf.flow.ID, inj, del, inflight)
	}
	return b.String()
}
