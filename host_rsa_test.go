package parity_test

import (
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"os"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

// hostOracle simulates the regression host callback. The HOST owns and uses the
// private key here; the parity package only receives the returned bit.
func hostOracle(priv *rsa.PrivateKey) parity.Callback {
	N := priv.N
	D := priv.D
	return func(x *big.Int) (parity.Parity, error) {
		if x == nil || x.Sign() < 0 || x.Cmp(N) >= 0 {
			return parity.ParityRejected, parity.ErrInvalidQuery
		}
		z := new(big.Int).Exp(x, D, N)
		if z.Bit(0) == 1 {
			return parity.ParityOdd, nil
		}
		return parity.ParityEven, nil
	}
}

func runHostKeyRandomVerification(t *testing.T, bits, randomCases int, baseBlind bool) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("host key generation: %v", err)
	}
	pub := &priv.PublicKey
	if pub.N.Bit(0) == 0 {
		t.Fatal("RSA modulus must be odd")
	}
	e := big.NewInt(int64(pub.E))
	oracle := hostOracle(priv)

	plaintexts := []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		new(big.Int).Sub(pub.N, big.NewInt(1)),
		new(big.Int).Sub(pub.N, big.NewInt(2)),
	}
	for i := 0; i < randomCases; i++ {
		m, err := rand.Int(rand.Reader, pub.N)
		if err != nil {
			t.Fatal(err)
		}
		plaintexts = append(plaintexts, m)
	}

	for idx, m := range plaintexts {
		c := new(big.Int).Exp(m, e, pub.N)
		p := parity.Params{N: pub.N, E: e, C: c, KeyVersion: "host-key-1"}
		opts := parity.Options{}
		if baseBlind {
			opts.BaseBlind = true
		}
		res, err := parity.Recover(p, oracle, opts)
		if err != nil {
			t.Fatalf("case %d m-bitlen=%d baseBlind=%v: %v", idx, m.BitLen(), baseBlind, err)
		}
		if res.Status != parity.StatusRecovered || res.Plaintext.Cmp(m) != 0 {
			t.Fatalf("case %d: status=%s recovered-bitlen=%v want-bitlen=%d candidates=%s",
				idx, res.Status, bitLen(res.Plaintext), m.BitLen(), res.Candidates)
		}
		if new(big.Int).Exp(res.Plaintext, e, pub.N).Cmp(c) != 0 {
			t.Fatalf("case %d: final m^e mod N != c", idx)
		}
	}
}

func bitLen(m *big.Int) int {
	if m == nil {
		return -1
	}
	return m.BitLen()
}

func TestHostGeneratedKeyRandom1024(t *testing.T) {
	runHostKeyRandomVerification(t, 1024, 5, false)
}

func TestHostGeneratedKeyBaseBlind1024(t *testing.T) {
	runHostKeyRandomVerification(t, 1024, 2, true)
}

func TestHostGeneratedKeyLarge2048OptIn(t *testing.T) {
	if os.Getenv("PARITY_TEST_LARGE") == "" {
		t.Skip("set PARITY_TEST_LARGE=1 to run the 2048-bit host verification")
	}
	runHostKeyRandomVerification(t, 2048, 3, false)
}
