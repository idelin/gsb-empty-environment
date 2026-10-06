// Package settle allocates rational usage quantities to half-open rate
// intervals by event time, settles exact rational totals, and rounds the
// grand total half-even to cents. Retroactive rate changes are reported
// as deltas between rounded totals rather than re-billed totals.
package settle

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Rate is a price per unit of usage over the half-open interval
// [Start, End). An event at exactly End belongs to the next interval.
type Rate struct {
	Start time.Time
	End   time.Time
	Price *big.Rat
}

// Event is an instantaneous usage of Qty units at Time.
type Event struct {
	Time time.Time
	Qty  *big.Rat
}

// Linear is a usage of Qty total units spread uniformly over [Start, End).
// Portions overlapping several rate intervals are prorated by duration.
// A zero-duration Linear contributes nothing.
type Linear struct {
	Start time.Time
	End   time.Time
	Qty   *big.Rat
}

// Segment is the quantity and amount attributed to one rate interval.
type Segment struct {
	Rate   Rate
	Qty    *big.Rat
	Amount *big.Rat
}

// Settlement is the exact result of allocating usage to rate intervals.
type Settlement struct {
	Segments []Segment
	Total    *big.Rat
}

// Cents returns the total rounded half-even (banker's rounding) to cents.
func (s Settlement) Cents() *big.Int {
	return RoundHalfEvenCents(s.Total)
}

// DeltaCents returns the retroactive adjustment caused by a rate change:
// the rounded total of curr minus the rounded total of prev, in cents.
// Deltas telescope: summing deltas over a chain of revisions equals the
// delta between the first and last settlements.
func DeltaCents(prev, curr Settlement) *big.Int {
	return new(big.Int).Sub(curr.Cents(), prev.Cents())
}

// Settle allocates every event and linear usage to the covering rate
// interval and accumulates exact rational amounts. Rates must be sorted
// by Start, non-overlapping, and have Start < End. Usage not fully
// covered by any rate interval is an error, except zero-duration Linears.
func Settle(rates []Rate, events []Event, linears []Linear) (Settlement, error) {
	if err := validateRates(rates); err != nil {
		return Settlement{}, err
	}
	qty := make([]*big.Rat, len(rates))
	for i := range qty {
		qty[i] = new(big.Rat)
	}
	for _, ev := range events {
		if ev.Qty == nil {
			return Settlement{}, errors.New("settle: event with nil Qty")
		}
		i := findRate(rates, ev.Time)
		if i < 0 {
			return Settlement{}, fmt.Errorf("settle: event at %s not covered by any rate interval", ev.Time)
		}
		qty[i].Add(qty[i], ev.Qty)
	}
	for _, ln := range linears {
		if ln.Qty == nil {
			return Settlement{}, errors.New("settle: linear with nil Qty")
		}
		if ln.End.Before(ln.Start) {
			return Settlement{}, fmt.Errorf("settle: linear with End %s before Start %s", ln.End, ln.Start)
		}
		if ln.End.Equal(ln.Start) {
			continue
		}
		if err := allocateLinear(rates, ln, qty); err != nil {
			return Settlement{}, err
		}
	}
	s := Settlement{Total: new(big.Rat)}
	for i, r := range rates {
		if qty[i].Sign() == 0 {
			continue
		}
		amount := new(big.Rat).Mul(qty[i], r.Price)
		s.Segments = append(s.Segments, Segment{
			Rate:   r,
			Qty:    new(big.Rat).Set(qty[i]),
			Amount: amount,
		})
		s.Total.Add(s.Total, amount)
	}
	return s, nil
}

func validateRates(rates []Rate) error {
	for i, r := range rates {
		if r.Price == nil {
			return fmt.Errorf("settle: rate %d has nil Price", i)
		}
		if !r.Start.Before(r.End) {
			return fmt.Errorf("settle: rate %d has Start %s not before End %s", i, r.Start, r.End)
		}
		if i > 0 && r.Start.Before(rates[i-1].End) {
			return fmt.Errorf("settle: rate %d overlaps previous interval", i)
		}
	}
	return nil
}

// findRate returns the index of the half-open interval containing t.
func findRate(rates []Rate, t time.Time) int {
	for i, r := range rates {
		if !t.Before(r.Start) && t.Before(r.End) {
			return i
		}
	}
	return -1
}

func allocateLinear(rates []Rate, ln Linear, qty []*big.Rat) error {
	total := ln.End.Sub(ln.Start)
	covered := time.Duration(0)
	for i, r := range rates {
		start := maxTime(ln.Start, r.Start)
		end := minTime(ln.End, r.End)
		if !start.Before(end) {
			continue
		}
		inter := end.Sub(start)
		covered += inter
		share := new(big.Rat).Mul(ln.Qty, big.NewRat(int64(inter), int64(total)))
		qty[i].Add(qty[i], share)
	}
	if covered != total {
		return fmt.Errorf("settle: linear [%s, %s) not fully covered by rate intervals", ln.Start, ln.End)
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// RoundHalfEvenCents rounds an exact rational amount to an integer number
// of cents, breaking ties to the even cent.
func RoundHalfEvenCents(x *big.Rat) *big.Int {
	scaled := new(big.Rat).Mul(x, big.NewRat(100, 1))
	sign := scaled.Sign()
	if sign == 0 {
		return new(big.Int)
	}
	abs := new(big.Rat).Abs(scaled)
	num := abs.Num()
	den := abs.Denom()
	floor, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// Compare 2*rem with den.
	twice := new(big.Int).Lsh(rem, 1)
	switch twice.Cmp(den) {
	case 1:
		floor.Add(floor, big.NewInt(1))
	case 0:
		if floor.Bit(0) == 1 {
			floor.Add(floor, big.NewInt(1))
		}
	}
	if sign < 0 {
		floor.Neg(floor)
	}
	return floor
}
