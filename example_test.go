package parity_test

import (
	"fmt"
	"math/big"

	parity "prescreen.local/ps-2b994de5fb"
)

// This example is a complete local regression-host template using a tiny RSA
// key. The host owns d; the attack package only sees parity bits.
func Example() {
	N := big.NewInt(143) // 11 * 13
	E := big.NewInt(7)
	D := big.NewInt(103) // 7^{-1} mod (10*12)
	m := big.NewInt(42)
	c := new(big.Int).Exp(m, E, N)

	hostCallback := func(x *big.Int) (parity.Parity, error) {
		// Host-side raw RSA private operation; never passed to the package.
		z := new(big.Int).Exp(x, D, N)
		if z.Bit(0) == 1 {
			return parity.ParityOdd, nil
		}
		return parity.ParityEven, nil
	}

	res, err := parity.NewAttack().
		N(N).
		E(E).
		C(c).
		KeyVersion("test-key-1").
		Callback(hostCallback).
		Run()
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("status=%s recovered=%v verified=%v\n",
		res.Status,
		res.Plaintext.Cmp(m) == 0,
		parity.VerifyCandidate(N, E, c, res.Plaintext))
	// Output: status=recovered recovered=true verified=true
}
