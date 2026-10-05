package rowconv

import (
	"fmt"
	"math/big"
	"time"
)

var bigMicro = big.NewInt(1000)

// ParseTimestampMicros parses an RFC 3339 timestamp that must carry an
// explicit numeric offset (or Z) and returns UTC integer microseconds.
// Sub-microsecond digits are rejected unless mode is ModeHalfEven.
func ParseTimestampMicros(s string, mode Mode) (int64, *Error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, &Error{Code: CodeBadTimestamp,
			Msg: fmt.Sprintf("%q is not an RFC 3339 timestamp with explicit offset: %v", s, err)}
	}
	// Total nanoseconds since the epoch, exact via big.Int so pre-1970
	// instants and far-future dates cannot overflow mid-computation.
	total := new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(int64(time.Second)))
	total.Add(total, big.NewInt(int64(t.Nanosecond())))

	q, r := new(big.Int), new(big.Int)
	q.QuoRem(total, bigMicro, r)
	if r.Sign() != 0 {
		if mode != ModeHalfEven {
			return 0, &Error{Code: CodeSubmicrosecond,
				Msg: fmt.Sprintf("%q carries sub-microsecond precision", s)}
		}
		roundQuotientHalfEven(q, r, bigMicro, total.Sign() < 0)
	}
	if !fitsInt64(q) {
		return 0, &Error{Code: CodeOverflow,
			Msg: fmt.Sprintf("%q does not fit int64 UTC microseconds", s)}
	}
	return q.Int64(), nil
}
