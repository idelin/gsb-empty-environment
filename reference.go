package tsxform

import (
	"fmt"
	"math/big"
	"sort"
)

// RateSeg declares one time-rate segment for the reference timeline.
// Segments must start at tick 0 and are continuous by construction.
type RateSeg struct {
	At   int64
	Rate Ratio
}

// RefSpec is the independent input to the reference checker: the original
// trace plus the declared transform parameters.
type RefSpec struct {
	Packets      []Packet
	TimeSegs     []RateSeg
	ByteRate     Ratio
	MTU          int64
	BytesPerTick int64
	MinSpacing   int64 // required minimum ideal spacing per stream; 0 disables
}

// Report summarizes a reference check. Violations are hard errors;
// Unsatisfiable lists packet pairs whose declared spacing cannot be met
// under the given rate without breaking the drift bound.
type Report struct {
	Violations    []string
	Unsatisfiable []string
	MaxDrift      *big.Rat
}

// OK reports whether no violations were found.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

func (r *Report) failf(format string, args ...interface{}) {
	r.Violations = append(r.Violations, fmt.Sprintf(format, args...))
}

func ratOf(r Ratio) *big.Rat { return new(big.Rat).SetFrac64(r.Num, r.Den) }

// exactPositions recomputes, from scratch and with exact rational
// arithmetic, the scaled position of every input packet. It shares no state
// with the scaler, so it is an independent reference timeline.
func exactPositions(packets []Packet, segs []RateSeg) ([]*big.Rat, error) {
	if len(segs) == 0 || segs[0].At != 0 {
		return nil, fmt.Errorf("reference: first time segment must start at tick 0")
	}
	sorted := append([]RateSeg(nil), segs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	dedup := sorted[:0]
	for _, sg := range sorted {
		if len(dedup) > 0 && dedup[len(dedup)-1].At == sg.At {
			dedup[len(dedup)-1] = sg
		} else {
			dedup = append(dedup, sg)
		}
	}
	anchors := make([]*big.Rat, len(dedup))
	anchors[0] = new(big.Rat)
	for i := 1; i < len(dedup); i++ {
		d := new(big.Rat).SetInt64(dedup[i].At - dedup[i-1].At)
		d.Mul(d, ratOf(dedup[i-1].Rate))
		anchors[i] = d.Add(d, anchors[i-1])
	}
	pos := make([]*big.Rat, len(packets))
	for i, p := range packets {
		seg := 0
		for j, sg := range dedup {
			if sg.At <= p.Time {
				seg = j
			} else {
				break
			}
		}
		d := new(big.Rat).SetInt64(p.Time - dedup[seg].At)
		d.Mul(d, ratOf(dedup[seg].Rate))
		pos[i] = d.Add(d, anchors[seg])
	}
	return pos, nil
}

// Check verifies an output record stream against the independent reference
// timeline: per-packet drift below one tick, per-stream causality, MTU
// fragment structure, link serialization, declared-order tie-breaking on
// equal ideal ticks, and feasibility of the declared minimum spacing.
func Check(spec RefSpec, out []Record) *Report {
	rep := &Report{MaxDrift: new(big.Rat)}
	exact, err := exactPositions(spec.Packets, spec.TimeSegs)
	if err != nil {
		rep.failf("%v", err)
		return rep
	}
	byPkt := map[int64][]Record{}
	for _, rec := range out {
		byPkt[rec.PacketID] = append(byPkt[rec.PacketID], rec)
	}
	cumBytes := map[int]int64{}
	emitted := map[int]int64{}
	lastTick := map[int]int64{}
	haveTick := map[int]bool{}
	lastIn := map[int]int64{}
	seen := map[int]bool{}
	one := big.NewInt(1)

	for i, p := range spec.Packets {
		id := int64(i)
		if seen[p.Stream] && p.Time < lastIn[p.Stream] {
			rep.failf("packet %d: stream %d input time went backwards", i, p.Stream)
		}
		lastIn[p.Stream] = p.Time
		seen[p.Stream] = true

		recs := byPkt[id]
		if len(recs) == 0 {
			rep.failf("packet %d (stream %d) missing from output", i, p.Stream)
			continue
		}
		sort.Slice(recs, func(a, b int) bool { return recs[a].FragIndex < recs[b].FragIndex })

		wantTick := floorRat(exact[i])
		if haveTick[p.Stream] && wantTick < lastTick[p.Stream] {
			rep.failf("packet %d: stream %d ideal tick %d goes backwards (last %d)", i, p.Stream, wantTick, lastTick[p.Stream])
		}
		drift := new(big.Rat).Sub(exact[i], new(big.Rat).SetInt64(wantTick))
		if drift.Sign() < 0 || drift.Cmp(new(big.Rat).SetInt(one)) >= 0 {
			rep.failf("packet %d: drift %s outside [0,1) tick", i, drift.RatString())
		}
		if drift.Cmp(rep.MaxDrift) > 0 {
			rep.MaxDrift.Set(drift)
		}

		cumBytes[p.Stream] += p.Size
		wantCum := scaleBytes(cumBytes[p.Stream], spec.ByteRate)
		var total int64
		for j, rec := range recs {
			if rec.IdealTick != wantTick {
				rep.failf("packet %d frag %d: ideal tick %d, reference floor %d", i, j, rec.IdealTick, wantTick)
			}
			if rec.FragIndex != j || rec.FragCount != len(recs) {
				rep.failf("packet %d frag %d: bad fragment indexing %d/%d", i, j, rec.FragIndex, rec.FragCount)
			}
			if rec.Size <= 0 || rec.Size > spec.MTU {
				rep.failf("packet %d frag %d: size %d violates MTU %d", i, j, rec.Size, spec.MTU)
			}
			if rec.Stream != p.Stream {
				rep.failf("packet %d frag %d: stream mismatch %d != %d", i, j, rec.Stream, p.Stream)
			}
			total += rec.Size
		}
		if want := int((total + spec.MTU - 1) / spec.MTU); len(recs) != want {
			rep.failf("packet %d: %d fragments, want %d for size %d", i, len(recs), want, total)
		}
		prevEmitted := emitted[p.Stream]
		emitted[p.Stream] += total
		if emitted[p.Stream] < wantCum {
			rep.failf("packet %d: emitted bytes %d fell behind exact floor %d", i, emitted[p.Stream], wantCum)
		}
		if emitted[p.Stream] > wantCum && wantCum > prevEmitted {
			rep.failf("packet %d: emitted bytes %d ran ahead of exact floor %d without a clamp", i, emitted[p.Stream], wantCum)
		}

		if spec.MinSpacing > 0 && haveTick[p.Stream] {
			if gap := wantTick - lastTick[p.Stream]; gap < spec.MinSpacing {
				rep.Unsatisfiable = append(rep.Unsatisfiable,
					fmt.Sprintf("stream %d packets ending at input tick %d: ideal spacing %d < required %d", p.Stream, p.Time, gap, spec.MinSpacing))
			}
		}
		lastTick[p.Stream] = wantTick
		haveTick[p.Stream] = true
	}

	for i, rec := range out {
		if rec.Depart < rec.IdealTick {
			rep.failf("record %d: depart %d before ideal tick %d", i, rec.Depart, rec.IdealTick)
		}
		wantDur := (rec.Size + spec.BytesPerTick - 1) / spec.BytesPerTick
		if rec.Done-rec.Depart != wantDur {
			rep.failf("record %d: transmission took %d ticks, want %d", i, rec.Done-rec.Depart, wantDur)
		}
		if i > 0 && rec.Depart < out[i-1].Done {
			rep.failf("record %d: departs at %d while previous transmission ends at %d", i, rec.Depart, out[i-1].Done)
		}
	}

	byTick := map[int64][]Record{}
	var tickOrder []int64
	for _, rec := range out {
		if _, ok := byTick[rec.IdealTick]; !ok {
			tickOrder = append(tickOrder, rec.IdealTick)
		}
		byTick[rec.IdealTick] = append(byTick[rec.IdealTick], rec)
	}
	for _, tick := range tickOrder {
		recs := byTick[tick]
		for j := 1; j < len(recs); j++ {
			a, b := recs[j-1], recs[j]
			if a.Seq > b.Seq || a.Seq == b.Seq && a.FragIndex > b.FragIndex {
				rep.failf("ideal tick %d: declared order violated (seq %d frag %d before seq %d frag %d)",
					tick, a.Seq, a.FragIndex, b.Seq, b.FragIndex)
			}
		}
	}

	lastPkt := map[int]int64{}
	streamSeen := map[int]bool{}
	for _, rec := range out {
		if streamSeen[rec.Stream] && rec.PacketID < lastPkt[rec.Stream] {
			rep.failf("stream %d: packet %d departs after packet %d", rec.Stream, rec.PacketID, lastPkt[rec.Stream])
		}
		streamSeen[rec.Stream] = true
		lastPkt[rec.Stream] = rec.PacketID
	}
	return rep
}
