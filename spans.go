package colarchive

import (
	"fmt"
	"sort"
)

// ByteInterval is a half-open byte range [Start, Start+Length).
type ByteInterval struct {
	Start  int64
	Length int64
}

// End returns Start+Length.
func (r ByteInterval) End() int64 { return r.Start + r.Length }

// Validate checks the interval.
func (r ByteInterval) Validate() error {
	if r.Start < 0 || r.Length < 0 {
		return fmt.Errorf("colarchive: invalid byte interval [%d,%d)", r.Start, r.End())
	}
	if r.Length > 0 && r.Start > (int64(^uint64(0)>>1))-r.Length {
		return fmt.Errorf("colarchive: byte interval end overflows")
	}
	return nil
}

// spanRun is one arithmetic run of required byte intervals.  Copy k is
// [Start+k*Step, End+k*Step).  Count is at least one; Step is zero exactly
// when Count is one.
type spanRun struct {
	Start int64
	End   int64
	Step  int64
	Count int64
}

func (r spanRun) length() int64 { return r.End - r.Start }

func (r spanRun) at(k int64) ByteInterval {
	off := k * r.Step
	return ByteInterval{Start: r.Start + off, Length: r.End - r.Start}
}

// runsFromMapping converts compact logical/physical map pieces into
// arithmetic runs of required byte ranges.  It never expands an arithmetic
// progression.
func runsFromMapping(m *mappingResult) ([]spanRun, error) {
	var out []spanRun
	for _, p := range m.pieces {
		runs, err := pieceSpanRuns(p)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	return out, nil
}

func pieceSpanRuns(p MapPiece) ([]spanRun, error) {
	if p.Null || p.RowCount <= 0 {
		return nil, nil
	}
	if p.PhysicalStep <= 0 {
		return nil, fmt.Errorf("colarchive: non-positive physical step")
	}
	if p.PhysicalStep == 1 {
		first := p.PhysicalStart >> 3
		last := (p.PhysicalStart + p.RowCount - 1) >> 3
		return []spanRun{{Start: first, End: last + 1, Count: 1}}, nil
	}
	g := gcd64(p.PhysicalStep, 8)
	periodRows := 8 / g // at most eight
	periodBytes, ok := mulInt64(periodRows, p.PhysicalStep)
	if !ok {
		return nil, fmt.Errorf("colarchive: physical span overflow")
	}
	periodBytes /= 8

	baseBit := p.PhysicalStart
	baseByte := baseBit >> 3
	full := p.RowCount / periodRows
	rem := p.RowCount % periodRows

	var firstOffsets []int64
	n := periodRows
	if p.RowCount < n {
		n = p.RowCount
	}
	for j := int64(0); j < n; j++ {
		b := (p.PhysicalStart + j*p.PhysicalStep) >> 3
		firstOffsets = append(firstOffsets, b-baseByte)
	}
	firstRuns := mergeOffsets(firstOffsets)

	var out []spanRun
	if full > 0 {
		for _, iv := range firstRuns {
			out = append(out, spanRun{
				Start: baseByte + iv.Start,
				End:   baseByte + iv.End(),
				Step:  periodBytes,
				Count: full,
			})
		}
	}
	if rem > 0 {
		tailBase := baseByte
		if full > 0 {
			shift, ok := mulInt64(full, periodBytes)
			if !ok {
				return nil, fmt.Errorf("colarchive: tail offset overflow")
			}
			tailBase += shift
		}
		var tailOffsets []int64
		for j := int64(0); j < rem; j++ {
			b := (p.PhysicalStart + j*p.PhysicalStep) >> 3
			tailOffsets = append(tailOffsets, b-baseByte)
		}
		for _, iv := range mergeOffsets(tailOffsets) {
			out = append(out, spanRun{
				Start: tailBase + iv.Start,
				End:   tailBase + iv.End(),
				Count: 1,
			})
		}
	}
	return out, nil
}

func mergeOffsets(offsets []int64) []ByteInterval {
	if len(offsets) == 0 {
		return nil
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	out := make([]ByteInterval, 0, len(offsets))
	start := offsets[0]
	prev := offsets[0]
	for _, x := range offsets[1:] {
		if x <= prev {
			continue
		}
		if x == prev+1 {
			prev = x
			continue
		}
		out = append(out, ByteInterval{Start: start, Length: prev - start + 1})
		start, prev = x, x
	}
	out = append(out, ByteInterval{Start: start, Length: prev - start + 1})
	return out
}

// coalesceRuns canonicalises runs that are already contiguous because their
// copies touch or overlap, and merges explicit intervals.  The result is a
// mix of arithmetic runs and single intervals.
func coalesceRuns(runs []spanRun) ([]spanRun, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	var singles []ByteInterval
	var arithmetic []spanRun
	for _, r := range runs {
		if r.Count <= 0 || r.End <= r.Start {
			return nil, fmt.Errorf("colarchive: invalid span run")
		}
		if r.Count == 1 || r.Step <= 0 {
			singles = append(singles, ByteInterval{Start: r.Start, Length: r.length()})
			continue
		}
		if r.Step <= r.length() {
			lastOff, ok := mulInt64(r.Count-1, r.Step)
			if !ok {
				return nil, fmt.Errorf("colarchive: span run overflow")
			}
			singles = append(singles, ByteInterval{Start: r.Start, Length: r.length() + lastOff})
			continue
		}
		arithmetic = append(arithmetic, r)
	}
	if len(singles) > 0 {
		merged, err := mergeByteIntervals(singles)
		if err != nil {
			return nil, err
		}
		for _, iv := range merged {
			arithmetic = append(arithmetic, spanRun{Start: iv.Start, End: iv.End(), Count: 1})
		}
	}
	sort.Slice(arithmetic, func(i, j int) bool {
		if arithmetic[i].Start != arithmetic[j].Start {
			return arithmetic[i].Start < arithmetic[j].Start
		}
		return arithmetic[i].End < arithmetic[j].End
	})
	return arithmetic, nil
}

func mergeByteIntervals(in []ByteInterval) ([]ByteInterval, error) {
	if len(in) == 0 {
		return nil, nil
	}
	xs := append([]ByteInterval(nil), in...)
	for _, r := range xs {
		if err := r.Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Start != xs[j].Start {
			return xs[i].Start < xs[j].Start
		}
		return xs[i].End() < xs[j].End()
	})
	out := make([]ByteInterval, 0, len(xs))
	cur := xs[0]
	for _, r := range xs[1:] {
		if r.Start <= cur.End() {
			if r.End() > cur.End() {
				cur.Length = r.End() - cur.Start
			}
			continue
		}
		out = append(out, cur)
		cur = r
	}
	out = append(out, cur)
	return out, nil
}

func totalIntervals(runs []spanRun) (int64, bool) {
	var total int64
	for _, r := range runs {
		var ok bool
		total, ok = addInt64(total, r.Count)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

// expandRuns expands at most limit arithmetic intervals, then merges
// adjacent/overlapping byte ranges.
func expandRuns(runs []spanRun, limit int64) ([]ByteInterval, bool, error) {
	total, ok := totalIntervals(runs)
	if !ok || total > limit {
		return nil, false, nil
	}
	out := make([]ByteInterval, 0, int(total))
	for _, r := range runs {
		for k := int64(0); k < r.Count; k++ {
			out = append(out, r.at(k))
		}
	}
	merged, err := mergeByteIntervals(out)
	if err != nil {
		return nil, false, err
	}
	return merged, true, nil
}

// periodicReq describes required intervals repeated on a byte period:
//
//	for k in [0,Copies): interval j at Origin+k*Period+Intervals[j].Start
//
// Intervals are relative to Origin and sorted, half-open, in [0,Period).
type periodicReq struct {
	Origin    int64
	Period    int64
	Intervals []ByteInterval
	Copies    int64
}

// buildPeriodic converts large arithmetic runs into a small number of
// constant-pattern periodic segments.  Explicit/single intervals are
// returned separately.  Runs whose copies overlap are already coalesced by
// coalesceRuns and therefore arrive here as singles.
func buildPeriodic(runs []spanRun) ([]periodicReq, []ByteInterval, error) {
	type phasedRun struct {
		startPeriod int64
		count       int64
		intervals   []ByteInterval // relative to the group origin period
	}
	byPeriod := map[int64][]spanRun{}
	var singles []ByteInterval
	for _, r := range runs {
		if r.Count == 1 || r.Step <= 0 {
			singles = append(singles, ByteInterval{Start: r.Start, Length: r.length()})
			continue
		}
		if r.Step <= r.length() {
			// Should have been coalesced.
			lastOff, ok := mulInt64(r.Count-1, r.Step)
			if !ok {
				return nil, nil, fmt.Errorf("colarchive: span run overflow")
			}
			singles = append(singles, ByteInterval{Start: r.Start, Length: r.length() + lastOff})
			continue
		}
		byPeriod[r.Step] = append(byPeriod[r.Step], r)
	}

	var periods []int64
	for p := range byPeriod {
		periods = append(periods, p)
	}
	sort.Slice(periods, func(i, j int) bool { return periods[i] < periods[j] })

	var out []periodicReq
	for _, period := range periods {
		group := byPeriod[period]
		origin := group[0].Start
		for _, r := range group[1:] {
			if r.Start < origin {
				origin = r.Start
			}
		}
		var phases []phasedRun
		breakSet := map[int64]struct{}{0: {}}
		for _, r := range group {
			diff := r.Start - origin
			p0 := diff / period
			off := diff % period
			length := r.length()
			if off+length <= period {
				phases = append(phases, phasedRun{
					startPeriod: p0,
					count:       r.Count,
					intervals:   []ByteInterval{{Start: off, Length: length}},
				})
				breakSet[p0] = struct{}{}
				breakSet[p0+r.Count] = struct{}{}
				continue
			}
			// The interval crosses a period boundary.  Its tail belongs to
			// the *next* period, not to the same period.
			phases = append(phases,
				phasedRun{
					startPeriod: p0,
					count:       r.Count,
					intervals:   []ByteInterval{{Start: off, Length: period - off}},
				},
				phasedRun{
					startPeriod: p0 + 1,
					count:       r.Count,
					intervals:   []ByteInterval{{Start: 0, Length: off + length - period}},
				},
			)
			breakSet[p0] = struct{}{}
			breakSet[p0+r.Count] = struct{}{}
			breakSet[p0+1] = struct{}{}
			breakSet[p0+1+r.Count] = struct{}{}
		}
		var breaks []int64
		for b := range breakSet {
			breaks = append(breaks, b)
		}
		sort.Slice(breaks, func(i, j int) bool { return breaks[i] < breaks[j] })
		for bi := 0; bi+1 < len(breaks); bi++ {
			lo, hi := breaks[bi], breaks[bi+1]
			if hi <= lo {
				continue
			}
			var rel []ByteInterval
			for _, ph := range phases {
				if ph.startPeriod <= lo && ph.startPeriod+ph.count >= hi {
					rel = append(rel, ph.intervals...)
				}
			}
			if len(rel) == 0 {
				continue
			}
			rel, err := mergeByteIntervals(rel)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, periodicReq{
				Origin:    origin + lo*period,
				Period:    period,
				Intervals: rel,
				Copies:    hi - lo,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Origin < out[j].Origin })
	return out, singles, nil
}
