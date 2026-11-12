package allocplan

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
)

type exhaustiveState struct {
	planner *Planner
	oracle  *oracleModel
}

type searchNode struct {
	state *exhaustiveState
	depth int
}

// canonicalKey freezes every observable planner decision so equivalent states
// are explored only once during the exhaustive search.
func canonicalKey(p *Planner) string {
	key := strconv.FormatInt(p.clock.Tick(), 10) + "|segs"
	for _, seg := range p.segments {
		key += fmt.Sprintf("(%d-%d:%d)", seg.Start, seg.End, seg.Tag)
	}
	key += "|hist"
	for addr := 0; addr < p.space; addr++ {
		for tag := Tag(1); int(tag) <= p.tagCount; tag++ {
			if until := p.history[addr][tag]; until >= 0 {
				key += strconv.Itoa(addr) + ":" + strconv.Itoa(int(tag)) + "=" + strconv.FormatInt(until, 10) + ","
			}
		}
	}
	key += "|live"
	for tag := Tag(1); int(tag) <= p.tagCount; tag++ {
		key += strconv.Itoa(p.live[tag]) + ","
	}
	return key
}

func (s *exhaustiveState) clone() *exhaustiveState {
	clone := &Planner{
		tagCount: s.planner.tagCount,
		space:    s.planner.space,
		quar:     s.planner.quar,
		clock:    Clock{tick: s.planner.clock.Tick()},
		segments: make([]*Segment, 0, len(s.planner.segments)),
		live:     map[Tag]int{},
		history:  make([][]int64, len(s.planner.history)),
	}
	for _, seg := range s.planner.segments {
		clone.segments = append(clone.segments, &Segment{
			Start: seg.Start,
			End:   seg.End,
			Tag:   seg.Tag,
		})
	}
	for addr := range s.planner.history {
		clone.history[addr] = append([]int64(nil), s.planner.history[addr]...)
	}
	for tag, count := range s.planner.live {
		clone.live[tag] = count
	}
	return &exhaustiveState{planner: clone, oracle: s.oracle.clone()}
}

// assertSafety checks the core dangling-pointer invariant against the
// independent per-unit oracle: live tag counts, segment boundaries, unit
// ownership and open quarantine intervals must all agree, and the segment
// table must tile the whole address space.
func (s *exhaustiveState) assertSafety(t *testing.T, where string) {
	t.Helper()
	p := s.planner
	now := p.clock.Tick()
	for tag := Tag(1); int(tag) <= p.tagCount; tag++ {
		if got, want := p.live[tag], s.oracle.live[tag]; got != want {
			t.Fatalf("%s: tag %d live count planner=%d oracle=%d", where, tag, got, want)
		}
	}
	if len(p.segments) != len(s.oracle.segments()) {
		t.Fatalf("%s: planner has %d segments, oracle has %d", where, len(p.segments), len(s.oracle.segments()))
	}
	covered := 0
	for i, seg := range p.segments {
		oSeg := s.oracle.segments()[i]
		if seg.Start != oSeg.start || seg.End != oSeg.end || (seg.Tag != NoTag) != oSeg.used {
			t.Fatalf("%s: segment %d mismatch planner=[%d,%d) tag=%d oracle=[%d,%d) used=%v", where, i, seg.Start, seg.End, seg.Tag, oSeg.start, oSeg.end, oSeg.used)
		}
		covered += seg.Length()
		if seg.Tag != NoTag && p.live[seg.Tag] == 0 {
			t.Fatalf("%s: segment %+v labels a tag with no live reference", where, seg)
		}
		for addr := seg.Start; addr < seg.End; addr++ {
			if (seg.Tag != NoTag) != s.oracle.unitUsed(addr) {
				t.Fatalf("%s: unit %d ownership mismatch", where, addr)
			}
		}
	}
	if covered != p.space {
		t.Fatalf("%s: covered %d units, want %d", where, covered, p.space)
	}
	// Every still-open planner quarantine must be backed by the oracle with at
	// least the same expiry; expired history entries must also match exactly.
	for addr := 0; addr < p.space; addr++ {
		for tag := Tag(1); int(tag) <= p.tagCount; tag++ {
			plannerUntil := p.history[addr][tag]
			oracleUntil := s.oracle.until[addr][tag]
			if plannerUntil != oracleUntil {
				t.Fatalf("%s: history addr=%d tag=%d planner=%d oracle=%d", where, addr, tag, plannerUntil, oracleUntil)
			}
			if plannerUntil > now && oracleUntil <= now {
				t.Fatalf("%s: planner keeps open quarantine addr=%d tag=%d but oracle expired", where, addr, tag)
			}
		}
	}
}

func mustNewPlanner(t *testing.T, cfg Config) *Planner {
	t.Helper()
	planner, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return planner
}

// TestExhaustiveSmallDomain explores every reachable state for a small tag
// domain and address space up to a depth bound. For each state it tries all
// allocation sizes, frees, splits and one-tick clock advances, comparing
// every decision against the independent oracle and verifying that rejection
// never silently reuses a quarantined or live tag.
func TestExhaustiveSmallDomain(t *testing.T) {
	cfg := Config{TagDomainSize: 2, AddressSpace: 3, Quarantine: 2}
	const maxDepth = 10
	root := &exhaustiveState{
		planner: mustNewPlanner(t, cfg),
		oracle:  newOracle(cfg),
	}
	visited := map[string]bool{canonicalKey(root.planner): true}
	queue := []searchNode{{state: root, depth: 0}}
	const maxStates = 200000
	explored := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		current := node.state
		explored++
		if explored > maxStates {
			t.Fatalf("exhaustive search exceeded %d states; reduce domain or depth", maxStates)
		}
		where := fmt.Sprintf("state %d depth %d", explored, node.depth)
		current.assertSafety(t, where)
		if node.depth == maxDepth {
			continue
		}
		enqueue := func(next *exhaustiveState) {
			key := canonicalKey(next.planner)
			if !visited[key] {
				visited[key] = true
				queue = append(queue, searchNode{state: next, depth: node.depth + 1})
			}
		}

		// Clock advance: reaching a quarantine expiry tick is an explicit
		// transition, so the same-tick boundary is exercised exhaustively.
		advanced := current.clone()
		advanced.planner.Clock().Advance(1)
		if advanced.planner.Clock().Tick() <= int64(maxDepth)+cfg.Quarantine {
			enqueue(advanced)
		}

		// Every positive allocation size up to the full address space.
		for size := 1; size <= cfg.AddressSpace; size++ {
			next := current.clone()
			start, tag, err := next.planner.Alloc(size)
			now := next.planner.Clock().Tick()
			oStart, oTag, waitTick, ok := next.oracle.decision(size, now)
			if err != nil {
				if ok {
					t.Fatalf("%s: planner rejected size %d but oracle accepts start=%d tag=%d: %v", where, size, oStart, oTag, err)
				}
				if errors.Is(err, ErrNoAddressSpace) {
					if ok && oStart >= 0 {
						t.Fatalf("%s: planner claims no space for size %d but oracle found %d", where, size, oStart)
					}
					continue
				}
				var exhausted *ExhaustedError
				if !errors.As(err, &exhausted) {
					t.Fatalf("%s: size %d rejection is not ExhaustedError: %v", where, size, err)
				}
				if exhausted.Waitable {
					if waitTick == 0 || exhausted.WaitTick != waitTick {
						t.Fatalf("%s: size %d wait tick planner=%d oracle=%d", where, size, exhausted.WaitTick, waitTick)
					}
				} else if waitTick != 0 {
					t.Fatalf("%s: size %d planner reports all busy but oracle wait tick is %d", where, size, waitTick)
				}
				if canonicalKey(next.planner) != canonicalKey(current.planner) {
					t.Fatalf("%s: rejected Alloc(size=%d) mutated state", where, size)
				}
				continue
			}
			if !ok || start != oStart || tag != oTag {
				t.Fatalf("%s: alloc size %d planner=(%d,%d) oracle=(%d,%d,ok=%v)", where, size, start, tag, oStart, oTag, ok)
			}
			next.oracle.alloc(start, size, tag)
			next.assertSafety(t, where+fmt.Sprintf(" alloc-size-%d", size))
			enqueue(next)
		}

		// Free every currently allocated segment start.
		for _, seg := range current.planner.Segments() {
			if seg.Tag == NoTag {
				continue
			}
			next := current.clone()
			if err := next.planner.Free(seg.Start); err != nil {
				t.Fatalf("%s: Free(%d): %v", where, seg.Start, err)
			}
			next.oracle.free(seg.Start, seg.Tag, next.planner.Clock().Tick())
			next.assertSafety(t, where+fmt.Sprintf(" free-%d", seg.Start))
			enqueue(next)
		}

		// Split every allocated segment at every interior point.
		for _, seg := range current.planner.Segments() {
			if seg.Tag == NoTag || seg.Length() < 2 {
				continue
			}
			for left := 1; left < seg.Length(); left++ {
				next := current.clone()
				mid, err := next.planner.Split(seg.Start, left)
				if err != nil || mid != seg.Start+left {
					t.Fatalf("%s: Split(%d,%d) = (%d,%v)", where, seg.Start, left, mid, err)
				}
				next.oracle.split(seg.Start, left)
				next.assertSafety(t, where+fmt.Sprintf(" split-%d-%d", seg.Start, left))
				enqueue(next)
			}
		}
	}
	if len(visited) < 200 {
		t.Fatalf("exhaustive search only visited %d states; harness is too small", len(visited))
	}
	t.Logf("exhaustively verified %d distinct states across depth %d", len(visited), maxDepth)
}
