package main

import (
	"container/heap"
	"errors"
	"fmt"
	"strings"
)

const minutesPerDay = 1440

// Edge consumes an integer amount of battery and takes Time minutes to traverse.
type Edge struct {
	From   int `json:"from"`
	To     int `json:"to"`
	Energy int `json:"energy"`
	Time   int `json:"time"`
}

// Station sells a swap to a full battery. Open/Close are minutes after
// midnight and repeat daily; Close <= Open means an overnight window
// (e.g. 20:00-08:00). A swap may start at any moment inside [Open, Close).
type Station struct {
	Node     int `json:"node"`
	Fee      int `json:"fee"`
	SwapTime int `json:"swap_time"`
	Open     int `json:"open"`
	Close    int `json:"close"`
}

type Input struct {
	Capacity       int       `json:"capacity"`
	InitialBattery *int      `json:"initial_battery"` // nil means full
	Start          int       `json:"start"`
	Goal           int       `json:"goal"`
	Names          []string  `json:"names"`
	Edges          []Edge    `json:"edges"`
	Stations       []Station `json:"stations"`
}

type Step struct {
	Kind          string // "start" | "ride" | "wait+swap"
	From, To      int
	Energy        int // battery consumed by the ride
	RideTime      int
	Wait          int // minutes waited for the station to open
	Fee           int
	SwapTime      int
	BatteryBefore int
	BatteryAfter  int
	TimeAfter     int // clock time after this step
	CostAfter     int // cumulative fees after this step
}

type Plan struct {
	Found     bool
	TotalTime int
	TotalCost int
	Steps     []Step
}

// ---------------------------------------------------------------------------
// Multi-criteria label-setting search.
//
// A label is (node, battery, time, cost). Label A dominates label B at the
// same node iff A.time <= B.time && A.cost <= B.cost && A.battery >= B.battery
// (B is never preferable: it is later, not cheaper, and has less energy).
// Dominance considers BOTH time and cost, so a slower-but-cheaper label is
// never discarded in favour of a faster-but-pricier one, and vice versa.
//
// Termination with zero-energy cycles: battery is an integer in [0, Capacity],
// so per node only Capacity+1 battery levels exist. For a fixed battery level,
// mutually non-dominated (time, cost) pairs form a strictly decreasing chain
// in time (ties in time are dominated by the cheaper label). Time is a
// non-negative integer, hence the chain is finite. A zero-energy zero-time
// cycle reproduces an identical (or worse) label, which dominance prunes, so
// it cannot cause unbounded search.
// ---------------------------------------------------------------------------

type action struct {
	kind    string // "start" | "ride" | "swap"
	edge    Edge
	wait    int
	station Station
}

type label struct {
	node    int
	battery int
	time    int
	cost    int
	prev    *label
	act     action
}

func dominates(a, b *label) bool {
	return a.time <= b.time && a.cost <= b.cost && a.battery >= b.battery
}

// labelHeap is a priority queue ordered by (time, cost): the lexicographic
// objective of the whole problem (earliest arrival, then lowest cost).
type labelHeap []*label

func (h labelHeap) Len() int { return len(h) }
func (h labelHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].cost < h[j].cost
}
func (h labelHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *labelHeap) Push(x interface{}) { *h = append(*h, x.(*label)) }
func (h *labelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// nextSwapStart returns the earliest time >= t at which a swap may start.
func nextSwapStart(t, open, close int) int {
	c := close
	if c <= open {
		c += minutesPerDay // overnight window
	}
	window := c - open
	since := (t - open) % minutesPerDay
	if since < 0 {
		since += minutesPerDay
	}
	if since < window {
		return t
	}
	return t + (minutesPerDay - since)
}

func validate(in Input) error {
	if in.Capacity <= 0 {
		return errors.New("capacity must be positive")
	}
	if in.InitialBattery != nil && (*in.InitialBattery < 0 || *in.InitialBattery > in.Capacity) {
		return errors.New("initial_battery out of range [0, capacity]")
	}
	for _, e := range in.Edges {
		if e.Energy < 0 || e.Time < 0 {
			return fmt.Errorf("edge %d->%d has negative energy or time", e.From, e.To)
		}
	}
	seen := map[int]bool{}
	for _, s := range in.Stations {
		if s.Fee < 0 || s.SwapTime < 0 {
			return fmt.Errorf("station at node %d has negative fee or swap_time", s.Node)
		}
		if s.Open < 0 || s.Open >= minutesPerDay || s.Close < 0 || s.Close > minutesPerDay {
			return fmt.Errorf("station at node %d has invalid hours", s.Node)
		}
		if seen[s.Node] {
			return fmt.Errorf("duplicate station at node %d", s.Node)
		}
		seen[s.Node] = true
	}
	return nil
}

// Solve finds the lexicographically optimal plan: earliest arrival at Goal,
// then lowest total swap fees. Waiting is allowed (only useful at stations).
func Solve(in Input) (Plan, error) {
	if err := validate(in); err != nil {
		return Plan{}, err
	}
	adj := map[int][]Edge{}
	for _, e := range in.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	stations := map[int]Station{}
	for _, s := range in.Stations {
		stations[s.Node] = s
	}
	battery := in.Capacity
	if in.InitialBattery != nil {
		battery = *in.InitialBattery
	}

	accepted := map[int][]*label{} // non-dominated labels per node
	dominatedByAccepted := func(l *label) bool {
		for _, o := range accepted[l.node] {
			if dominates(o, l) {
				return true
			}
		}
		return false
	}

	pq := &labelHeap{{node: in.Start, battery: battery, act: action{kind: "start"}}}
	heap.Init(pq)

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*label)
		if dominatedByAccepted(cur) {
			continue // lazy deletion: a better label was accepted after cur was pushed
		}
		kept := make([]*label, 0, len(accepted[cur.node])+1)
		for _, o := range accepted[cur.node] {
			if !dominates(cur, o) {
				kept = append(kept, o)
			}
		}
		accepted[cur.node] = append(kept, cur)

		// Labels pop in (time, cost) order and all edge times / fees are
		// non-negative, so the first goal label popped is optimal.
		if cur.node == in.Goal {
			return buildPlan(cur), nil
		}

		for _, e := range adj[cur.node] {
			if e.Energy > cur.battery {
				continue
			}
			nl := &label{
				node:    e.To,
				battery: cur.battery - e.Energy,
				time:    cur.time + e.Time,
				cost:    cur.cost,
				prev:    cur,
				act:     action{kind: "ride", edge: e},
			}
			if !dominatedByAccepted(nl) {
				heap.Push(pq, nl)
			}
		}

		if s, ok := stations[cur.node]; ok && cur.battery < in.Capacity {
			start := nextSwapStart(cur.time, s.Open, s.Close)
			nl := &label{
				node:    cur.node,
				battery: in.Capacity,
				time:    start + s.SwapTime,
				cost:    cur.cost + s.Fee,
				prev:    cur,
				act:     action{kind: "swap", wait: start - cur.time, station: s},
			}
			if !dominatedByAccepted(nl) {
				heap.Push(pq, nl)
			}
		}
	}
	return Plan{Found: false}, nil
}

func buildPlan(goal *label) Plan {
	var rev []*label
	for l := goal; l != nil; l = l.prev {
		rev = append(rev, l)
	}
	plan := Plan{Found: true, TotalTime: goal.time, TotalCost: goal.cost}
	for i := len(rev) - 1; i >= 0; i-- {
		l := rev[i]
		switch l.act.kind {
		case "start":
			plan.Steps = append(plan.Steps, Step{
				Kind:          "start",
				From:          l.node,
				To:            l.node,
				BatteryBefore: l.battery,
				BatteryAfter:  l.battery,
				TimeAfter:     l.time,
				CostAfter:     l.cost,
			})
		case "ride":
			plan.Steps = append(plan.Steps, Step{
				Kind:          "ride",
				From:          l.act.edge.From,
				To:            l.act.edge.To,
				Energy:        l.act.edge.Energy,
				RideTime:      l.act.edge.Time,
				BatteryBefore: l.battery + l.act.edge.Energy,
				BatteryAfter:  l.battery,
				TimeAfter:     l.time,
				CostAfter:     l.cost,
			})
		case "swap":
			plan.Steps = append(plan.Steps, Step{
				Kind:          "wait+swap",
				From:          l.node,
				To:            l.node,
				Wait:          l.act.wait,
				Fee:           l.act.station.Fee,
				SwapTime:      l.act.station.SwapTime,
				BatteryBefore: l.prev.battery,
				BatteryAfter:  l.battery,
				TimeAfter:     l.time,
				CostAfter:     l.cost,
			})
		}
	}
	return plan
}

func clock(t int) string {
	return fmt.Sprintf("day %d %02d:%02d", t/minutesPerDay, (t%minutesPerDay)/60, t%60)
}

// Render produces a human-readable itinerary: per-segment remaining battery
// and every stop action (waiting, swapping).
func Render(in Input, p Plan) string {
	name := func(n int) string {
		if n >= 0 && n < len(in.Names) && in.Names[n] != "" {
			return in.Names[n]
		}
		return fmt.Sprintf("#%d", n)
	}
	var b strings.Builder
	if !p.Found {
		b.WriteString("No feasible plan: the goal is unreachable within battery and station constraints.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Arrival: t=%d min (%s)\n", p.TotalTime, clock(p.TotalTime))
	fmt.Fprintf(&b, "Total swap cost: %d\n", p.TotalCost)
	b.WriteString("Itinerary:\n")
	for i, s := range p.Steps {
		switch s.Kind {
		case "start":
			fmt.Fprintf(&b, " %2d. start at %s, battery %d/%d, t=%d (%s)\n",
				i+1, name(s.From), s.BatteryAfter, in.Capacity, s.TimeAfter, clock(s.TimeAfter))
		case "ride":
			fmt.Fprintf(&b, " %2d. ride %s -> %s: %d min, -%d battery, remaining %d/%d, arrive t=%d (%s)\n",
				i+1, name(s.From), name(s.To), s.RideTime, s.Energy, s.BatteryAfter, in.Capacity, s.TimeAfter, clock(s.TimeAfter))
		case "wait+swap":
			waitNote := ""
			if s.Wait > 0 {
				waitNote = fmt.Sprintf(" (after waiting %d min for the station to open)", s.Wait)
			}
			fmt.Fprintf(&b, " %2d. swap at %s%s: battery %d -> %d/%d, fee %d, %d min, t=%d (%s), cumulative cost %d\n",
				i+1, name(s.From), waitNote, s.BatteryBefore, s.BatteryAfter, in.Capacity, s.Fee, s.SwapTime, s.TimeAfter, clock(s.TimeAfter), s.CostAfter)
		}
	}
	return b.String()
}
