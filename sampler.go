package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
)

// DRBG is a deterministic, seed-derived byte stream (SHA-256 in counter
// mode). It is intentionally self-contained instead of using math/rand so
// that the sampling process is reproducible across Go versions and
// platforms: the same caller-provided seed always yields the same stream.
type DRBG struct {
	seed    []byte
	counter uint64
	buf     [sha256.Size]byte
	avail   int
}

const drbgDomain = "archive-audit-drbg-v1:"

func NewDRBG(seed string) *DRBG {
	sum := sha256.Sum256([]byte(drbgDomain + seed))
	d := &DRBG{seed: make([]byte, sha256.Size)}
	copy(d.seed, sum[:])
	return d
}

func (d *DRBG) refill() {
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], d.counter)
	h := sha256.New()
	h.Write(d.seed)
	h.Write(ctr[:])
	sum := h.Sum(nil)
	copy(d.buf[:], sum)
	d.avail = len(d.buf)
	d.counter++
}

// Read fills p with deterministic bytes derived from the seed.
func (d *DRBG) Read(p []byte) {
	for len(p) > 0 {
		if d.avail == 0 {
			d.refill()
		}
		n := copy(p, d.buf[len(d.buf)-d.avail:])
		d.avail -= n
		p = p[n:]
	}
}

// byteSource is anything that can fill a buffer with (pseudo-)random bytes.
type byteSource interface {
	Read(p []byte)
}

// uniformBelow returns a uniform random value in [0, n) using rejection
// sampling. It never reduces a wider random value modulo n, so there is no
// modulo bias regardless of how large n is.
func uniformBelow(d byteSource, n *big.Int) *big.Int {
	if n.Sign() <= 0 {
		panic("uniformBelow: bound must be positive")
	}
	bits := n.BitLen()
	nbytes := (bits + 7) / 8
	excess := uint(nbytes*8 - bits)
	buf := make([]byte, nbytes)
	for {
		d.Read(buf)
		if excess > 0 {
			buf[0] &= 0xff >> excess
		}
		v := new(big.Int).SetBytes(buf)
		if v.Cmp(n) < 0 {
			return v
		}
	}
}

// Draw records one step of the sampling process for audit output.
type Draw struct {
	Step       int      // 0-based draw index
	Remaining  *big.Int // total weight of selectable blocks before this draw
	Random     *big.Int // uniform value in [0, Remaining)
	BlockIndex int      // index into the archive's block list
	Lo         *big.Int // interval [Lo, Hi) within [0, Remaining) that
	Hi         *big.Int // the random value fell into
}

// Sample performs sequential probability-proportional-to-size sampling
// without replacement (PPSWOR): at each step a block is chosen with
// probability weight/totalRemainingWeight, then removed from the pool.
//
// Because the draws are produced sequentially from a single deterministic
// stream, the result for k draws is always a prefix of the result for
// k+m draws: resuming or repeating sampling with the same seed can never
// change blocks that were already selected.
func Sample(seed string, weights []*big.Int, k int) ([]Draw, error) {
	n := len(weights)
	if k < 0 || k > n {
		return nil, fmt.Errorf("cannot draw %d samples from %d blocks", k, n)
	}
	alive := make([]bool, n)
	remaining := big.NewInt(0)
	for i, w := range weights {
		if w.Sign() < 0 {
			return nil, fmt.Errorf("block %d has negative weight", i)
		}
		alive[i] = true
		remaining.Add(remaining, w)
	}
	d := NewDRBG(seed)
	draws := make([]Draw, 0, k)
	for step := 0; step < k; step++ {
		if remaining.Sign() == 0 {
			return nil, fmt.Errorf("draw %d: no positive-weight blocks remain", step)
		}
		r := uniformBelow(d, remaining)
		acc := big.NewInt(0)
		chosen := -1
		var lo, hi *big.Int
		for i := 0; i < n; i++ {
			if !alive[i] {
				continue
			}
			lo = new(big.Int).Set(acc)
			acc.Add(acc, weights[i])
			if r.Cmp(acc) < 0 {
				chosen = i
				hi = new(big.Int).Set(acc)
				break
			}
		}
		if chosen < 0 {
			return nil, fmt.Errorf("draw %d: internal error, no block selected", step)
		}
		draws = append(draws, Draw{
			Step:       step,
			Remaining:  new(big.Int).Set(remaining),
			Random:     r,
			BlockIndex: chosen,
			Lo:         lo,
			Hi:         hi,
		})
		alive[chosen] = false
		remaining.Sub(remaining, weights[chosen])
	}
	return draws, nil
}
