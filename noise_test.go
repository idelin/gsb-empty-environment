package parity_test

import (
	"math/big"
	"math/rand"
	"testing"
	"time"

	parity "prescreen.local/ps-2b994de5fb"
)

// noisyOracle injects tiny delays and erroneous rate-limit/unavailable errors.
type noisyOracle struct {
	inner parity.Callback
	rng   *rand.Rand
}

func (n *noisyOracle) query(x *big.Int) (parity.Parity, error) {
	time.Sleep(time.Microsecond)
	if n.rng.Intn(100) < 30 {
		return parity.ParityRejected, parity.ErrUnavailable
	}
	return n.inner(x)
}

func TestRandomDelayAndRateLimitNoise(t *testing.T) {
	k := makeTinyKey(11, 13) // N=143
	for m64 := int64(0); m64 < k.N.Int64(); m64++ {
		n := &noisyOracle{inner: k.oracle(), rng: rand.New(rand.NewSource(1000 + m64))}
		res, err := parity.Recover(
			paramsFor(k, m64),
			n.query,
			parity.Options{
				// Plain retries keep the exact dyadic interval; noise must not
				// change the recovered plaintext.
				Blinding:    parity.BlindNone,
				QueryBudget: 5000,
			},
		)
		if err != nil {
			t.Fatalf("m=%d: %v", m64, err)
		}
		if res.Plaintext.Int64() != m64 {
			t.Fatalf("m=%d got %s", m64, res.Plaintext)
		}
	}
}
