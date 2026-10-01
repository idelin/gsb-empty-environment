package pktscale

import (
	"fmt"
	"math/big"
	"sort"
)

// RateSegment declares that from input tick At (inclusive) the time rate
// is Rate. The first segment must have At <= the earliest record time.
type RateSegment struct {
	At   int64
	Rate *big.Rat
}

// ExactTimeline is the independent reference implementation of the
// input->output time mapping, recomputed from scratch in exact rational
// arithmetic. It shares no state with Scaler.
func ExactTimeline(segs []RateSegment, t int64) *big.Rat {
	if len(segs) == 0 {
		return big.NewRat(t, 1)
	}
	pos := new(big.Rat)
	base := segs[0].At
	for i, seg := range segs {
		next := t
		if i+1 < len(segs) && segs[i+1].At < next {
			next = segs[i+1].At
		}
		if t < seg.At {
			break
		}
		if next < seg.At {
			next = seg.At
		}
		if next > base {
			d := big.NewRat(next-base, 1)
			pos.Add(pos, d.Mul(d, seg.Rate))
		}
		base = next
		if t <= next {
			break
		}
	}
	return pos
}

// CheckDrift verifies, independently of the scaler, that every packet's
// first fragment lies within bound ticks of the exact rational position
// and that per-stream ticks never go backwards. It returns a list of
// violations (empty means the long-sequence drift bound holds).
func CheckDrift(segs []RateSegment, recs []Record, batches [][]Fragment, bound int64) []string {
	var bad []string
	last := map[int]int64{}
	seen := map[int]bool{}
	for i, rec := range recs {
		if len(batches[i]) == 0 {
			bad = append(bad, fmt.Sprintf("record %d produced no fragment", rec.Seq))
			continue
		}
		emit := batches[i][0].Tick
		exact := ExactTimeline(segs, rec.Time)
		diff := new(big.Rat).Sub(big.NewRat(emit, 1), exact)
		abs := new(big.Rat).Abs(diff)
		if abs.Cmp(big.NewRat(bound, 1)) > 0 {
			bad = append(bad, fmt.Sprintf("record %d (stream %d): emit %d deviates %s from exact %s (bound %d)",
				rec.Seq, rec.Stream, emit, diff.RatString(), exact.RatString(), bound))
		}
		if seen[rec.Stream] && emit < last[rec.Stream] {
			bad = append(bad, fmt.Sprintf("record %d (stream %d): tick %d goes backwards (last %d)",
				rec.Seq, rec.Stream, emit, last[rec.Stream]))
		}
		seen[rec.Stream] = true
		last[rec.Stream] = emit
	}
	return bad
}

// CheckSpacing audits per-stream packet spacing against the declared
// minimum gap and the link's serialization time. A packet whose
// transmission time exceeds the gap to its successor makes the declared
// spacing unsatisfiable on that link; both cases are reported.
func CheckSpacing(batches [][]Fragment, minGap, bandwidth int64) []string {
	type pkt struct {
		stream   int
		tick     int64
		lastTick int64
		size     int64
		seq      int64
	}
	var pkts []pkt
	for _, frags := range batches {
		if len(frags) == 0 {
			continue
		}
		var size int64
		for _, f := range frags {
			size += f.Size
		}
		pkts = append(pkts, pkt{
			stream:   frags[0].Stream,
			tick:     frags[0].Tick,
			lastTick: frags[len(frags)-1].Tick,
			size:     size,
			seq:      frags[0].Seq,
		})
	}
	sort.SliceStable(pkts, func(i, j int) bool {
		if pkts[i].stream != pkts[j].stream {
			return pkts[i].stream < pkts[j].stream
		}
		return pkts[i].seq < pkts[j].seq
	})
	var bad []string
	for i := 0; i+1 < len(pkts); i++ {
		a, b := pkts[i], pkts[i+1]
		if a.stream != b.stream {
			continue
		}
		gap := b.tick - a.tick
		tx := (a.size + bandwidth - 1) / bandwidth
		if gap < minGap {
			bad = append(bad, fmt.Sprintf("stream %d seq %d->%d: gap %d ticks < declared min gap %d",
				a.stream, a.seq, b.seq, gap, minGap))
		}
		if tx > gap {
			bad = append(bad, fmt.Sprintf("stream %d seq %d->%d: packet needs %d ticks on link but gap is %d (spacing unsatisfiable)",
				a.stream, a.seq, b.seq, tx, gap))
		}
	}
	return bad
}
