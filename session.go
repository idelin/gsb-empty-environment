package replicaread

import (
	"fmt"
	"sort"
)

// Effects describes what the caller must do after a response has been
// handled: publish a newly decided receipt and/or issue read-repairs.
type Effects struct {
	// Decided is set when this response completed a generation (the
	// first quorum, or a new generation opened by a late higher
	// version). Nil otherwise.
	Decided *Receipt
	// Repairs targets only replicas that have already confirmed an
	// older version than the decided one.
	Repairs []Repair
}

// Session collects replica responses for one read and decides results
// generation by generation. It is not safe for concurrent use; the
// caller controls the order in which responses are delivered via
// Handle.
type Session struct {
	quorum int

	responses map[string]Response
	order     []string // response arrival order, for deterministic output

	generation uint64
	current    *Receipt
	history    []Receipt
}

// NewSession starts a read that decides once quorum distinct replicas
// have answered.
func NewSession(quorum int) (*Session, error) {
	if quorum < 1 {
		return nil, fmt.Errorf("replicaread: quorum must be at least 1, got %d", quorum)
	}
	return &Session{quorum: quorum, responses: make(map[string]Response)}, nil
}

// Quorum returns the number of distinct replies required to decide the
// first generation.
func (s *Session) Quorum() int { return s.quorum }

// Generation returns the current generation number; 0 means nothing
// has been decided yet.
func (s *Session) Generation() uint64 { return s.generation }

// Current returns a copy of the latest decided receipt, or nil if no
// generation has been decided yet.
func (s *Session) Current() *Receipt {
	if s.current == nil {
		return nil
	}
	r := s.current.clone()
	return &r
}

// History returns copies of every decided receipt, oldest first. Past
// receipts are never mutated, even when a late higher version opens a
// new generation.
func (s *Session) History() []Receipt {
	out := make([]Receipt, len(s.history))
	for i, r := range s.history {
		out[i] = r.clone()
	}
	return out
}

// Handle feeds one replica response into the session. Responses are
// applied in call order; a replica's first answer stands and later
// duplicates are ignored. It returns a *ConflictError if the response
// carries the same version as a known response but different content.
func (s *Session) Handle(resp Response) (Effects, error) {
	var fx Effects
	if _, dup := s.responses[resp.Replica]; dup {
		return fx, nil
	}
	if err := s.checkConflict(resp); err != nil {
		return fx, err
	}
	s.responses[resp.Replica] = resp
	s.order = append(s.order, resp.Replica)

	if s.current == nil {
		if len(s.responses) < s.quorum {
			return fx, nil
		}
		receipt := s.decide()
		fx.Decided = &receipt
		fx.Repairs = s.repairsFor(receipt)
		return fx, nil
	}

	switch resp.Version.Compare(s.current.Version) {
	case 1:
		// A late higher version opens a new generation. The previous
		// receipt stays untouched in the history.
		receipt := s.decide()
		fx.Decided = &receipt
		fx.Repairs = s.repairsFor(receipt)
	case -1:
		// This replica confirmed it is behind: repair only it.
		fx.Repairs = []Repair{{
			Replica: resp.Replica,
			Version: s.current.Version,
			Content: s.current.Content.clone(),
		}}
	}
	return fx, nil
}

// checkConflict verifies that no known response carries the same
// version with different content.
func (s *Session) checkConflict(resp Response) error {
	var disagreeing []string
	for id, known := range s.responses {
		if known.Version == resp.Version && !known.Content.Equal(resp.Content) {
			disagreeing = append(disagreeing, id)
		}
	}
	if len(disagreeing) == 0 {
		return nil
	}
	disagreeing = append(disagreeing, resp.Replica)
	sort.Strings(disagreeing)
	return &ConflictError{Version: resp.Version, Replicas: disagreeing}
}

// decide freezes the current knowledge into a new generation's
// receipt: the highest version wins and every replica that vouched for
// it is listed in Acks, in arrival order.
func (s *Session) decide() Receipt {
	s.generation++
	receipt := Receipt{Generation: s.generation}
	first := true
	for _, id := range s.order {
		resp := s.responses[id]
		switch {
		case first || resp.Version.Compare(receipt.Version) > 0:
			receipt.Version = resp.Version
			receipt.Content = resp.Content.clone()
			receipt.Acks = []string{id}
			first = false
		case resp.Version.Compare(receipt.Version) == 0:
			receipt.Acks = append(receipt.Acks, id)
		}
	}
	s.history = append(s.history, receipt.clone())
	s.current = &receipt
	return receipt.clone()
}

// repairsFor emits a repair for every replica that has already
// answered with a version older than the receipt's. Replicas that have
// not answered yet are never repaired, because their state is
// unconfirmed.
func (s *Session) repairsFor(receipt Receipt) []Repair {
	var repairs []Repair
	for _, id := range s.order {
		resp := s.responses[id]
		if resp.Version.Compare(receipt.Version) < 0 {
			repairs = append(repairs, Repair{
				Replica: id,
				Version: receipt.Version,
				Content: receipt.Content.clone(),
			})
		}
	}
	return repairs
}
