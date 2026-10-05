package fxacct

import "math/big"

// LineItem is a single bill line. Amount is kept as an exact
// rational; no rounding is applied on entry.
type LineItem struct {
	ID         string
	Amount     *big.Rat
	Currency   string
	ReversalOf string // set when this line corrects an earlier one
}

// Reversal reports whether the item is a correction of another line.
func (it LineItem) Reversal() bool { return it.ReversalOf != "" }

// Reverse returns a correction item that exactly cancels it: the
// negated amount, linked back to the original by ReversalOf.
func (it LineItem) Reverse(id string) LineItem {
	return LineItem{
		ID:         id,
		Amount:     new(big.Rat).Neg(it.Amount),
		Currency:   it.Currency,
		ReversalOf: it.ID,
	}
}
