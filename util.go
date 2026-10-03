package ratbezier

import (
	"math/big"
	"strconv"
)

// ratDecimal renders a rational as a compact decimal string (about 15
// significant digits) for display purposes.
func ratDecimal(r *big.Rat) string {
	f, _ := new(big.Float).SetPrec(256).SetRat(r).Float64()
	return strconv.FormatFloat(f, 'g', -1, 64)
}
