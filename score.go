// Package swimtrust implements a local eviction and trust-decay scorer for
// Byzantine silent nodes in a SWIM-style gossip cluster.
//
// Two classes of misbehavior are scored:
//   - a node that accepts PingReq (indirect probe) requests but never relays
//     them ("silent relay");
//   - a node that forges Alive status for peers that are actually dead.
//
// Every observer keeps its own local TrustScorer. Scores start at 100 and
// decay on each confirmed violation. When a peer's score falls to or below
// QuarantineThreshold the observer force-quarantines it: it stops sending
// direct probes and PingReqs to that peer and announces the isolation.
package swimtrust

import (
	"fmt"
	"sort"
)

const (
	// InitialScore is the trust score every peer starts with.
	InitialScore = 100
	// QuarantineThreshold is the score at (or below) which a peer is
	// force-quarantined by the local node.
	QuarantineThreshold = 20
	// MinScore is the floor for trust scores.
	MinScore = 0
)

// ViolationKind classifies a confirmed misbehavior.
type ViolationKind int

const (
	// ViolationRefusePingReq: the peer accepted an indirect-probe request
	// but never relayed it nor answered within the probe round.
	ViolationRefusePingReq ViolationKind = iota
	// ViolationForgeAlive: the peer claimed a target is Alive while the
	// quorum of other helpers proved it unreachable.
	ViolationForgeAlive
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationRefusePingReq:
		return "REFUSE_PINGREQ"
	case ViolationForgeAlive:
		return "FORGE_ALIVE"
	}
	return "UNKNOWN"
}

// Penalty returns the trust-score points deducted for one violation.
func (k ViolationKind) Penalty() int {
	switch k {
	case ViolationRefusePingReq:
		return 10
	case ViolationForgeAlive:
		return 20
	}
	return 0
}

// Violation is one recorded, confirmed misbehavior against a peer.
type Violation struct {
	Round  int
	Kind   ViolationKind
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("round=%d kind=%s penalty=-%d %s", v.Round, v.Kind, v.Kind.Penalty(), v.Detail)
}

// PeerRecord is the observer-local view of one peer.
type PeerRecord struct {
	NodeID      string
	Score       int
	Violations  []Violation
	Quarantined bool
}

// TrustScorer is the local (per-observer) trust ledger. It is not shared
// state: every node keeps its own scorer and reaches its own quorum
// decisions, which is what makes the eviction "local".
type TrustScorer struct {
	Owner string
	peers map[string]*PeerRecord

	// OnQuarantine, if set, fires exactly once per peer when its score
	// first drops to or below QuarantineThreshold.
	OnQuarantine func(peerID string, score int)
	// OnViolation, if set, fires on every recorded violation.
	OnViolation func(rec PeerRecord, v Violation)
}

// NewTrustScorer creates a scorer owned by the given node id.
func NewTrustScorer(owner string) *TrustScorer {
	return &TrustScorer{Owner: owner, peers: make(map[string]*PeerRecord)}
}

func (s *TrustScorer) record(peerID string) *PeerRecord {
	rec, ok := s.peers[peerID]
	if !ok {
		rec = &PeerRecord{NodeID: peerID, Score: InitialScore}
		s.peers[peerID] = rec
	}
	return rec
}

// Record registers a confirmed violation against peerID, decays its score
// and triggers quarantine once the threshold is crossed. It returns the
// updated record.
func (s *TrustScorer) Record(peerID string, kind ViolationKind, round int, detail string) PeerRecord {
	rec := s.record(peerID)
	v := Violation{Round: round, Kind: kind, Detail: detail}
	rec.Violations = append(rec.Violations, v)
	rec.Score -= kind.Penalty()
	if rec.Score < MinScore {
		rec.Score = MinScore
	}
	if s.OnViolation != nil {
		s.OnViolation(*rec, v)
	}
	if !rec.Quarantined && rec.Score <= QuarantineThreshold {
		rec.Quarantined = true
		if s.OnQuarantine != nil {
			s.OnQuarantine(peerID, rec.Score)
		}
	}
	return *rec
}

// Score returns the current trust score for peerID (InitialScore if unseen).
func (s *TrustScorer) Score(peerID string) int {
	return s.record(peerID).Score
}

// IsQuarantined reports whether the local node has quarantined peerID.
func (s *TrustScorer) IsQuarantined(peerID string) bool {
	return s.record(peerID).Quarantined
}

// Snapshot returns a stable, id-sorted copy of all peer records.
func (s *TrustScorer) Snapshot() []PeerRecord {
	out := make([]PeerRecord, 0, len(s.peers))
	for _, rec := range s.peers {
		cp := *rec
		cp.Violations = append([]Violation(nil), rec.Violations...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}
