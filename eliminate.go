package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	MaxCandidates   = 256
	MaxCandidateLen = 16
	MaxQueries      = 128
)

var (
	// ErrModelInconsistent is returned when the oracle feedback matches
	// no remaining candidate's predicted compressed length.
	ErrModelInconsistent = errors.New("netsec: oracle feedback matches no candidate: model inconsistent")

	ErrNoCandidates          = errors.New("netsec: need at least 1 candidate")
	ErrTooManyCandidates     = errors.New("netsec: more than 256 candidates")
	ErrCandidateEmpty        = errors.New("netsec: candidate shorter than 1 byte")
	ErrCandidateTooLong      = errors.New("netsec: candidate longer than 16 bytes")
	ErrCandidateHasSeparator = errors.New("netsec: candidate contains separator byte 0xFF")
	ErrTooManyQueries        = errors.New("netsec: more than 128 queries")
)

// Oracle is the host callback: it returns only the compressed length of
// secret || Separator || query for the hidden secret.
type Oracle func(query []byte) (int, error)

// Status is the elimination outcome.
type Status int

const (
	// StatusInProgress means candidates remain and no terminal condition
	// has been reached (e.g. the round budget ran out).
	StatusInProgress Status = iota
	// StatusRecovered means exactly one candidate remains.
	StatusRecovered
	// StatusIndistinguishable means no available query can shrink the
	// candidate set; the remaining group is reported, not recovered.
	StatusIndistinguishable
)

func (s Status) String() string {
	switch s {
	case StatusRecovered:
		return "recovered"
	case StatusIndistinguishable:
		return "indistinguishable"
	default:
		return "in-progress"
	}
}

// Round records the rationale of one elimination step.
type Round struct {
	Query      []byte   // chosen query
	Feedback   int      // compressed length returned by the oracle
	Before     int      // candidates before the round
	After      int      // candidates after the round
	Eliminated [][]byte // candidates ruled out by this feedback
}

func (r Round) String() string {
	return fmt.Sprintf("query=%q feedback=%d candidates %d->%d eliminated=%q",
		r.Query, r.Feedback, r.Before, r.After, r.Eliminated)
}

// Session is a resumable candidate-elimination state machine.
type Session struct {
	candidates [][]byte
	queries    [][]byte
	pred       [][]int // pred[candidate][query] = predicted compressed length
	remaining  []int   // indices into candidates
	rounds     []Round
	status     Status
}

// NewSession validates the inputs and precomputes every candidate/query
// prediction. Inputs are copied.
func NewSession(candidates, queries [][]byte) (*Session, error) {
	if len(candidates) == 0 {
		return nil, ErrNoCandidates
	}
	if len(candidates) > MaxCandidates {
		return nil, ErrTooManyCandidates
	}
	if len(queries) > MaxQueries {
		return nil, ErrTooManyQueries
	}
	s := &Session{
		candidates: make([][]byte, len(candidates)),
		queries:    make([][]byte, len(queries)),
		pred:       make([][]int, len(candidates)),
		remaining:  make([]int, len(candidates)),
	}
	for i, c := range candidates {
		if len(c) == 0 {
			return nil, fmt.Errorf("%w: index %d", ErrCandidateEmpty, i)
		}
		if len(c) > MaxCandidateLen {
			return nil, fmt.Errorf("%w: index %d", ErrCandidateTooLong, i)
		}
		if bytes.IndexByte(c, Separator) >= 0 {
			return nil, fmt.Errorf("%w: index %d", ErrCandidateHasSeparator, i)
		}
		s.candidates[i] = bytes.Clone(c)
		s.remaining[i] = i
	}
	for j, q := range queries {
		s.queries[j] = bytes.Clone(q)
	}
	for i := range s.candidates {
		s.pred[i] = make([]int, len(s.queries))
		for j := range s.queries {
			s.pred[i][j] = CompressLen(Payload(s.candidates[i], s.queries[j]))
		}
	}
	if len(s.remaining) == 1 {
		s.status = StatusRecovered
	}
	return s, nil
}

// bestQuery picks the query minimising the largest remaining candidate
// group; ties are broken by query byte order. Returns the largest group
// size for the chosen query, or -1 and a count above the remaining size
// when no queries are available.
func (s *Session) bestQuery() (bestIdx, bestMax int) {
	bestIdx, bestMax = -1, len(s.remaining)+1
	for qi := range s.queries {
		counts := make(map[int]int)
		mx := 0
		for _, ci := range s.remaining {
			c := counts[s.pred[ci][qi]] + 1
			counts[s.pred[ci][qi]] = c
			if c > mx {
				mx = c
			}
		}
		if mx < bestMax ||
			(mx == bestMax && bestIdx >= 0 && bytes.Compare(s.queries[qi], s.queries[bestIdx]) < 0) {
			bestIdx, bestMax = qi, mx
		}
	}
	return bestIdx, bestMax
}

// Step performs one elimination round. It is a no-op once a terminal
// status has been reached.
func (s *Session) Step(oracle Oracle) error {
	if s.status != StatusInProgress {
		return nil
	}
	if len(s.remaining) <= 1 {
		s.status = StatusRecovered
		return nil
	}
	qIdx, maxGroup := s.bestQuery()
	if maxGroup >= len(s.remaining) {
		// No query can shrink the set: stop and report the group.
		s.status = StatusIndistinguishable
		return nil
	}
	query := s.queries[qIdx]
	feedback, err := oracle(query)
	if err != nil {
		return fmt.Errorf("netsec: oracle: %w", err)
	}
	var keep, drop []int
	for _, ci := range s.remaining {
		if s.pred[ci][qIdx] == feedback {
			keep = append(keep, ci)
		} else {
			drop = append(drop, ci)
		}
	}
	if len(keep) == 0 {
		// State is left untouched so the session can be inspected or
		// retried with a corrected oracle.
		return ErrModelInconsistent
	}
	eliminated := make([][]byte, len(drop))
	for i, ci := range drop {
		eliminated[i] = s.candidates[ci]
	}
	s.rounds = append(s.rounds, Round{
		Query:      bytes.Clone(query),
		Feedback:   feedback,
		Before:     len(s.remaining),
		After:      len(keep),
		Eliminated: eliminated,
	})
	s.remaining = keep
	if len(keep) == 1 {
		s.status = StatusRecovered
	}
	return nil
}

// Run executes at most budget rounds. When the budget is exhausted the
// status stays StatusInProgress; call Run again (on this session or one
// restored via Import) to resume.
func (s *Session) Run(oracle Oracle, budget int) (Status, error) {
	for budget > 0 && s.status == StatusInProgress {
		if err := s.Step(oracle); err != nil {
			return s.status, err
		}
		budget--
	}
	return s.status, nil
}

// Status returns the current elimination status.
func (s *Session) Status() Status { return s.status }

// Remaining returns a copy of the candidates still in the running.
func (s *Session) Remaining() [][]byte {
	out := make([][]byte, len(s.remaining))
	for i, ci := range s.remaining {
		out[i] = s.candidates[ci]
	}
	return out
}

// Recovered returns the single remaining candidate. The boolean is false
// unless the status is StatusRecovered; an indistinguishable group is
// never reported as recovered.
func (s *Session) Recovered() ([]byte, bool) {
	if s.status != StatusRecovered || len(s.remaining) != 1 {
		return nil, false
	}
	return bytes.Clone(s.candidates[s.remaining[0]]), true
}

// Rounds returns the per-round elimination rationale.
func (s *Session) Rounds() []Round {
	out := make([]Round, len(s.rounds))
	copy(out, s.rounds)
	return out
}

// Report renders the per-round elimination rationale plus the outcome.
func (s *Session) Report() string {
	var b strings.Builder
	for i, r := range s.rounds {
		fmt.Fprintf(&b, "round %d: %s\n", i+1, r)
	}
	switch s.status {
	case StatusRecovered:
		fmt.Fprintf(&b, "recovered: %q\n", s.candidates[s.remaining[0]])
	case StatusIndistinguishable:
		fmt.Fprintf(&b, "indistinguishable group (%d candidates, not recovered): %q\n",
			len(s.remaining), s.Remaining())
	default:
		fmt.Fprintf(&b, "in progress: %d candidates remain\n", len(s.remaining))
	}
	return b.String()
}

// snapshot is the serialisable session state.
type snapshot struct {
	Candidates [][]byte `json:"candidates"`
	Queries    [][]byte `json:"queries"`
	Remaining  []int    `json:"remaining"`
	Rounds     []Round  `json:"rounds"`
	Status     Status   `json:"status"`
}

// Export serialises the session so an interrupted run can be resumed
// later, possibly in another process.
func (s *Session) Export() ([]byte, error) {
	return json.Marshal(snapshot{
		Candidates: s.candidates,
		Queries:    s.queries,
		Remaining:  s.remaining,
		Rounds:     s.rounds,
		Status:     s.status,
	})
}

// Import restores a session previously saved with Export.
func Import(data []byte) (*Session, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("netsec: import: %w", err)
	}
	s, err := NewSession(snap.Candidates, snap.Queries)
	if err != nil {
		return nil, fmt.Errorf("netsec: import: %w", err)
	}
	for _, ci := range snap.Remaining {
		if ci < 0 || ci >= len(s.candidates) {
			return nil, fmt.Errorf("netsec: import: remaining index %d out of range", ci)
		}
	}
	s.remaining = append([]int(nil), snap.Remaining...)
	s.rounds = snap.Rounds
	s.status = snap.Status
	return s, nil
}
