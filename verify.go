package netsec

import (
	"fmt"
	"math/big"
)

// CountDifferential is the ground-truth verifier: it enumerates all
// 65536 plaintexts x and counts how many satisfy
//
//	E(x) ^ E(x^inDiff) == outDiff
//
// under the caller-supplied round keys (exactly one key per round, each
// a uint16 and therefore within 0..65535). It returns the count and the
// exact true differential probability count/65536.
//
// Note this counts ordered pairs: x and x^inDiff are both enumerated,
// so the counts over all outDiff values sum to exactly 65536.
func (c *Cipher) CountDifferential(roundKeys []uint16, inDiff, outDiff uint16) (uint32, *big.Rat, error) {
	if len(roundKeys) != c.rounds {
		return 0, nil, fmt.Errorf("netsec: need exactly %d round keys, got %d", c.rounds, len(roundKeys))
	}
	var count uint32
	for x := 0; x < StateSize; x++ {
		y1 := c.encrypt(uint16(x), roundKeys)
		y2 := c.encrypt(uint16(x)^inDiff, roundKeys)
		if y1^y2 == outDiff {
			count++
		}
	}
	prob := big.NewRat(int64(count), StateSize)
	return count, prob, nil
}
