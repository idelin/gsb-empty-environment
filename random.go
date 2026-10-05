package wsample

import (
	"errors"
	"io"
	"math/big"
)

var errNonPositiveBound = errors.New("wsample: uniform bound must be positive")

// uniformBelow returns a uniform random value in [0, n) using rejection
// sampling, which is free of modulo bias: the random bytes are interpreted as
// a value in [0, 2^bitLen(n)) and any value >= n is rejected and redrawn,
// never folded back into range with a modulo operation.
func uniformBelow(r io.Reader, n *big.Int) (*big.Int, error) {
	if n.Sign() <= 0 {
		return nil, errNonPositiveBound
	}
	bitLen := uint(n.BitLen())
	numBytes := (bitLen + 7) / 8
	excess := numBytes*8 - bitLen // in [0, 7]
	mask := byte(0xFF >> excess)
	buf := make([]byte, numBytes)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		buf[0] &= mask
		v := new(big.Int).SetBytes(buf)
		if v.Cmp(n) < 0 {
			return v, nil
		}
		// Rejected: draw again. Every accepted value in [0, n) has equal
		// probability, so the result is exactly uniform.
	}
}
