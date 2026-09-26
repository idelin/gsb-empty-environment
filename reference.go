package tbsim

import (
	"math/big"
	"sort"
)

// RunRefShaper is the deliberately simple per-tick reference: it advances
// one tick at a time, refills every bucket, applies controls, admits
// arrivals, and emits at most one packet per tick. The event-hopping Shaper
// must reproduce its pass sequence exactly.
func RunRefShaper(flows []*Flow, arrivals []Arrival, controls []ControlOp, horizon int64) ([]EmitRecord, []DropRecord) {
	byName := map[string]*Flow{}
	buckets := map[string]*Bucket{}
	for _, f := range flows {
		byName[f.Name] = f
		for _, b := range f.Path() {
			buckets[b.Name] = b
		}
	}
	arr := append([]Arrival(nil), arrivals...)
	sort.SliceStable(arr, func(i, j int) bool { return arr[i].Time < arr[j].Time })
	ctl := append([]ControlOp(nil), controls...)
	sort.SliceStable(ctl, func(i, j int) bool { return ctl[i].Time < ctl[j].Time })

	var emitted []EmitRecord
	var dropped []DropRecord
	lastEmit := int64(-1)
	ai, ci := 0, 0
	for t := int64(0); t <= horizon; t++ {
		for _, b := range buckets {
			b.Advance(t)
		}
		for ci < len(ctl) && ctl[ci].Time == t {
			op := ctl[ci]
			switch op.Kind {
			case "setrate":
				buckets[op.Bucket].SetRate(t, big.NewRat(op.RateN, op.RateD))
			case "setweight":
				byName[op.Flow].Weight = op.Weight
			case "cancel":
				byName[op.Flow].cancel(op.PktID)
			}
			ci++
		}
		for ai < len(arr) && arr[ai].Time == t {
			p := arr[ai].Pkt
			f := byName[p.Flow]
			if reason := admit(f, p.Size); reason != "" {
				dropped = append(dropped, DropRecord{Pkt: p, Time: t, Reason: reason})
			} else {
				f.push(p, t)
			}
			ai++
		}
		if t <= lastEmit {
			continue // single link: at most one emit per tick
		}
		var cands []*Flow
		for _, f := range flows {
			if len(f.queue) == 0 {
				continue
			}
			ready := true
			for _, b := range f.Path() {
				if !b.Has(t, f.queue[0].pkt.Size) {
					ready = false
					break
				}
			}
			if ready {
				cands = append(cands, f)
			}
		}
		if len(cands) == 0 {
			continue
		}
		f := pick(cands)
		head := f.pop()
		for _, b := range f.Path() {
			b.Consume(t, head.pkt.Size)
		}
		emitted = append(emitted, EmitRecord{Pkt: head.pkt, Arrival: head.arrival, Emit: t})
		lastEmit = t
	}
	return emitted, dropped
}
