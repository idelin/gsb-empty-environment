// Package main implements a grouped rate-limiter simulator.
//
// Each group owns a reserved (minimum guaranteed) quota. While a group is
// idle, its unused share may be lent to other groups. Allocation runs in
// rounds: when a group becomes active again, its reserved share is reclaimed
// in the next round (new lending to others stops and freed capacity serves
// the owner's guarantee first), but permits already issued are never revoked.
package main

import (
	"errors"
	"fmt"
	"sort"
)

// EventKind is the type of an input event within a round.
type EventKind int

const (
	// EventRequest enqueues one permit request for a group.
	EventRequest EventKind = iota
	// EventCancel releases the group's oldest held permit; if the group
	// holds none, it drops the group's oldest pending request instead.
	EventCancel
)

// Event is a single request or cancel input.
type Event struct {
	Kind  EventKind
	Group string
}

// Grant records one issued permit: the round it was issued in, the group
// that received it, and the per-group sequence number of the request it
// satisfies (proving per-group first-come-first-served order).
type Grant struct {
	Round int
	Group string
	Req   int
}

func (g Grant) String() string {
	return fmt.Sprintf("round=%d group=%s req=%d", g.Round, g.Group, g.Req)
}

type groupState struct {
	name    string
	reserve int
	nextReq int   // sequence number assigned to the next incoming request
	pending []int // queue of waiting request sequence numbers (FIFO)
	active  []int // sequence numbers of held (not yet cancelled) permits
}

// Simulator is the grouped rate limiter.
type Simulator struct {
	total  int
	round  int
	groups map[string]*groupState
	order  []string // deterministic group order (sorted names)
	rr     int      // round-robin cursor for the lending phase
	grants []Grant  // full permit sequence across all rounds
}

// NewSimulator creates a limiter with the given total capacity and per-group
// reserved quotas. The sum of reserves must not exceed total.
func NewSimulator(total int, reserves map[string]int) (*Simulator, error) {
	if total < 0 {
		return nil, errors.New("total capacity must be non-negative")
	}
	sum := 0
	names := make([]string, 0, len(reserves))
	for name, r := range reserves {
		if r < 0 {
			return nil, fmt.Errorf("group %q: reserve must be non-negative", name)
		}
		sum += r
		names = append(names, name)
	}
	if sum > total {
		return nil, fmt.Errorf("sum of reserves (%d) exceeds total capacity (%d)", sum, total)
	}
	sort.Strings(names)
	s := &Simulator{total: total, groups: make(map[string]*groupState), order: names}
	for _, name := range names {
		s.groups[name] = &groupState{name: name, reserve: reserves[name]}
	}
	return s, nil
}

// Request enqueues one permit request for the group.
func (s *Simulator) Request(group string) error {
	g, ok := s.groups[group]
	if !ok {
		return fmt.Errorf("unknown group %q", group)
	}
	g.nextReq++
	g.pending = append(g.pending, g.nextReq)
	return nil
}

// Cancel releases the group's oldest held permit. If the group holds no
// permit, it drops the group's oldest pending request instead.
func (s *Simulator) Cancel(group string) error {
	g, ok := s.groups[group]
	if !ok {
		return fmt.Errorf("unknown group %q", group)
	}
	if len(g.active) > 0 {
		g.active = g.active[1:]
	} else if len(g.pending) > 0 {
		g.pending = g.pending[1:]
	}
	return nil
}

// Apply feeds the events of the current round into the simulator.
func (s *Simulator) Apply(events []Event) error {
	for _, e := range events {
		var err error
		switch e.Kind {
		case EventRequest:
			err = s.Request(e.Group)
		case EventCancel:
			err = s.Cancel(e.Group)
		default:
			err = fmt.Errorf("unknown event kind %d", int(e.Kind))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Allocate runs one allocation round and returns the permits granted in it.
//
// Phase 1 (guarantee): every group with waiting requests is served from its
// own reserved share first, bounded by free capacity. Because the sum of
// reserves never exceeds total capacity, a group below its reserve always
// has first claim on capacity freed by cancels — this is how a group that
// became active again reclaims its lent share in the next round.
//
// Phase 2 (lending): remaining free capacity (idle groups' reserved shares
// plus any unreserved headroom) is lent round-robin to groups that still
// have waiting requests. Already-issued permits are never revoked.
func (s *Simulator) Allocate() []Grant {
	s.round++
	free := s.total - s.TotalActive()
	before := len(s.grants)

	// Phase 1: guarantees, in deterministic group order.
	for _, name := range s.order {
		g := s.groups[name]
		quota := g.reserve - len(g.active)
		if quota < 0 {
			quota = 0
		}
		n := min3(quota, len(g.pending), free)
		for i := 0; i < n; i++ {
			s.grantLocked(g)
		}
		free -= n
	}

	// Phase 2: lend leftover capacity round-robin, rotating the starting
	// group each round for fairness.
	if free > 0 && len(s.order) > 0 {
		start := s.rr
		for free > 0 {
			progressed := false
			for i := 0; i < len(s.order) && free > 0; i++ {
				g := s.groups[s.order[(start+i)%len(s.order)]]
				if len(g.pending) > 0 {
					s.grantLocked(g)
					free--
					progressed = true
				}
			}
			if !progressed {
				break
			}
		}
		s.rr = (start + 1) % len(s.order)
	}

	return s.grants[before:]
}

// grantLocked moves the head of the group's pending queue to its active set
// and records the permit. Per-group FCFS is guaranteed by FIFO popping.
func (s *Simulator) grantLocked(g *groupState) {
	req := g.pending[0]
	g.pending = g.pending[1:]
	g.active = append(g.active, req)
	s.grants = append(s.grants, Grant{Round: s.round, Group: g.name, Req: req})
}

// Grants returns the full permit sequence issued so far, in issue order.
func (s *Simulator) Grants() []Grant {
	out := make([]Grant, len(s.grants))
	copy(out, s.grants)
	return out
}

// Round returns the number of allocation rounds run so far.
func (s *Simulator) Round() int { return s.round }

// Active returns the number of permits currently held by the group.
func (s *Simulator) Active(group string) int {
	if g, ok := s.groups[group]; ok {
		return len(g.active)
	}
	return 0
}

// Pending returns the number of requests currently waiting in the group.
func (s *Simulator) Pending(group string) int {
	if g, ok := s.groups[group]; ok {
		return len(g.pending)
	}
	return 0
}

// TotalActive returns the total number of permits currently held.
func (s *Simulator) TotalActive() int {
	n := 0
	for _, g := range s.groups {
		n += len(g.active)
	}
	return n
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
