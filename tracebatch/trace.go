package tracebatch

import (
	"fmt"
	"math/big"
)

// markedFractions computes, for every batch, the fraction of its mass
// attributable to source. Batches are processed in creation order, which is
// a topological order of the DAG, so each batch's fraction is folded exactly
// once from its direct inputs: shared paths are deduplicated by construction
// instead of being enumerated per path. Inputs whose source fraction is zero
// contribute nothing and do not propagate the mark.
func (l *Ledger) markedFractions(source string) (map[string]*big.Rat, error) {
	if _, ok := l.batches[source]; !ok {
		return nil, fmt.Errorf("%v: %s", ErrUnknownBatch, source)
	}
	frac := make(map[string]*big.Rat, len(l.order))
	for _, id := range l.order {
		b := l.batches[id]
		if id == source {
			frac[id] = big.NewRat(1, 1)
			continue
		}
		acc := new(big.Rat)
		for _, in := range b.inputs {
			c := frac[in.from]
			if c == nil || c.Sign() == 0 {
				continue
			}
			t := new(big.Rat).Mul(c, in.amt)
			acc.Add(acc, t)
		}
		if acc.Sign() > 0 {
			frac[id] = new(big.Rat).Quo(acc, b.initQty)
		}
	}
	return frac, nil
}

// AffectedProportions returns, for the given contamination source, every
// downstream batch with a non-zero affected proportion, in creation order.
// Scrapped batches are included (flagged) since their proportion at scrap
// time is well defined. The source itself is not listed.
func (l *Ledger) AffectedProportions(source string) ([]Affected, error) {
	frac, err := l.markedFractions(source)
	if err != nil {
		return nil, err
	}
	var out []Affected
	for _, id := range l.order {
		if id == source {
			continue
		}
		f := frac[id]
		if f == nil || f.Sign() == 0 {
			continue
		}
		out = append(out, Affected{
			BatchID:    id,
			Proportion: f,
			Scrapped:   !l.batches[id].live,
		})
	}
	return out, nil
}

// VerifyTrace cross-checks the deduplicated marked fractions for source
// against an independent symbolic model: an explicit enumeration of every
// directed source-to-batch path, where a path's weight is the product of
// amt/created-quantity ratios along its edges. The two models must agree,
// which proves shared path prefixes are neither dropped nor double counted.
func (l *Ledger) VerifyTrace(source string) error {
	want, err := l.markedFractions(source)
	if err != nil {
		return err
	}
	got := make(map[string]*big.Rat)
	var walk func(id string, acc *big.Rat)
	walk = func(id string, acc *big.Rat) {
		for _, e := range l.children[id] {
			head := l.batches[e.to]
			w := new(big.Rat).Mul(acc, new(big.Rat).Quo(e.amt, head.initQty))
			addTo(got, e.to, w)
			walk(e.to, w)
		}
	}
	walk(source, big.NewRat(1, 1))
	for id, g := range got {
		w := want[id]
		if w == nil || w.Cmp(g) != 0 {
			return fmt.Errorf("tracebatch: trace mismatch for source %s at %s: path-sum %s, folded %s",
				source, id, g.RatString(), ratString(w))
		}
	}
	for id, w := range want {
		if id == source || w.Sign() == 0 {
			continue
		}
		if got[id] == nil {
			return fmt.Errorf("tracebatch: trace mismatch for source %s at %s: path-sum missing, folded %s",
				source, id, w.RatString())
		}
	}
	return nil
}

// CheckConservation verifies mass conservation for every origin batch:
// registered quantity == mass held across live batches + mass scrapped.
func (l *Ledger) CheckConservation() error {
	held := make(map[string]*big.Rat)
	for _, id := range l.order {
		b := l.batches[id]
		if !b.live {
			continue
		}
		for origin, m := range b.comp {
			addTo(held, origin, m)
		}
	}
	for origin, init := range l.initial {
		total := new(big.Rat)
		if h := held[origin]; h != nil {
			total.Add(total, h)
		}
		if s := l.scrapped[origin]; s != nil {
			total.Add(total, s)
		}
		if total.Cmp(init) != 0 {
			return fmt.Errorf("tracebatch: mass not conserved for origin %s: registered %s, accounted %s",
				origin, init.RatString(), total.RatString())
		}
	}
	return nil
}

func ratString(r *big.Rat) string {
	if r == nil {
		return "<nil>"
	}
	return r.RatString()
}
