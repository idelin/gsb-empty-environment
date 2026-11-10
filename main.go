package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// segment covers the half-open interval [start, end). Segments form a sorted,
// gap-free partition of [0, high). A zero gen means offsets never leased, so
// they cannot be acknowledged.
type segment struct {
	start int64
	end   int64
	gen   int64
	ack   bool
}

type shard struct {
	segs      []segment
	high      int64
	committed int64
}

// ensureCoverage makes [0, end) represented, extending with unleased offsets.
func (s *shard) ensureCoverage(end int64) {
	if end > s.high {
		s.segs = append(s.segs, segment{start: s.high, end: end})
		s.high = end
	}
}

// split introduces a segment boundary at x; x must lie within [0, high].
func (s *shard) split(x int64) {
	i := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].start >= x })
	if i < len(s.segs) && s.segs[i].start == x {
		return
	}
	prev := s.segs[i-1]
	if prev.end <= x {
		return
	}
	left := segment{start: prev.start, end: x, gen: prev.gen, ack: prev.ack}
	right := segment{start: x, end: prev.end, gen: prev.gen, ack: prev.ack}
	s.segs = append(s.segs[:i-1], append([]segment{left, right}, s.segs[i:]...)...)
}

// merge joins adjacent segments with the same state, never crossing the
// committed boundary so the frozen prefix stays addressable.
func (s *shard) merge() {
	out := s.segs[:0]
	for _, seg := range s.segs {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.end == seg.start && seg.start != s.committed &&
				last.gen == seg.gen && last.ack == seg.ack {
				last.end = seg.end
				continue
			}
		}
		out = append(out, seg)
	}
	s.segs = out
}

// lease reassigns [lo, hi): only offsets whose existing generation is strictly
// below gen take the new generation and lose any uncommitted acknowledgement.
func (s *shard) lease(lo, hi, gen int64) {
	if lo < s.committed {
		lo = s.committed
	}
	if lo >= hi {
		return
	}
	s.ensureCoverage(hi)
	s.split(lo)
	s.split(hi)
	for i := range s.segs {
		seg := &s.segs[i]
		if seg.start >= hi {
			break
		}
		if seg.end <= lo {
			continue
		}
		if seg.gen < gen {
			seg.gen = gen
			seg.ack = false
		}
	}
	s.merge()
}

// ack acknowledges [lo, hi) only where the current generation matches gen.
func (s *shard) ack(lo, hi, gen int64) {
	if gen <= 0 {
		return
	}
	if lo < s.committed {
		lo = s.committed
	}
	if lo >= hi {
		return
	}
	s.ensureCoverage(hi)
	s.split(lo)
	s.split(hi)
	for i := range s.segs {
		seg := &s.segs[i]
		if seg.start >= hi {
			break
		}
		if seg.end <= lo {
			continue
		}
		if seg.gen == gen {
			seg.ack = true
		}
	}
	s.merge()
}

// advance extends the permanently frozen prefix through consecutive
// acknowledged offsets starting from the current committed position.
func (s *shard) advance() {
	i := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].start >= s.committed })
	for i < len(s.segs) && s.segs[i].start == s.committed && s.segs[i].ack {
		s.committed = s.segs[i].end
		i++
	}
}

func main() {
	var events []json.RawMessage
	if err := json.NewDecoder(os.Stdin).Decode(&events); err != nil {
		fmt.Fprintln(os.Stderr, "decode events:", err)
		os.Exit(1)
	}

	state := &shard{}
	breakpoints := make([]int64, 0, len(events))

	for _, raw := range events {
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 4 {
			fmt.Fprintln(os.Stderr, "invalid event:", string(raw))
			os.Exit(1)
		}
		var kind string
		var lo, hi, gen int64
		if err := json.Unmarshal(parts[0], &kind); err != nil {
			fmt.Fprintln(os.Stderr, "invalid event type:", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(parts[1], &lo); err != nil {
			fmt.Fprintln(os.Stderr, "invalid start:", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(parts[2], &hi); err != nil {
			fmt.Fprintln(os.Stderr, "invalid end:", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(parts[3], &gen); err != nil {
			fmt.Fprintln(os.Stderr, "invalid generation:", err)
			os.Exit(1)
		}
		if lo < 0 || hi < lo {
			fmt.Fprintln(os.Stderr, "invalid interval:", string(raw))
			os.Exit(1)
		}

		switch kind {
		case "lease":
			state.lease(lo, hi, gen)
		case "ack":
			state.ack(lo, hi, gen)
		default:
			fmt.Fprintln(os.Stderr, "unknown event type:", kind)
			os.Exit(1)
		}
		state.advance()
		breakpoints = append(breakpoints, state.committed)
	}

	out, err := json.Marshal(breakpoints)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode breakpoints:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
