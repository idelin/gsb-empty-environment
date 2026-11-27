package decimal128

// MaxPrecision is the maximum number of significant decimal digits for a
// SQL DECIMAL value supported by this kernel.
const MaxPrecision = 38

var (
	// pow10u128[k] = 10^k for 0 <= k <= 38.
	pow10u128 [39]u128
	// pow10u256[k] = 10^k for 0 <= k <= 77, covering every intermediate
	// scale produced by multiplication (up to 76) and division (up to 77).
	pow10u256 [78]u256
)

func init() {
	pow10u128[0] = u128{lo: 1}
	for k := 1; k <= 38; k++ {
		pow10u128[k], _ = pow10u128[k-1].mulSmall(10, 0)
	}
	pow10u256[0] = u256{l: [4]uint64{1, 0, 0, 0}}
	for k := 1; k <= 77; k++ {
		var carry uint64
		pow10u256[k], carry = pow10u256[k-1].mulSmall(10, 0)
		if carry != 0 {
			panic("pow10u256 table overflow")
		}
	}
}

// limitForPrecision returns 10^p, the exclusive magnitude bound for a
// coefficient declared with precision p.
func limitForPrecision(p int) u128 {
	if p < 0 || p > MaxPrecision {
		panic("precision out of range")
	}
	return pow10u128[p]
}
