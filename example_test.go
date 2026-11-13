package netsec_test

import (
	"math/big"
	"testing"

	netsec "example.org/netsec/q0031"
)

func TestExternalPackageAPI(t *testing.T) {
	// A local toy RSA key: N is prime, e*d = 1 (mod N-1).
	n := big.NewInt(65537)
	e := big.NewInt(3)
	d := big.NewInt(43691)
	b := big.NewInt(256)
	m := big.NewInt(700)
	c := new(big.Int).Exp(m, e, n)

	oracle := netsec.PredicateOracle(func(x *big.Int) (bool, bool) {
		plain := new(big.Int).Exp(x, d, n)
		lo := new(big.Int).Lsh(new(big.Int).Set(b), 1)
		hi := new(big.Int).Mul(new(big.Int).Set(b), big.NewInt(3))
		return plain.Cmp(lo) >= 0 && plain.Cmp(hi) < 0, true
	})

	out, err := netsec.Recover(n, e, c, oracle, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != netsec.StatusRecovered || out.Plaintext == nil || out.Plaintext.Cmp(m) != 0 {
		t.Fatalf("external recovery failed: %+v", out)
	}

	key, err := netsec.NewPublicKey(n, e)
	if err != nil {
		t.Fatal(err)
	}
	session, err := netsec.NewSession(key, c, oracle, 5000)
	if err != nil {
		t.Fatal(err)
	}
	out, err = session.Recover()
	if err != nil || !out.Recovered() {
		t.Fatalf("session recovery failed: %+v, %v", out, err)
	}
}
