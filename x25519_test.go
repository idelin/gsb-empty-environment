package netsec

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func leBytes(v *big.Int) []byte {
	out := make([]byte, 32)
	v.FillBytes(out)
	return reverseBytes(out)
}

// RFC 7748 §6.1 的 X25519 测试向量（线格式为小端）。
func TestX25519RFC7748Vector(t *testing.T) {
	alicePriv := mustHex(t, "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	alicePub := mustHex(t, "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	bobPriv := mustHex(t, "5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb")
	bobPub := mustHex(t, "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f")
	wantSecret := mustHex(t, "4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742")

	aPriv, err := ecdh.X25519().NewPrivateKey(alicePriv)
	if err != nil {
		t.Fatal(err)
	}
	bPriv, err := ecdh.X25519().NewPrivateKey(bobPriv)
	if err != nil {
		t.Fatal(err)
	}

	if got := aPriv.PublicKey().Bytes(); !equalBytes(got, alicePub) {
		t.Fatalf("Alice public key mismatch:\n got %x\nwant %x", got, alicePub)
	}
	if got := bPriv.PublicKey().Bytes(); !equalBytes(got, bobPub) {
		t.Fatalf("Bob public key mismatch:\n got %x\nwant %x", got, bobPub)
	}

	if _, err := X25519ParsePublicKey(bobPub); err != nil {
		t.Fatalf("canonical RFC public key rejected: %v", err)
	}

	secret, err := X25519SharedSecret(aPriv, bobPub)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(secret, wantSecret) {
		t.Fatalf("shared secret mismatch:\n got %x\nwant %x", secret, wantSecret)
	}

	// 反向协商应得到相同的共享秘密。
	back, err := X25519SharedSecret(bPriv, alicePub)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(back, wantSecret) {
		t.Fatal("X25519 shared secrets do not agree")
	}
}

func TestX25519GeneratedKeysRoundTrip(t *testing.T) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := priv.PublicKey().Bytes()

	pub, err := X25519ParsePublicKey(enc)
	if err != nil {
		t.Fatalf("generated public key rejected: %v", err)
	}
	if !equalBytes(pub.Bytes(), enc) {
		t.Fatal("parsed public key bytes differ")
	}

	other, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := X25519SharedSecret(priv, other.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s2, err := X25519SharedSecret(other, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(s1, s2) {
		t.Fatal("X25519 shared secrets do not agree")
	}
}

// 低阶 u（RFC 7748 §6.1.2）：通过了 u < p 的规范检查，但对任意私钥
// ECDH 输出都为全零，必须以 ErrSharedSecretZero 拒绝。
func TestX25519RejectLowOrderU(t *testing.T) {
	lowOrder := map[string]*big.Int{
		"u=0":   big.NewInt(0),
		"u=1":   big.NewInt(1),
		"u=p-1": new(big.Int).Sub(x25519P, big.NewInt(1)),
	}

	randomPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	zeroPriv, err := ecdh.X25519().NewPrivateKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	for name, u := range lowOrder {
		enc := leBytes(u)
		pub, parseErr := X25519ParsePublicKey(enc)
		if parseErr != nil {
			t.Fatalf("%s: canonical low-order u should parse, got %v", name, parseErr)
		}
		for _, priv := range []*ecdh.PrivateKey{randomPriv, zeroPriv} {
			if _, ecdhErr := X25519ECDH(priv, pub); !lowOrderRejected(ecdhErr) {
				t.Fatalf("%s: want low-order rejection, got %v", name, ecdhErr)
			}
		}
		if _, oneShotErr := X25519SharedSecret(randomPriv, enc); !lowOrderRejected(oneShotErr) {
			t.Fatalf("%s (one-shot): want low-order rejection, got %v", name, oneShotErr)
		}
	}
}

// lowOrderRejected 兼容两种实现：本包的全零哨兵错误（标准库未拦截时），
// 或 crypto/ecdh 在较新版本中直接返回的低阶点错误。
func lowOrderRejected(err error) bool {
	return err != nil &&
		(errors.Is(err, ErrSharedSecretZero) ||
			err.Error() == "crypto/ecdh: bad X25519 remote ECDH input: low order point")
}

func TestIsAllZeroHelper(t *testing.T) {
	if !isAllZero(make([]byte, 32)) {
		t.Fatal("all-zero slice not detected")
	}
	nonZero := make([]byte, 32)
	nonZero[31] = 1
	if isAllZero(nonZero) {
		t.Fatal("non-zero slice misclassified")
	}
	if isAllZero(nil) {
		t.Fatal("nil slice misclassified")
	}
}

// 非规范 u：u >= 2^255-19 必须在解析阶段拒绝，避免约化碰撞与低阶点攻击。
func TestX25519RejectNonCanonicalU(t *testing.T) {
	// 紧邻边界两侧：p-2、p-1 合法（p-1 另在低阶用例中覆盖），p 起拒绝。
	for delta := int64(-2); delta <= 2; delta++ {
		u := new(big.Int).Add(x25519P, big.NewInt(delta))
		_, err := X25519ParsePublicKey(leBytes(u))
		if delta >= 0 {
			if !errors.Is(err, ErrInvalidPublicKey) {
				t.Fatalf("u=p%+d: want ErrInvalidPublicKey, got %v", delta, err)
			}
		} else if err != nil {
			t.Fatalf("u=p%+d: canonical value rejected: %v", delta, err)
		}
	}

	// 2^255-1 与 2^256-1 同样非规范（注意它们不是低阶点，但编码非法）。
	max255 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(1))
	max256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	for _, u := range []*big.Int{max255, max256} {
		if _, err := X25519ParsePublicKey(leBytes(u)); !errors.Is(err, ErrInvalidPublicKey) {
			t.Fatalf("u=%s: want ErrInvalidPublicKey", u)
		}
	}

	// 高 3 字节置 1 也必然使 u >= 2^255。
	highBitSet := make([]byte, 32)
	highBitSet[31] = 0x80
	if _, err := X25519ParsePublicKey(highBitSet); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("high-bit-set u: want ErrInvalidPublicKey, got %v", err)
	}
}

// 合法但非常见的规范 u（p-2、p-3）应能正常协商出非零秘密。
func TestX25519CanonicalEdgeUSucceeds(t *testing.T) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*big.Int{
		new(big.Int).Sub(x25519P, big.NewInt(2)),
		new(big.Int).Sub(x25519P, big.NewInt(3)),
	} {
		secret, err := X25519SharedSecret(priv, leBytes(u))
		if err != nil {
			t.Fatalf("canonical u=%s rejected: %v", u, err)
		}
		if isAllZero(secret) {
			t.Fatalf("canonical u=%s produced zero secret", u)
		}
	}
}

func TestX25519RejectBadLength(t *testing.T) {
	for _, n := range []int{0, 1, 31, 33, 64} {
		if _, err := X25519ParsePublicKey(make([]byte, n)); !errors.Is(err, ErrInvalidPublicKey) {
			t.Fatalf("length %d: want ErrInvalidPublicKey, got %v", n, err)
		}
	}
}

func TestX25519ECDHRejectsWrongCurveAndNil(t *testing.T) {
	xPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := X25519ECDH(xPriv, pPriv.PublicKey()); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("cross-curve key: want ErrInvalidPublicKey, got %v", err)
	}
	if _, err := X25519ECDH(nil, xPriv.PublicKey()); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("nil private key: want ErrInvalidPublicKey, got %v", err)
	}
	if _, err := X25519ECDH(xPriv, nil); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("nil public key: want ErrInvalidPublicKey, got %v", err)
	}
}

func TestX25519ParseNoPanicOnRandomInput(t *testing.T) {
	for i := 0; i < 500; i++ {
		buf := make([]byte, 1+int(byte(i))%40)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on random input %x: %v", buf, r)
				}
			}()
			_, _ = X25519ParsePublicKey(buf)
		}()
	}
}
