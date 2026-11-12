package netsec

import (
	"crypto/rand"
	"math/big"
)

// randInt returns a uniform random integer in [0, max).
func randInt(max *big.Int) (*big.Int, error) {
	return rand.Int(rand.Reader, max)
}
