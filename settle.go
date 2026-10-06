package settle

import (
	"fmt"
	"math/big"
	"time"
)

// LineItem is the exact (unrounded) quantity and amount charged against
// one rate segment.
type LineItem struct {
	Segment  Segment
	Quantity *big.Rat
	Amount   *big.Rat // Quantity * Segment.Rate, exact
}

// Statement is the settlement of a batch of usages against a rate book.
// ExactTotal is the exact rational sum; Total is ExactTotal rounded
// half-even to cents. Rounding happens exactly once, at the end.
type Statement struct {
	Items      []LineItem
	ExactTotal *big.Rat
	Total      *big.Rat
}

// Settle allocates every usage to the rate segments of the book and
// prices them. It fails if any usage falls outside rate coverage.
func (b *Book) Settle(usages []Usage) (*Statement, error) {
	items, err := b.allocateAll(usages)
	if err != nil {
		return nil, err
	}
	total := new(big.Rat)
	for _, it := range items {
		total.Add(total, it.Amount)
	}
	return &Statement{
		Items:      items,
		ExactTotal: total,
		Total:      RoundToCents(total),
	}, nil
}

func (b *Book) allocateAll(usages []Usage) ([]LineItem, error) {
	acc := make(map[int]*big.Rat) // segment index -> quantity
	for i, u := range usages {
		if u == nil {
			return nil, fmt.Errorf("settle: usage %d is nil", i)
		}
		start, end := u.span()
		if start.Equal(end) {
			if _, ok := b.SegmentAt(start); !ok {
				return nil, fmt.Errorf("settle: usage %d at %s outside rate coverage", i, start.Format(time.RFC3339Nano))
			}
		} else if !b.covered(start, end) {
			return nil, fmt.Errorf("settle: usage %d over [%s, %s) outside rate coverage", i, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
		}
		for j, seg := range b.segments {
			q := u.allocate(seg)
			if q.Sign() != 0 {
				if acc[j] == nil {
					acc[j] = new(big.Rat)
				}
				acc[j].Add(acc[j], q)
			}
		}
	}
	var items []LineItem
	for j, seg := range b.segments {
		q := acc[j]
		if q == nil || q.Sign() == 0 {
			continue
		}
		items = append(items, LineItem{
			Segment:  seg,
			Quantity: q,
			Amount:   new(big.Rat).Mul(q, seg.Rate),
		})
	}
	return items, nil
}

// Delta is the price difference caused by a retroactive rate revision.
// It reports only the difference, never a restated total. ExactAmount is
// the exact rational difference; Amount is ExactAmount rounded half-even
// to cents.
type Delta struct {
	Segment     Segment // the revised segment (with the new rate)
	OldRate     *big.Rat
	NewRate     *big.Rat
	Quantity    *big.Rat // exact quantity affected
	ExactAmount *big.Rat // Quantity * (NewRate - OldRate)
	Amount      *big.Rat // ExactAmount rounded to cents
}

// Revise retroactively replaces the rate of the segment exactly matching
// seg's [Start, End) interval, applies it to the book, and returns the
// price difference for the given usages. The usages themselves are not
// re-settled; only the difference is reported.
func (b *Book) Revise(seg Segment, usages []Usage) (*Delta, error) {
	idx := -1
	for i, s := range b.segments {
		if s.Start.Equal(seg.Start) && sameEnd(s.End, seg.End) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("settle: no segment matching [%s, %s) to revise",
			seg.Start.Format(time.RFC3339Nano), endString(seg.End))
	}
	if seg.Rate == nil {
		return nil, fmt.Errorf("settle: revised segment has nil rate")
	}
	old := b.segments[idx]

	qty := new(big.Rat)
	for _, u := range usages {
		if u == nil {
			continue
		}
		qty.Add(qty, u.allocate(old))
	}

	exact := new(big.Rat).Mul(qty, new(big.Rat).Sub(seg.Rate, old.Rate))
	b.segments[idx] = seg

	return &Delta{
		Segment:     seg,
		OldRate:     new(big.Rat).Set(old.Rate),
		NewRate:     new(big.Rat).Set(seg.Rate),
		Quantity:    qty,
		ExactAmount: exact,
		Amount:      RoundToCents(exact),
	}, nil
}

func sameEnd(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	return a.Equal(b)
}

func endString(end time.Time) string {
	if end.IsZero() {
		return "+inf"
	}
	return end.Format(time.RFC3339Nano)
}
