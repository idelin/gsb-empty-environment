package contentmatch

// DefaultMaxCandidatesPerRule bounds the number of pending partial matches
// kept per rule and stream when WithMaxCandidatesPerRule is not used.
const DefaultMaxCandidatesPerRule = 128

// Engine holds the compiled rule set: one shared multi-pattern automaton
// plus per-pattern constraint metadata. It is safe for concurrent use;
// each flow creates its own Stream.
type Engine struct {
	ac       *automaton
	pats     []Pattern  // flattened patterns, indexed by pattern ID
	rules    [][]int    // rule -> ordered pattern IDs
	refs     [][]patRef // pattern ID -> places where it occurs in rules
	maxCands int
}

type patRef struct {
	rule, idx int
}

// Option configures an Engine.
type Option func(*Engine)

// WithMaxCandidatesPerRule sets the per-stream, per-rule cap on pending
// partial matches. When the cap is exceeded the oldest candidates are
// evicted (and counted in Stream.Dropped), so memory per flow stays bounded.
func WithMaxCandidatesPerRule(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.maxCands = n
		}
	}
}

// NewEngine compiles rules into an Engine.
func NewEngine(rules []Rule, opts ...Option) (*Engine, error) {
	if err := validate(rules); err != nil {
		return nil, err
	}
	e := &Engine{
		ac:       newAutomaton(),
		maxCands: DefaultMaxCandidatesPerRule,
	}
	for _, o := range opts {
		o(e)
	}
	for ri, r := range rules {
		ids := make([]int, len(r.Patterns))
		for pi, p := range r.Patterns {
			id := len(e.pats)
			cp := p
			cp.Content = append([]byte(nil), p.Content...)
			e.pats = append(e.pats, cp)
			e.ac.add(cp.Content, id)
			e.refs = append(e.refs, []patRef{{rule: ri, idx: pi}})
			ids[pi] = id
		}
		e.rules = append(e.rules, ids)
	}
	e.ac.build()
	return e, nil
}

// NumRules returns the number of compiled rules.
func (e *Engine) NumRules() int { return len(e.rules) }

// candidate is a pending partial match of a rule: patterns [0, next) have
// matched at spans, and the candidate waits for pattern next.
type candidate struct {
	next    int
	lastEnd int
	spans   []Span
}

// Stream is the per-flow matching state. Memory use is bounded: the
// automaton itself is shared and immutable, and pending candidates are
// capped per rule (plus pruned as soon as their within/depth window has
// irrevocably passed).
type Stream struct {
	eng     *Engine
	state   int // automaton state
	pos     int // bytes consumed so far (stream offset of next byte)
	cands   [][]candidate
	dropped int
}

// NewStream creates a fresh per-flow matching state.
func (e *Engine) NewStream() *Stream {
	return &Stream{eng: e, cands: make([][]candidate, len(e.rules))}
}

// Dropped reports how many candidates were evicted due to the memory cap.
func (s *Stream) Dropped() int { return s.dropped }

// Candidates returns the current number of pending partial matches.
func (s *Stream) Candidates() int {
	n := 0
	for _, cs := range s.cands {
		n += len(cs)
	}
	return n
}

// Feed consumes one payload segment and returns all rule matches completed
// by it. Positions in Match.Spans are stream offsets. Segments may be of
// any size; patterns spanning segment boundaries are matched transparently.
func (s *Stream) Feed(chunk []byte) []Match {
	var out []Match
	for _, b := range chunk {
		s.pos++
		s.state = s.eng.ac.step(s.state, b)
		for _, pid := range s.eng.ac.nodes[s.state].out {
			end := s.pos
			s.processPattern(pid, end-len(s.eng.pats[pid].Content), end, &out)
		}
	}
	s.prune()
	return out
}

// processPattern handles one occurrence of pattern pid at [start, end).
// Every candidate that can be extended by this occurrence is extended, so
// all combinations of pattern occurrences are explored; a combination that
// violates the constraints simply leaves the other candidates alive, which
// is what lets later occurrences "backtrack" to them.
func (s *Stream) processPattern(pid, start, end int, out *[]Match) {
	pat := &s.eng.pats[pid]
	if !absOK(pat, start, end) {
		return
	}
	for _, ref := range s.eng.refs[pid] {
		if ref.idx == 0 {
			s.advance(ref.rule, candidate{}, start, end, out)
			continue
		}
		cs := s.cands[ref.rule]
		for i := 0; i < len(cs); i++ {
			c := cs[i]
			if c.next != ref.idx {
				continue
			}
			if !relOK(pat, start, end, c.lastEnd) {
				continue
			}
			s.advance(ref.rule, c, start, end, out)
		}
	}
}

// advance extends candidate c of the given rule with a match at [start,end)
// of the pattern the candidate was waiting for.
func (s *Stream) advance(rule int, c candidate, start, end int, out *[]Match) {
	spans := make([]Span, len(c.spans)+1)
	copy(spans, c.spans)
	spans[len(spans)-1] = Span{Start: start, End: end}
	if len(spans) == len(s.eng.rules[rule]) {
		*out = append(*out, Match{Rule: rule, Spans: spans})
		return
	}
	cs := append(s.cands[rule], candidate{next: len(spans), lastEnd: end, spans: spans})
	if len(cs) > s.eng.maxCands {
		excess := len(cs) - s.eng.maxCands
		s.dropped += excess
		cs = append([]candidate(nil), cs[excess:]...)
	}
	s.cands[rule] = cs
}

// prune drops candidates whose next pattern can no longer satisfy its
// within/depth window, keeping per-flow memory proportional to the windows
// actually in play.
func (s *Stream) prune() {
	for ri, cs := range s.cands {
		kept := cs[:0]
		for _, c := range cs {
			if !s.dead(ri, c) {
				kept = append(kept, c)
			}
		}
		for i := len(kept); i < len(cs); i++ {
			cs[i] = candidate{} // release spans
		}
		s.cands[ri] = kept
	}
}

// dead reports whether candidate c of rule ri can still be completed: any
// future match ends after s.pos, so a candidate is dead once s.pos has
// reached the latest end position its next pattern allows.
func (s *Stream) dead(ri int, c candidate) bool {
	p := &s.eng.pats[s.eng.rules[ri][c.next]]
	if p.Within > 0 && s.pos >= c.lastEnd+p.Distance+p.Within {
		return true
	}
	if p.Depth > 0 && s.pos >= p.Offset+p.Depth {
		return true
	}
	return false
}
