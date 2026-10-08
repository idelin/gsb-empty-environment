package material

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Trace is the provenance report for one lot: the lot expressed as an
// exact linear combination of sources, both as normalized shares and as
// absolute rational amounts (share × quantity).
type Trace struct {
	LotID   string
	Qty     int64
	Shares  Vector              // source -> share (sums to 1)
	Amounts map[string]*big.Rat // source -> absolute amount (sums to Qty)
}

// Trace computes the provenance linear combination of any live lot.
func (s *Simulator) Trace(lotID string) (*Trace, error) {
	lot, ok := s.lots[lotID]
	if !ok {
		return nil, fmt.Errorf("lot %s not found", lotID)
	}
	t := &Trace{
		LotID:   lotID,
		Qty:     lot.Qty,
		Shares:  lot.Vec.Clone(),
		Amounts: map[string]*big.Rat{},
	}
	q := big.NewRat(lot.Qty, 1)
	for src, share := range lot.Vec {
		t.Amounts[src] = new(big.Rat).Mul(share, q)
	}
	return t, nil
}

// String renders the trace as an exact linear combination, e.g.
//   lot F (qty 10) = 3/5·A + 2/5·B   [amounts: A=6, B=4]
func (t *Trace) String() string {
	keys := make([]string, 0, len(t.Shares))
	for k := range t.Shares {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	shares := make([]string, 0, len(keys))
	amounts := make([]string, 0, len(keys))
	for _, k := range keys {
		shares = append(shares, fmt.Sprintf("%s·%s", t.Shares[k].RatString(), k))
		amounts = append(amounts, fmt.Sprintf("%s=%s", k, t.Amounts[k].RatString()))
	}
	return fmt.Sprintf("lot %s (qty %d) = %s   [amounts: %s]",
		t.LotID, t.Qty, strings.Join(shares, " + "), strings.Join(amounts, ", "))
}
