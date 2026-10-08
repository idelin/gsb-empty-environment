package material

import (
	"errors"
	"fmt"
	"sort"
)

// Simulator applies material events to a lot inventory, enforcing
// conservation (or explicit loss) on every event.
type Simulator struct {
	lots      map[string]*Lot
	order     []string // lot IDs in creation order, for deterministic output
	applied   map[string]*Receipt
	log       []*Event
	totalLoss int64
}

func NewSimulator() *Simulator {
	return &Simulator{
		lots:    make(map[string]*Lot),
		applied: make(map[string]*Receipt),
	}
}

// Apply validates and applies an event. Re-applying an event whose ID was
// already applied is a no-op returning the original receipt (idempotent).
func (s *Simulator) Apply(e *Event) (*Receipt, error) {
	if e.ID == "" {
		return nil, errors.New("event ID must not be empty")
	}
	if r, ok := s.applied[e.ID]; ok {
		return r, nil
	}
	var r *Receipt
	var err error
	switch e.Type {
	case EvReceive:
		r, err = s.applyReceive(e)
	case EvSplit:
		r, err = s.applySplit(e)
	case EvMerge:
		r, err = s.applyMerge(e)
	case EvLoss:
		r, err = s.applyLoss(e)
	default:
		err = fmt.Errorf("unknown event type %d", int(e.Type))
	}
	if err != nil {
		return nil, fmt.Errorf("event %s (%s): %s", e.ID, e.Type, err)
	}
	s.applied[e.ID] = r
	s.log = append(s.log, e)
	s.totalLoss += r.LossQty
	return r, nil
}

func (s *Simulator) applyReceive(e *Event) (*Receipt, error) {
	if e.Qty <= 0 {
		return nil, fmt.Errorf("qty must be positive, got %d", e.Qty)
	}
	if e.Source == "" {
		return nil, errors.New("source must not be empty")
	}
	if _, ok := s.lots[e.LotID]; ok {
		return nil, fmt.Errorf("lot %s already exists", e.LotID)
	}
	lot := &Lot{ID: e.LotID, Qty: e.Qty, Vec: Pure(e.Source)}
	s.put(lot)
	return &Receipt{
		EventID:  e.ID,
		Type:     e.Type,
		LossVec:  Vector{},
		Produced: []*Lot{lot.Clone()},
	}, nil
}

func (s *Simulator) applySplit(e *Event) (*Receipt, error) {
	from, ok := s.lots[e.From]
	if !ok {
		return nil, fmt.Errorf("lot %s not found", e.From)
	}
	if len(e.Parts) == 0 {
		return nil, errors.New("split needs at least one part")
	}
	var sum int64
	seen := map[string]bool{}
	for _, p := range e.Parts {
		if p.Qty <= 0 {
			return nil, fmt.Errorf("part %s qty must be positive, got %d", p.LotID, p.Qty)
		}
		if seen[p.LotID] {
			return nil, fmt.Errorf("duplicate part lot %s", p.LotID)
		}
		if _, ok := s.lots[p.LotID]; ok {
			return nil, fmt.Errorf("lot %s already exists", p.LotID)
		}
		seen[p.LotID] = true
		sum += p.Qty
	}
	if sum > from.Qty {
		return nil, fmt.Errorf("parts sum %d exceeds lot qty %d; neither conserved nor loss declared", sum, from.Qty)
	}
	loss := from.Qty - sum // explicit loss, recorded in the receipt

	r := &Receipt{
		EventID:  e.ID,
		Type:     e.Type,
		LossQty:  loss,
		Consumed: []*Lot{from.Clone()},
	}
	if loss > 0 {
		r.LossVec = from.Vec.Clone() // proportional deduction
	} else {
		r.LossVec = Vector{}
	}
	delete(s.lots, from.ID)
	for _, p := range e.Parts {
		lot := &Lot{ID: p.LotID, Qty: p.Qty, Vec: from.Vec.Clone()}
		s.put(lot)
		r.Produced = append(r.Produced, lot.Clone())
	}
	return r, nil
}

func (s *Simulator) applyMerge(e *Event) (*Receipt, error) {
	if len(e.Ins) == 0 {
		return nil, errors.New("merge needs at least one input")
	}
	if _, ok := s.lots[e.Out]; ok {
		return nil, fmt.Errorf("lot %s already exists", e.Out)
	}
	qtys := make([]int64, 0, len(e.Ins))
	vecs := make([]Vector, 0, len(e.Ins))
	r := &Receipt{EventID: e.ID, Type: e.Type, LossVec: Vector{}}
	seen := map[string]bool{}
	for _, id := range e.Ins {
		lot, ok := s.lots[id]
		if !ok {
			return nil, fmt.Errorf("lot %s not found", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate input lot %s", id)
		}
		seen[id] = true
		qtys = append(qtys, lot.Qty)
		vecs = append(vecs, lot.Vec)
		r.Consumed = append(r.Consumed, lot.Clone())
	}
	var total int64
	for _, q := range qtys {
		total += q
	}
	// Merge conserves exactly: out qty == sum of input qtys, loss = 0.
	out := &Lot{ID: e.Out, Qty: total, Vec: Weighted(qtys, vecs)}
	for _, id := range e.Ins {
		delete(s.lots, id)
	}
	s.put(out)
	r.Produced = []*Lot{out.Clone()}
	return r, nil
}

func (s *Simulator) applyLoss(e *Event) (*Receipt, error) {
	lot, ok := s.lots[e.LotID]
	if !ok {
		return nil, fmt.Errorf("lot %s not found", e.LotID)
	}
	if e.LossQty <= 0 {
		return nil, fmt.Errorf("loss qty must be positive, got %d", e.LossQty)
	}
	if e.LossQty > lot.Qty {
		return nil, fmt.Errorf("loss %d exceeds lot qty %d", e.LossQty, lot.Qty)
	}
	r := &Receipt{
		EventID:  e.ID,
		Type:     e.Type,
		LossQty:  e.LossQty,
		LossVec:  lot.Vec.Clone(), // proportional rational deduction
		Consumed: []*Lot{lot.Clone()},
	}
	lot.Qty -= e.LossQty
	if lot.Qty == 0 {
		delete(s.lots, lot.ID)
	} else {
		r.Produced = []*Lot{lot.Clone()}
	}
	return r, nil
}

func (s *Simulator) put(l *Lot) {
	if _, ok := s.lots[l.ID]; !ok {
		s.order = append(s.order, l.ID)
	}
	s.lots[l.ID] = l
}

// Lot returns a snapshot of a live lot, or nil.
func (s *Simulator) Lot(id string) *Lot {
	if l, ok := s.lots[id]; ok {
		return l.Clone()
	}
	return nil
}

// Lots returns snapshots of all live lots in creation order.
func (s *Simulator) Lots() []*Lot {
	out := make([]*Lot, 0, len(s.lots))
	for _, id := range s.order {
		if l, ok := s.lots[id]; ok {
			out = append(out, l.Clone())
		}
	}
	return out
}

// TotalLoss returns the cumulative explicitly-recorded loss.
func (s *Simulator) TotalLoss() int64 { return s.totalLoss }

// Receipt returns the audit record of an applied event, or nil.
func (s *Simulator) Receipt(eventID string) *Receipt {
	return s.applied[eventID]
}

// Log returns the applied event log (defensive copy).
func (s *Simulator) Log() []*Event {
	out := make([]*Event, len(s.log))
	copy(out, s.log)
	return out
}

// Replay rebuilds a fresh simulator from the event log and verifies that
// the resulting state is identical to the current state. It proves the
// log fully determines the state (replay consistency).
func (s *Simulator) Replay() error {
	fresh := NewSimulator()
	for _, e := range s.log {
		if _, err := fresh.Apply(e); err != nil {
			return fmt.Errorf("replay failed at event %s: %s", e.ID, err)
		}
	}
	return s.checkEqual(fresh)
}

func (s *Simulator) checkEqual(o *Simulator) error {
	if s.totalLoss != o.totalLoss {
		return fmt.Errorf("replay mismatch: total loss %d != %d", s.totalLoss, o.totalLoss)
	}
	if len(s.lots) != len(o.lots) {
		return fmt.Errorf("replay mismatch: %d lots != %d lots", len(s.lots), len(o.lots))
	}
	ids := make([]string, 0, len(s.lots))
	for id := range s.lots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a, b := s.lots[id], o.lots[id]
		if b == nil {
			return fmt.Errorf("replay mismatch: lot %s missing after replay", id)
		}
		if !a.Equal(b) {
			return fmt.Errorf("replay mismatch: lot %s differs: %s vs %s", id, a, b)
		}
	}
	return nil
}
