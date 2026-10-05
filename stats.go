package gsb

import "sync"

// RoundStat captures byte and cursor accounting for one sync round.
type RoundStat struct {
	Peer          string
	ReqBytes      int
	RespBytes     int
	RecordsPulled int
	CursorAdvance int // records accepted through cursors this round
	Restarts      int // snapshot-invalidated cursor restarts
	Rejected      int // rejected (oversized, stale, forged, duplicate) messages
}

func (r RoundStat) Bytes() int { return r.ReqBytes + r.RespBytes }

// Stats aggregates per-round statistics.
type Stats struct {
	mu     sync.Mutex
	rounds []RoundStat
}

func (s *Stats) Add(rs RoundStat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds = append(s.rounds, rs)
}

func (s *Stats) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rounds)
}

func (s *Stats) Last() (RoundStat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rounds) == 0 {
		return RoundStat{}, false
	}
	return s.rounds[len(s.rounds)-1], true
}

// Totals aggregates all rounds: round count, bytes, records pulled,
// restarts and rejections.
func (s *Stats) Totals() (rounds, bytes, pulled, restarts, rejected int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rounds {
		rounds++
		bytes += r.Bytes()
		pulled += r.RecordsPulled
		restarts += r.Restarts
		rejected += r.Rejected
	}
	return
}
