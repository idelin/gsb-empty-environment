// Package trace tracks batch genealogy for contamination traceability.
//
// Every batch carries a symbolic source vector: a map from source batch ID
// to the exact rational fraction of the batch's mass originating from that
// source. Mixing, splitting and discarding move mass along a directed
// acyclic graph; contamination marks propagate only along flows with a
// non-zero contribution, so a zero-amount input never taints its output.
package trace

import (
	"errors"
	"fmt"
	"math/big"
)

// Portion is an amount of mass drawn from one input batch into a mix.
type Portion struct {
	Batch  string
	Amount *big.Rat
}

// event is one mass movement: a flow from one batch into another, or a
// discard (to == ""). Events are recorded in chronological order.
type event struct {
	from, to   string
	amount     *big.Rat
	massBefore *big.Rat
}

// Batch is a node in the genealogy graph.
type Batch struct {
	id    string
	mass  *big.Rat
	vec   map[string]*big.Rat
	drawn *big.Rat
}

// ID returns the batch identifier.
func (b *Batch) ID() string { return b.id }

// Mass returns the remaining mass of the batch.
func (b *Batch) Mass() *big.Rat { return new(big.Rat).Set(b.mass) }

// Fraction returns the exact fraction of the batch's mass that originates
// from the given source, or zero.
func (b *Batch) Fraction(source string) *big.Rat {
	if f, ok := b.vec[source]; ok {
		return new(big.Rat).Set(f)
	}
	return new(big.Rat)
}

// Ledger records all batches and the flows between them.
type Ledger struct {
	batches   map[string]*Batch
	order     []string
	events    []event
	sources   map[string]*big.Rat
	discarded map[string]*big.Rat
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{
		batches:   map[string]*Batch{},
		sources:   map[string]*big.Rat{},
		discarded: map[string]*big.Rat{},
	}
}

// Batch returns the batch with the given id, or nil.
func (l *Ledger) Batch(id string) *Batch { return l.batches[id] }

// AddSource registers a pristine source batch whose entire mass is
// attributed to itself.
func (l *Ledger) AddSource(id string, mass *big.Rat) error {
	if id == "" {
		return errors.New("trace: empty batch id")
	}
	if mass == nil || mass.Sign() <= 0 {
		return fmt.Errorf("trace: source %q needs positive mass", id)
	}
	if _, ok := l.batches[id]; ok {
		return fmt.Errorf("trace: batch %q already exists", id)
	}
	l.batches[id] = &Batch{
		id:    id,
		mass:  new(big.Rat).Set(mass),
		vec:   map[string]*big.Rat{id: big.NewRat(1, 1)},
		drawn: new(big.Rat),
	}
	l.order = append(l.order, id)
	l.sources[id] = new(big.Rat).Set(mass)
	return nil
}

// Mix draws the given portions from existing batches and combines them into
// a new batch. Zero-amount portions are ignored: they contribute no mass,
// create no flow and propagate no contamination.
func (l *Ledger) Mix(out string, portions ...Portion) error {
	if err := l.checkFresh(out); err != nil {
		return err
	}
	var draws []Portion
	total := new(big.Rat)
	for _, p := range portions {
		if p.Amount == nil || p.Amount.Sign() < 0 {
			return fmt.Errorf("trace: invalid amount for batch %q", p.Batch)
		}
		if p.Amount.Sign() == 0 {
			continue
		}
		b, ok := l.batches[p.Batch]
		if !ok {
			return fmt.Errorf("trace: unknown batch %q", p.Batch)
		}
		if b.mass.Cmp(p.Amount) < 0 {
			return fmt.Errorf("trace: draw %s exceeds remaining mass %s of batch %q",
				p.Amount.RatString(), b.mass.RatString(), p.Batch)
		}
		if l.reachable(out, p.Batch) {
			return fmt.Errorf("trace: mixing %q into %q would create a cycle", p.Batch, out)
		}
		draws = append(draws, Portion{p.Batch, new(big.Rat).Set(p.Amount)})
		total.Add(total, p.Amount)
	}
	if total.Sign() == 0 {
		return errors.New("trace: mix needs at least one positive portion")
	}
	vec := map[string]*big.Rat{}
	for _, d := range draws {
		b := l.batches[d.Batch]
		l.consume(b, d.Amount, out)
		for s, f := range b.vec {
			w := new(big.Rat).Mul(d.Amount, f)
			w.Quo(w, total)
			if cur, ok := vec[s]; ok {
				cur.Add(cur, w)
			} else {
				vec[s] = w
			}
		}
	}
	l.batches[out] = &Batch{id: out, mass: total, vec: vec, drawn: new(big.Rat)}
	l.order = append(l.order, out)
	return nil
}

// Split moves amount of mass from batch from into a new batch out. The
// child inherits the exact source vector of its parent.
func (l *Ledger) Split(out, from string, amount *big.Rat) error {
	if err := l.checkFresh(out); err != nil {
		return err
	}
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("trace: split of %q needs positive amount", from)
	}
	b, ok := l.batches[from]
	if !ok {
		return fmt.Errorf("trace: unknown batch %q", from)
	}
	if b.mass.Cmp(amount) < 0 {
		return fmt.Errorf("trace: split %s exceeds remaining mass %s of batch %q",
			amount.RatString(), b.mass.RatString(), from)
	}
	if l.reachable(out, from) {
		return fmt.Errorf("trace: splitting %q into %q would create a cycle", from, out)
	}
	l.consume(b, amount, out)
	vec := map[string]*big.Rat{}
	for s, f := range b.vec {
		vec[s] = new(big.Rat).Set(f)
	}
	l.batches[out] = &Batch{
		id:    out,
		mass:  new(big.Rat).Set(amount),
		vec:   vec,
		drawn: new(big.Rat),
	}
	l.order = append(l.order, out)
	return nil
}

// Discard scraps amount of mass from a batch. The discarded mass keeps its
// source attribution so mass conservation can be verified exactly.
func (l *Ledger) Discard(id string, amount *big.Rat) error {
	b, ok := l.batches[id]
	if !ok {
		return fmt.Errorf("trace: unknown batch %q", id)
	}
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("trace: discard of %q needs positive amount", id)
	}
	if b.mass.Cmp(amount) < 0 {
		return fmt.Errorf("trace: discard %s exceeds remaining mass %s of batch %q",
			amount.RatString(), b.mass.RatString(), id)
	}
	l.events = append(l.events, event{
		from:       id,
		amount:     new(big.Rat).Set(amount),
		massBefore: new(big.Rat).Set(b.mass),
	})
	b.mass.Sub(b.mass, amount)
	b.drawn.Add(b.drawn, amount)
	for s, f := range b.vec {
		w := new(big.Rat).Mul(amount, f)
		if cur, ok := l.discarded[s]; ok {
			cur.Add(cur, w)
		} else {
			l.discarded[s] = w
		}
	}
	return nil
}

// AffectedBy returns, for every downstream batch that carries a non-zero
// contribution of the given source, the exact fraction of its mass
// attributable to that source. Zero contributions are omitted, and shared
// paths are counted exactly once because contributions are aggregated per
// flow, not per path.
func (l *Ledger) AffectedBy(source string) map[string]*big.Rat {
	res := map[string]*big.Rat{}
	for _, id := range l.order {
		if f := l.batches[id].Fraction(source); f.Sign() > 0 {
			res[id] = f
		}
	}
	return res
}

// MassFrom returns the mass of the given source present in a batch.
func (l *Ledger) MassFrom(source, batch string) *big.Rat {
	b, ok := l.batches[batch]
	if !ok {
		return new(big.Rat)
	}
	return new(big.Rat).Mul(b.mass, b.Fraction(source))
}

// DiscardedMass returns the total discarded mass attributable to a source.
func (l *Ledger) DiscardedMass(source string) *big.Rat {
	if d, ok := l.discarded[source]; ok {
		return new(big.Rat).Set(d)
	}
	return new(big.Rat)
}

// VerifyConservation checks that every unit of the source's original mass
// is accounted for by live batches plus discarded mass.
func (l *Ledger) VerifyConservation(source string) error {
	initial, ok := l.sources[source]
	if !ok {
		return fmt.Errorf("trace: unknown source %q", source)
	}
	total := l.DiscardedMass(source)
	for _, id := range l.order {
		total.Add(total, l.MassFrom(source, id))
	}
	if total.Cmp(initial) != 0 {
		return fmt.Errorf("trace: source %q not conserved: initial %s, accounted %s",
			source, initial.RatString(), total.RatString())
	}
	return nil
}

// VerifyAgainstPaths cross-checks the symbolic source-vector model against
// explicit path enumeration over the flow graph. For every batch it sums,
// over all distinct paths from the source, each path's exact contribution
// (draw ratios times survival factors for mass that left via sibling
// flows), and requires the total to equal the vector model's attribution
// for all mass that ever entered the batch. This catches duplicated shared
// paths and any mass leaked or invented by the vector aggregation.
func (l *Ledger) VerifyAgainstPaths(source string) error {
	initial, ok := l.sources[source]
	if !ok {
		return fmt.Errorf("trace: unknown source %q", source)
	}
	for _, id := range l.order {
		b := l.batches[id]
		got := l.pathMass(source, id, initial)
		ever := new(big.Rat).Add(b.mass, b.drawn)
		want := new(big.Rat).Mul(ever, b.Fraction(source))
		if got.Cmp(want) != 0 {
			return fmt.Errorf("trace: path/vector mismatch at batch %q for source %q: paths %s, vector %s",
				id, source, got.RatString(), want.RatString())
		}
	}
	return nil
}

// pathMass enumerates every directed path from source to target and sums
// initial*prod(drawRatio)*prod(survivalFactor) per path. The survival
// factor of an event is 1-amount/massBefore: the share of mass that stays
// behind when a sibling flow or discard draws from a path node.
func (l *Ledger) pathMass(source, target string, initial *big.Rat) *big.Rat {
	one := big.NewRat(1, 1)
	sum := new(big.Rat)
	var walk func(node string, last int, acc *big.Rat)
	walk = func(node string, last int, acc *big.Rat) {
		if node == target {
			sum.Add(sum, acc)
			return
		}
		for i := last + 1; i < len(l.events); i++ {
			ev := l.events[i]
			if ev.from != node || ev.to == "" {
				continue
			}
			surv := big.NewRat(1, 1)
			for j := last + 1; j < i; j++ {
				sib := l.events[j]
				if sib.from != node {
					continue
				}
				f := new(big.Rat).Quo(sib.amount, sib.massBefore)
				surv.Mul(surv, f.Sub(one, f))
			}
			next := new(big.Rat).Mul(acc, surv)
			next.Mul(next, new(big.Rat).Quo(ev.amount, ev.massBefore))
			walk(ev.to, i, next)
		}
	}
	walk(source, -1, new(big.Rat).Set(initial))
	return sum
}

func (l *Ledger) checkFresh(out string) error {
	if out == "" {
		return errors.New("trace: empty batch id")
	}
	if _, ok := l.batches[out]; ok {
		return fmt.Errorf("trace: batch %q already exists; reusing it would create a cycle", out)
	}
	return nil
}

func (l *Ledger) consume(b *Batch, amount *big.Rat, to string) {
	l.events = append(l.events, event{
		from:       b.id,
		to:         to,
		amount:     new(big.Rat).Set(amount),
		massBefore: new(big.Rat).Set(b.mass),
	})
	b.mass.Sub(b.mass, amount)
	b.drawn.Add(b.drawn, amount)
}

// reachable reports whether target is reachable from start along flows.
func (l *Ledger) reachable(start, target string) bool {
	adj := map[string][]string{}
	for _, ev := range l.events {
		if ev.to != "" {
			adj[ev.from] = append(adj[ev.from], ev.to)
		}
	}
	seen := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == target {
			return true
		}
		for _, m := range adj[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	return false
}
