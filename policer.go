package tbsim

import "sort"

// Policer is a dropping-style traffic policer: it never delays. At each
// arrival every ancestor bucket is checked atomically — all must have enough
// tokens, otherwise the packet is dropped and no bucket is charged.
type Policer struct {
	flows   map[string]*Flow
	buckets map[string]*Bucket

	Passed  []PassRecord
	Dropped []DropRecord
}

func NewPolicer(flows []*Flow) *Policer {
	p := &Policer{flows: map[string]*Flow{}, buckets: map[string]*Bucket{}}
	for _, f := range flows {
		p.flows[f.Name] = f
		for _, b := range f.Path() {
			p.buckets[b.Name] = b
		}
	}
	return p
}

// Bucket exposes a bucket by name (for audits).
func (p *Policer) Bucket(name string) *Bucket { return p.buckets[name] }

// decide assumes all buckets are already advanced to now.
func (p *Policer) decide(now int64, pkt Packet) {
	f := p.flows[pkt.Flow]
	path := f.Path()
	for _, b := range path {
		if b.Cap < pkt.Size {
			p.Dropped = append(p.Dropped, DropRecord{Pkt: pkt, Time: now, Reason: "oversize"})
			return
		}
	}
	for _, b := range path {
		if !b.Has(now, pkt.Size) {
			p.Dropped = append(p.Dropped, DropRecord{Pkt: pkt, Time: now, Reason: "tokens"})
			return
		}
	}
	for _, b := range path {
		b.Consume(now, pkt.Size)
	}
	p.Passed = append(p.Passed, PassRecord{Pkt: pkt, Time: now})
}

// Arrive processes one arrival in the event (lazy-refill) style.
func (p *Policer) Arrive(now int64, pkt Packet) {
	for _, b := range p.flows[pkt.Flow].Path() {
		b.Advance(now)
	}
	p.decide(now, pkt)
}

// RunRefPolicer is the per-tick reference policer: every bucket is refilled
// tick by tick before that tick's arrivals are decided.
func RunRefPolicer(flows []*Flow, arrivals []Arrival, horizon int64) ([]PassRecord, []DropRecord) {
	p := NewPolicer(flows)
	arr := append([]Arrival(nil), arrivals...)
	sort.SliceStable(arr, func(i, j int) bool { return arr[i].Time < arr[j].Time })
	ai := 0
	for t := int64(0); t <= horizon; t++ {
		for _, b := range p.buckets {
			b.Advance(t)
		}
		for ai < len(arr) && arr[ai].Time == t {
			p.decide(t, arr[ai].Pkt)
			ai++
		}
	}
	return p.Passed, p.Dropped
}
