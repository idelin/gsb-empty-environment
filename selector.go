package decision

import (
	"fmt"
	"sort"
	"strings"
)

// Selector is a conflict-free replicated weighted-voting state. Each node
// owns one replica; replicas exchange and merge states to converge without
// coordination. Merge is commutative, associative and idempotent.
type Selector struct {
	id      string
	clock   uint64 // Lamport clock for causal ordering of local updates
	votes   map[string]Vote
	weights map[string]Weight
}

// NewSelector creates a replica owned by the given node ID.
func NewSelector(id string) *Selector {
	return &Selector{
		id:      id,
		votes:   make(map[string]Vote),
		weights: make(map[string]Weight),
	}
}

// ID returns the owning node's ID.
func (s *Selector) ID() string { return s.id }

// tick advances the Lamport clock for a local event.
func (s *Selector) tick() uint64 {
	s.clock++
	return s.clock
}

// SetWeight records a causal weight update for node. Any replica may issue
// weight updates; concurrent updates to the same node resolve
// deterministically by (Lamport time, source ID, value).
func (s *Selector) SetWeight(node string, value uint64) {
	s.weights[node] = Weight{Value: value, Time: s.tick(), Source: s.id}
}

// CastVote records this replica owner's ballot, replacing any prior ballot.
func (s *Selector) CastVote(c Choice) {
	s.votes[s.id] = Vote{Choice: c, Time: s.tick(), Voter: s.id}
}

// VoteOf returns the current ballot of a node, if known.
func (s *Selector) VoteOf(node string) (Vote, bool) {
	v, ok := s.votes[node]
	return v, ok
}

// WeightOf returns the current weight of a node, if known.
func (s *Selector) WeightOf(node string) (Weight, bool) {
	w, ok := s.weights[node]
	return w, ok
}

// Merge folds other's state into s. It is commutative, associative and
// idempotent, so replicas converge regardless of exchange order or
// duplicates. other is not modified.
func (s *Selector) Merge(other *Selector) {
	for voter, v := range other.votes {
		if cur, ok := s.votes[voter]; !ok || lessVote(cur, v) {
			s.votes[voter] = v
		}
	}
	for node, w := range other.weights {
		if cur, ok := s.weights[node]; !ok || lessWeight(cur, w) {
			s.weights[node] = w
		}
	}
	if other.clock > s.clock {
		s.clock = other.clock
	}
}

// Clone returns a deep copy of the replica.
func (s *Selector) Clone() *Selector {
	c := NewSelector(s.id)
	c.clock = s.clock
	for k, v := range s.votes {
		c.votes[k] = v
	}
	for k, w := range s.weights {
		c.weights[k] = w
	}
	return c
}

// Tally aggregates the current state into a deterministic summary. Only
// ballots from nodes with a known positive weight contribute; abstentions
// count toward the total effective weight but toward neither side.
func (s *Selector) Tally() Tally {
	var t Tally
	for node, w := range s.weights {
		t.TotalWeight += w.Value
		v, ok := s.votes[node]
		if !ok {
			t.MissingVoters = append(t.MissingVoters, node)
			continue
		}
		if w.Value == 0 {
			continue
		}
		t.VotedWeight += w.Value
		switch v.Choice {
		case Yes:
			t.YesWeight += w.Value
			t.YesCount++
		case No:
			t.NoWeight += w.Value
			t.NoCount++
		default:
			t.AbstainWeight += w.Value
			t.AbstainCount++
		}
	}
	sort.Strings(t.MissingVoters)
	return t
}

// Decision is shorthand for s.Tally().Decision().
func (s *Selector) Decision() Decision { return s.Tally().Decision() }

// String renders the replica state deterministically (sorted by node ID).
func (s *Selector) String() string {
	nodes := make([]string, 0, len(s.weights))
	for n := range s.weights {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var b strings.Builder
	fmt.Fprintf(&b, "replica %s (clock=%d)", s.id, s.clock)
	for _, n := range nodes {
		w := s.weights[n]
		fmt.Fprintf(&b, "\n  %-8s weight=%-4d (t=%d by %s)", n, w.Value, w.Time, w.Source)
		if v, ok := s.votes[n]; ok {
			fmt.Fprintf(&b, " vote=%s (t=%d)", v.Choice, v.Time)
		} else {
			b.WriteString(" vote=<none>")
		}
	}
	return b.String()
}
