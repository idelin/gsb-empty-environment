// Package trust implements a local interaction-credit scorer used to detect
// and quarantine Byzantine silent nodes: peers that accept Ping messages but
// refuse to relay PingReq probes, or that forge Alive states for dead members.
//
// Every node keeps its own local Scorer view of its peers (local eviction:
// no global consensus is required). Scores start at 100, decay on confirmed
// violations, recover slowly on good behavior, and once they fall to or below
// the quarantine threshold the peer is forcibly isolated.
package trust

import (
	"fmt"
	"sync"
)

// NodeID identifies a cluster member.
type NodeID string

// Violation is a confirmed misbehavior observed against a peer.
type Violation int

const (
	// ViolationPingReqRefused: the peer accepted our PingReq but never
	// relayed it nor returned an Ack within the probe window.
	ViolationPingReqRefused Violation = iota
	// ViolationAliveForged: the peer disseminated an Alive state for a
	// member that independent cross-checks prove to be unreachable.
	ViolationAliveForged
)

func (v Violation) String() string {
	switch v {
	case ViolationPingReqRefused:
		return "PINGREQ_REFUSED"
	case ViolationAliveForged:
		return "ALIVE_FORGED"
	}
	return "UNKNOWN"
}

// Event is emitted when a peer is quarantined.
type Event struct {
	Node                  NodeID
	Score                 float64
	ConsecutiveViolations int
	Reason                Violation
}

func (e Event) String() string {
	return fmt.Sprintf("QUARANTINE node=%s score=%.0f consecutive_violations=%d reason=%s",
		e.Node, e.Score, e.ConsecutiveViolations, e.Reason)
}

// Config tunes the scoring policy.
type Config struct {
	InitialScore         float64
	MaxScore             float64
	MinScore             float64
	QuarantineThreshold  float64 // quarantine when score <= threshold
	PenaltyPingReqRefuse float64
	PenaltyAliveForge    float64
	RewardRelay          float64 // peer correctly relayed a PingReq
	RewardAck            float64 // peer answered a direct probe
}

// DefaultConfig returns the policy used by the acceptance scenario:
// score 100, five consecutive refused PingReqs (-20 each) slide the score
// to exactly 20, which triggers forced isolation.
func DefaultConfig() Config {
	return Config{
		InitialScore:         100,
		MaxScore:             100,
		MinScore:             0,
		QuarantineThreshold:  20,
		PenaltyPingReqRefuse: 20,
		PenaltyAliveForge:    25,
		RewardRelay:          4,
		RewardAck:            2,
	}
}

// Scorer is the local trust view one node keeps over its peers.
// It is safe for concurrent use.
type Scorer struct {
	cfg Config

	mu          sync.Mutex
	scores      map[NodeID]float64
	consecutive map[NodeID]int
	quarantined map[NodeID]bool
	events      []Event
}

// NewScorer creates a Scorer with the given policy.
func NewScorer(cfg Config) *Scorer {
	return &Scorer{
		cfg:         cfg,
		scores:      make(map[NodeID]float64),
		consecutive: make(map[NodeID]int),
		quarantined: make(map[NodeID]bool),
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Score returns the current trust score of a peer (InitialScore if unseen).
func (s *Scorer) Score(id NodeID) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scoreLocked(id)
}

func (s *Scorer) scoreLocked(id NodeID) float64 {
	if v, ok := s.scores[id]; ok {
		return v
	}
	return s.cfg.InitialScore
}

// IsQuarantined reports whether the peer has been forcibly isolated.
func (s *Scorer) IsQuarantined(id NodeID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quarantined[id]
}

// ConsecutiveViolations returns the peer's current violation streak.
func (s *Scorer) ConsecutiveViolations(id NodeID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutive[id]
}

// Events returns a copy of all quarantine events emitted so far.
func (s *Scorer) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// RecordViolation applies the decay penalty for a confirmed violation.
// It returns the new score and whether the peer became quarantined by
// this call. Quarantine is permanent: once isolated, a peer stays isolated.
func (s *Scorer) RecordViolation(id NodeID, v Violation) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.quarantined[id] {
		return s.scoreLocked(id), false
	}

	penalty := s.cfg.PenaltyPingReqRefuse
	if v == ViolationAliveForged {
		penalty = s.cfg.PenaltyAliveForge
	}

	s.consecutive[id]++
	score := clamp(s.scoreLocked(id)-penalty, s.cfg.MinScore, s.cfg.MaxScore)
	s.scores[id] = score

	if score <= s.cfg.QuarantineThreshold {
		s.quarantined[id] = true
		s.events = append(s.events, Event{
			Node:                  id,
			Score:                 score,
			ConsecutiveViolations: s.consecutive[id],
			Reason:                v,
		})
		return score, true
	}
	return score, false
}

// RecordRelay rewards a peer that correctly relayed a PingReq for us.
func (s *Scorer) RecordRelay(id NodeID) float64 {
	return s.reward(id, s.cfg.RewardRelay)
}

// RecordAck rewards a peer that answered a direct probe.
func (s *Scorer) RecordAck(id NodeID) float64 {
	return s.reward(id, s.cfg.RewardAck)
}

func (s *Scorer) reward(id NodeID, delta float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quarantined[id] {
		return s.scoreLocked(id)
	}
	// Good behavior resets the consecutive-violation streak.
	s.consecutive[id] = 0
	score := clamp(s.scoreLocked(id)+delta, s.cfg.MinScore, s.cfg.MaxScore)
	s.scores[id] = score
	return score
}

// ForceQuarantine marks a peer as quarantined based on a trusted isolation
// announcement from another honest node, pinning the local score at the
// quarantine threshold. Quarantine is permanent.
func (s *Scorer) ForceQuarantine(id NodeID, reason Violation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quarantined[id] {
		return
	}
	s.quarantined[id] = true
	s.scores[id] = clamp(s.cfg.QuarantineThreshold, s.cfg.MinScore, s.cfg.MaxScore)
	s.events = append(s.events, Event{
		Node:                  id,
		Score:                 s.scores[id],
		ConsecutiveViolations: s.consecutive[id],
		Reason:                reason,
	})
}
