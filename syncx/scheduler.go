package syncx

import (
	"sort"
	"sync"
)

// Scheduler picks which peer to probe next. Trust scores only reorder probes
// within a cycle; every peer is probed exactly once per cycle, so a low- or
// zero-trust peer can never be starved: a legal new incarnation always gets
// a chance to propagate. Trust is a scheduling hint, not an admission
// control.
type Scheduler struct {
	mu    sync.Mutex
	peers []string
	trust map[string]float64
	cycle []string
	pos   int
}

func NewScheduler(peers []string) *Scheduler {
	return &Scheduler{
		peers: append([]string(nil), peers...),
		trust: map[string]float64{},
		pos:   1 << 30, // force cycle build on first Next
	}
}

// SetTrust records a trust score in [0,1] for a peer and reshuffles the
// probe order. It never removes the peer from the cycle.
func (s *Scheduler) SetTrust(peer string, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	s.trust[peer] = v
	s.pos = 1 << 30
}

func (s *Scheduler) Trust(peer string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trust[peer]
}

// Next returns the next peer to probe. Within each cycle peers are ordered
// by descending trust, but every peer appears exactly once per cycle
// (fairness), which is what the termination argument relies on.
func (s *Scheduler) Next() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos >= len(s.cycle) {
		s.cycle = append([]string(nil), s.peers...)
		sort.SliceStable(s.cycle, func(i, j int) bool {
			return s.trust[s.cycle[i]] > s.trust[s.cycle[j]]
		})
		s.pos = 0
	}
	p := s.cycle[s.pos]
	s.pos++
	return p
}

// CycleLen is the number of probes after which every peer has been probed
// exactly once.
func (s *Scheduler) CycleLen() int { return len(s.peers) }
