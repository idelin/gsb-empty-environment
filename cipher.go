package netsec

import (
	"fmt"
)

const (
	// StateBits is the block size of the SPN in bits.
	StateBits = 16
	// StateSize is the number of distinct block values (2^StateBits).
	StateSize = 1 << StateBits
	// NibbleCount is the number of 4-bit S-boxes applied per round.
	NibbleCount = 4
	// MinRounds is the minimum supported round count.
	MinRounds = 1
	// MaxRounds is the maximum supported round count.
	MaxRounds = 4
)

// Cipher is a 16-bit substitution-permutation network.
//
// Each round consists of, in order:
//  1. XOR of the round key (round keys are XORed at the start of each
//     round; they do not influence differential propagation),
//  2. a substitution layer applying the same 16-entry S-box to each of
//     the 4 nibbles (nibble 0 holds bits 0..3, the least significant bit
//     is bit 0),
//  3. a bit permutation: input bit i is moved to output position perm[i].
//
// Differences and round keys are 16-bit values, i.e. always within
// 0..65535 as required.
type Cipher struct {
	sbox   [16]uint8
	perm   [16]uint8
	rounds int

	ddt       [16][16]uint8
	sboxTab   [StateSize]uint16
	permTab   [StateSize]uint16
	unpermTab [StateSize]uint16
}

// NewCipher builds an SPN from a 16-entry bijective S-box, a 16-position
// bit permutation and a round count in [MinRounds, MaxRounds]. It returns
// an error if the S-box or the permutation is not a bijection on
// {0..15}, or if the round count is out of range.
func NewCipher(sbox []uint8, perm []uint8, rounds int) (*Cipher, error) {
	if rounds < MinRounds || rounds > MaxRounds {
		return nil, fmt.Errorf("netsec: rounds %d out of range [%d, %d]", rounds, MinRounds, MaxRounds)
	}
	if len(sbox) != 16 {
		return nil, fmt.Errorf("netsec: S-box must have exactly 16 entries, got %d", len(sbox))
	}
	var sSeen [16]bool
	for i, v := range sbox {
		if v > 15 {
			return nil, fmt.Errorf("netsec: S-box entry sbox[%d]=%d is not a nibble", i, v)
		}
		if sSeen[v] {
			return nil, fmt.Errorf("netsec: S-box is not a bijection: value %d appears more than once", v)
		}
		sSeen[v] = true
	}
	if len(perm) != 16 {
		return nil, fmt.Errorf("netsec: bit permutation must have exactly 16 entries, got %d", len(perm))
	}
	var pSeen [16]bool
	for i, p := range perm {
		if p > 15 {
			return nil, fmt.Errorf("netsec: permutation entry perm[%d]=%d is not a bit position", i, p)
		}
		if pSeen[p] {
			return nil, fmt.Errorf("netsec: bit permutation is not a bijection: position %d appears more than once", p)
		}
		pSeen[p] = true
	}

	c := &Cipher{rounds: rounds}
	copy(c.sbox[:], sbox)
	copy(c.perm[:], perm)
	c.build()
	return c, nil
}

func (c *Cipher) build() {
	for a := 0; a < 16; a++ {
		for x := 0; x < 16; x++ {
			c.ddt[a][c.sbox[x]^c.sbox[x^a]]++
		}
	}
	for v := 0; v < StateSize; v++ {
		var s uint16
		for n := 0; n < NibbleCount; n++ {
			s |= uint16(c.sbox[(v>>(4*n))&0xF]) << (4 * n)
		}
		c.sboxTab[v] = s

		var p uint16
		for i := 0; i < StateBits; i++ {
			if v&(1<<i) != 0 {
				p |= 1 << c.perm[i]
			}
		}
		c.permTab[v] = p
	}
	for v := 0; v < StateSize; v++ {
		c.unpermTab[c.permTab[v]] = uint16(v)
	}
}

// Rounds returns the configured round count.
func (c *Cipher) Rounds() int { return c.rounds }

// DDT returns a copy of the S-box difference distribution table:
// DDT[a][b] counts the x in 0..15 with S(x)^S(x^a) == b.
func (c *Cipher) DDT() [16][16]uint8 { return c.ddt }

// SBox returns a copy of the S-box.
func (c *Cipher) SBox() [16]uint8 { return c.sbox }

// Permutation returns a copy of the bit permutation: input bit i moves
// to output position perm[i].
func (c *Cipher) Permutation() [16]uint8 { return c.perm }

// SubNibbles applies the S-box layer to a 16-bit state.
func (c *Cipher) SubNibbles(v uint16) uint16 { return c.sboxTab[v] }

// PermuteBits applies the bit permutation to a 16-bit state.
func (c *Cipher) PermuteBits(v uint16) uint16 { return c.permTab[v] }

// Encrypt enciphers a 16-bit block. roundKeys must contain exactly one
// key per round; key i is XORed at the start of round i. Every key is a
// uint16 and therefore trivially within 0..65535.
func (c *Cipher) Encrypt(block uint16, roundKeys []uint16) (uint16, error) {
	if len(roundKeys) != c.rounds {
		return 0, fmt.Errorf("netsec: need exactly %d round keys, got %d", c.rounds, len(roundKeys))
	}
	return c.encrypt(block, roundKeys), nil
}

func (c *Cipher) encrypt(block uint16, roundKeys []uint16) uint16 {
	x := block
	for i := 0; i < c.rounds; i++ {
		x ^= roundKeys[i]
		x = c.sboxTab[x]
		x = c.permTab[x]
	}
	return x
}
