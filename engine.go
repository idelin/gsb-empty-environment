package fxacct

import (
	"fmt"
	"math/big"
)

// Event records one traced step application for audit. In and Out
// are exact rationals; Delta is the rounding gain/loss (Out-In) and
// is nil for exact steps.
type Event struct {
	ItemID   string
	StepIdx  int
	StepName string
	Kind     string // "convert" or "round"
	Currency string
	In       *big.Rat
	Out      *big.Rat
	Delta    *big.Rat
}

// Engine runs a fixed conversion pipeline over bill line items.
type Engine struct {
	Steps []Step
}

func (e Engine) run(it LineItem) (Money, []Event, error) {
	m := Money{Value: new(big.Rat).Set(it.Amount), Currency: it.Currency}
	events := make([]Event, 0, len(e.Steps))
	for i, s := range e.Steps {
		in := new(big.Rat).Set(m.Value)
		out, delta, err := s.Apply(m)
		if err != nil {
			return Money{}, nil, fmt.Errorf("item %s: %w", it.ID, err)
		}
		kind := "convert"
		if s.Rounding() {
			kind = "round"
		}
		events = append(events, Event{
			ItemID:   it.ID,
			StepIdx:  i,
			StepName: s.Name(),
			Kind:     kind,
			Currency: out.Currency,
			In:       in,
			Out:      new(big.Rat).Set(out.Value),
			Delta:    delta,
		})
		m = out
	}
	return m, events, nil
}

// StepPL aggregates rounding gain/loss at one rounding node.
type StepPL struct {
	StepIdx        int
	StepName       string
	Currency       string
	PerItemDelta   *big.Rat // Σ (rounded - exact) over all items
	AggregateDelta *big.Rat // delta when rounding the exact total
}

// Comparison contrasts per-item conversion with aggregate conversion
// of the same bill.
type Comparison struct {
	Currency        string
	PerItemTotal    *big.Rat
	AggregateTotal  *big.Rat
	Difference      *big.Rat // PerItemTotal - AggregateTotal
	StepPLs         []StepPL
	ItemEvents      []Event
	AggregateEvents []Event
}

// Compare converts every item individually (rounding each at the
// Round nodes) and also converts the exact sum of all items
// (rounding the total once per Round node). Because conversion is
// linear, any difference between the two totals is attributable
// entirely to rounding; the per-node gain/loss in StepPLs shows
// exactly where it arises.
func (e Engine) Compare(items []LineItem) (*Comparison, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("fxacct: no line items")
	}
	currency := items[0].Currency
	perItemTotal := new(big.Rat)
	var itemEvents []Event
	perItemDeltas := map[int]*big.Rat{}
	for _, it := range items {
		if it.Currency != currency {
			return nil, fmt.Errorf("fxacct: mixed currencies: %s is %s, want %s", it.ID, it.Currency, currency)
		}
		m, events, err := e.run(it)
		if err != nil {
			return nil, err
		}
		perItemTotal.Add(perItemTotal, m.Value)
		for _, ev := range events {
			if ev.Delta != nil {
				d := perItemDeltas[ev.StepIdx]
				if d == nil {
					d = new(big.Rat)
					perItemDeltas[ev.StepIdx] = d
				}
				d.Add(d, ev.Delta)
			}
		}
		itemEvents = append(itemEvents, events...)
	}

	total := new(big.Rat)
	for _, it := range items {
		total.Add(total, it.Amount)
	}
	aggMoney, aggEvents, err := e.run(LineItem{ID: "<aggregate>", Amount: total, Currency: currency})
	if err != nil {
		return nil, err
	}

	cmp := &Comparison{
		Currency:        aggMoney.Currency,
		PerItemTotal:    perItemTotal,
		AggregateTotal:  aggMoney.Value,
		Difference:      new(big.Rat).Sub(perItemTotal, aggMoney.Value),
		ItemEvents:      itemEvents,
		AggregateEvents: aggEvents,
	}
	for i, s := range e.Steps {
		if !s.Rounding() {
			continue
		}
		pl := StepPL{
			StepIdx:        i,
			StepName:       s.Name(),
			PerItemDelta:   new(big.Rat),
			AggregateDelta: new(big.Rat),
		}
		if d := perItemDeltas[i]; d != nil {
			pl.PerItemDelta.Set(d)
		}
		for _, ev := range aggEvents {
			if ev.StepIdx == i && ev.Delta != nil {
				pl.AggregateDelta.Add(pl.AggregateDelta, ev.Delta)
				pl.Currency = ev.Currency
			}
		}
		cmp.StepPLs = append(cmp.StepPLs, pl)
	}
	return cmp, nil
}
