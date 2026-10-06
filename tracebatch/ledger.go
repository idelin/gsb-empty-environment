package tracebatch

import (
	"fmt"
	"math/big"
)

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{
		batches:  make(map[string]*Batch),
		children: make(map[string][]outEdge),
		initial:  make(map[string]*big.Rat),
		scrapped: make(map[string]*big.Rat),
	}
}

func addTo(m map[string]*big.Rat, key string, v *big.Rat) {
	if cur, ok := m[key]; ok {
		cur.Add(cur, v)
	} else {
		m[key] = new(big.Rat).Set(v)
	}
}

// Register creates a new origin batch with the given positive quantity.
func (l *Ledger) Register(id string, qty *big.Rat) error {
	if id == "" {
		return ErrEmptyID
	}
	if qty == nil || qty.Sign() <= 0 {
		return ErrNonPositiveQuantity
	}
	if _, ok := l.batches[id]; ok {
		return fmt.Errorf("%v: %s", ErrBatchExists, id)
	}
	q := new(big.Rat).Set(qty)
	l.batches[id] = &Batch{
		id:      id,
		qty:     new(big.Rat).Set(q),
		initQty: new(big.Rat).Set(q),
		comp:    map[string]*big.Rat{id: new(big.Rat).Set(q)},
		live:    true,
	}
	l.order = append(l.order, id)
	l.initial[id] = new(big.Rat).Set(q)
	return nil
}

// Mix creates a new batch from rational contributions of existing live
// batches. Each contribution must be positive and may not exceed the
// source's remaining quantity; zero contributions are rejected and never
// propagate anything.
func (l *Ledger) Mix(id string, inputs []Contribution) error {
	if err := l.validateMix(id, inputs); err != nil {
		return err
	}
	total := new(big.Rat)
	for _, in := range inputs {
		total.Add(total, in.Amount)
	}
	nb := &Batch{
		id:      id,
		qty:     new(big.Rat).Set(total),
		initQty: new(big.Rat).Set(total),
		comp:    make(map[string]*big.Rat),
		live:    true,
	}
	for _, in := range inputs {
		amt := new(big.Rat).Set(in.Amount)
		moved := l.draw(in.Source, in.Amount)
		for origin, m := range moved {
			addTo(nb.comp, origin, m)
		}
		nb.inputs = append(nb.inputs, inEdge{from: in.Source, amt: amt})
		l.children[in.Source] = append(l.children[in.Source], outEdge{to: id, amt: amt})
	}
	l.batches[id] = nb
	l.order = append(l.order, id)
	return nil
}

func (l *Ledger) validateMix(id string, inputs []Contribution) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := l.batches[id]; ok {
		return fmt.Errorf("%v: %s", ErrBatchExists, id)
	}
	if len(inputs) == 0 {
		return ErrNoInputs
	}
	seen := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		if in.Source == id {
			return fmt.Errorf("%v: %s contributes to itself", ErrCycle, id)
		}
		src, ok := l.batches[in.Source]
		if !ok {
			return fmt.Errorf("%v: %s", ErrUnknownBatch, in.Source)
		}
		if !src.live {
			return fmt.Errorf("%v: %s", ErrBatchScrapped, in.Source)
		}
		if in.Amount == nil || in.Amount.Sign() <= 0 {
			return ErrNonPositiveQuantity
		}
		if in.Amount.Cmp(src.qty) > 0 {
			return fmt.Errorf("%v: %s", ErrInsufficientQty, in.Source)
		}
		if seen[in.Source] {
			return fmt.Errorf("%v: %s", ErrDuplicateSource, in.Source)
		}
		seen[in.Source] = true
		if l.reaches(id, in.Source) {
			return fmt.Errorf("%v: %s -> %s", ErrCycle, in.Source, id)
		}
	}
	return nil
}

// Split repackages one live batch into several new batches. The parts' total
// may not exceed the source's remaining quantity; any remainder stays in the
// source batch.
func (l *Ledger) Split(source string, parts []SplitPart) error {
	src, ok := l.batches[source]
	if !ok {
		return fmt.Errorf("%v: %s", ErrUnknownBatch, source)
	}
	if !src.live {
		return fmt.Errorf("%v: %s", ErrBatchScrapped, source)
	}
	if len(parts) == 0 {
		return ErrNoInputs
	}
	total := new(big.Rat)
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		if p.ID == "" {
			return ErrEmptyID
		}
		if p.ID == source {
			return fmt.Errorf("%v: %s splits into itself", ErrCycle, source)
		}
		if _, ok := l.batches[p.ID]; ok {
			return fmt.Errorf("%v: %s", ErrBatchExists, p.ID)
		}
		if p.Amount == nil || p.Amount.Sign() <= 0 {
			return ErrNonPositiveQuantity
		}
		if seen[p.ID] {
			return fmt.Errorf("%v: %s", ErrDuplicateSource, p.ID)
		}
		seen[p.ID] = true
		if l.reaches(p.ID, source) {
			return fmt.Errorf("%v: %s -> %s", ErrCycle, source, p.ID)
		}
		total.Add(total, p.Amount)
	}
	if total.Cmp(src.qty) > 0 {
		return fmt.Errorf("%v: %s", ErrInsufficientQty, source)
	}
	for _, p := range parts {
		amt := new(big.Rat).Set(p.Amount)
		moved := l.draw(source, p.Amount)
		nb := &Batch{
			id:      p.ID,
			qty:     new(big.Rat).Set(amt),
			initQty: new(big.Rat).Set(amt),
			comp:    moved,
			live:    true,
			inputs:  []inEdge{{from: source, amt: amt}},
		}
		l.children[source] = append(l.children[source], outEdge{to: p.ID, amt: amt})
		l.batches[p.ID] = nb
		l.order = append(l.order, p.ID)
	}
	return nil
}

// Scrap disposes of a live batch's remaining quantity. The scrapped mass is
// retained in the ledger's per-origin scrap account so mass conservation
// remains checkable. A scrapped batch is terminal: it can no longer be
// mixed, split, or scrapped again.
func (l *Ledger) Scrap(id string) error {
	b, ok := l.batches[id]
	if !ok {
		return fmt.Errorf("%v: %s", ErrUnknownBatch, id)
	}
	if !b.live {
		return fmt.Errorf("%v: %s", ErrBatchScrapped, id)
	}
	for origin, m := range b.comp {
		addTo(l.scrapped, origin, m)
	}
	b.qty = new(big.Rat)
	b.comp = make(map[string]*big.Rat)
	b.live = false
	return nil
}

// draw removes amt from the source batch and returns the symbolic source
// vector of the removed mass. Draws are proportional, so the origin
// fractions of the source batch are unchanged by a draw.
func (l *Ledger) draw(source string, amt *big.Rat) map[string]*big.Rat {
	src := l.batches[source]
	frac := new(big.Rat).Quo(amt, src.qty)
	moved := make(map[string]*big.Rat, len(src.comp))
	for origin, m := range src.comp {
		mm := new(big.Rat).Mul(m, frac)
		src.comp[origin] = new(big.Rat).Sub(m, mm)
		if mm.Sign() > 0 {
			moved[origin] = mm
		}
	}
	src.qty = new(big.Rat).Sub(src.qty, amt)
	return moved
}

// reaches reports whether batch `to` is reachable from batch `from`
// following child edges. Used as a cycle guard before adding edges.
func (l *Ledger) reaches(from, to string) bool {
	stack := []string{from}
	visited := map[string]bool{from: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		for _, e := range l.children[cur] {
			if !visited[e.to] {
				visited[e.to] = true
				stack = append(stack, e.to)
			}
		}
	}
	return false
}

// ValidateDAG verifies the batch graph is acyclic using Kahn's algorithm.
// It returns ErrCycle if a cycle is found.
func (l *Ledger) ValidateDAG() error {
	indeg := make(map[string]int, len(l.batches))
	for id := range l.batches {
		indeg[id] = 0
	}
	for _, es := range l.children {
		for _, e := range es {
			indeg[e.to]++
		}
	}
	var queue []string
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, e := range l.children[cur] {
			indeg[e.to]--
			if indeg[e.to] == 0 {
				queue = append(queue, e.to)
			}
		}
	}
	if visited != len(l.batches) {
		return ErrCycle
	}
	return nil
}

// Quantity returns the remaining quantity of a batch.
func (l *Ledger) Quantity(id string) (*big.Rat, error) {
	b, ok := l.batches[id]
	if !ok {
		return nil, fmt.Errorf("%v: %s", ErrUnknownBatch, id)
	}
	return new(big.Rat).Set(b.qty), nil
}

// Composition returns the symbolic source vector of a batch: the exact
// rational mass each origin batch contributes to its remaining quantity.
func (l *Ledger) Composition(id string) (map[string]*big.Rat, error) {
	b, ok := l.batches[id]
	if !ok {
		return nil, fmt.Errorf("%v: %s", ErrUnknownBatch, id)
	}
	out := make(map[string]*big.Rat, len(b.comp))
	for origin, m := range b.comp {
		out[origin] = new(big.Rat).Set(m)
	}
	return out, nil
}
