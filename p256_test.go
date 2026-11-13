package netsec

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"math/big"
	"testing"
)

func p256Generator(t *testing.T) (gx, gy *big.Int) {
	t.Helper()
	params := elliptic.P256().Params()
	return new(big.Int).Set(params.Gx), new(big.Int).Set(params.Gy)
}

func p256Compressed(x *big.Int, yOdd bool) []byte {
	out := make([]byte, 33)
	if yOdd {
		out[0] = 0x03
	} else {
		out[0] = 0x02
	}
	x.FillBytes(out[1:33])
	return out
}

func p256Uncompressed(x, y *big.Int) []byte {
	out := make([]byte, 65)
	out[0] = 0x04
	x.FillBytes(out[1:33])
	y.FillBytes(out[33:65])
	return out
}

func TestP256ParseValidUncompressed(t *testing.T) {
	gx, gy := p256Generator(t)
	enc := p256Uncompressed(gx, gy)

	pub, err := P256ParsePublicKey(enc)
	if err != nil {
		t.Fatalf("generator point rejected: %v", err)
	}
	if pub.Curve() != ecdh.P256() {
		t.Fatalf("unexpected curve: %v", pub.Curve())
	}
	if got := pub.Bytes(); !equalBytes(got, enc) {
		t.Fatalf("round-trip mismatch\n got %x\nwant %x", got, enc)
	}
}

func TestP256ParseCompressedBothParities(t *testing.T) {
	gx, gy := p256Generator(t)

	for _, yOdd := range []bool{false, true} {
		enc := p256Compressed(gx, yOdd)
		pub, err := P256ParsePublicKey(enc)
		if err != nil {
			t.Fatalf("compressed point (odd=%v) rejected: %v", yOdd, err)
		}

		// 从标准库的未压缩输出里取回 y，核对奇偶与曲线归属。
		raw := pub.Bytes()
		gotY := new(big.Int).SetBytes(raw[33:65])
		if gotY.Bit(0) != boolToBit(yOdd) {
			t.Fatalf("parity mismatch: want odd=%v", yOdd)
		}
		gotX := new(big.Int).SetBytes(raw[1:33])
		if !elliptic.P256().IsOnCurve(gotX, gotY) {
			t.Fatalf("decompressed point not on P-256")
		}
		// 两个平方根互为相反数。
		wantOther := new(big.Int).Sub(p256P, gy)
		if yOdd != (gy.Bit(0) == 1) && gotY.Cmp(wantOther) != 0 {
			t.Fatalf("expected -G.y for opposite parity")
		}
	}
}

func TestP256CompressedMatchesUncompressedECDH(t *testing.T) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gx, gy := p256Generator(t)

	pubU, err := P256ParsePublicKey(p256Uncompressed(gx, gy))
	if err != nil {
		t.Fatal(err)
	}
	pubC, err := P256ParsePublicKey(p256Compressed(gx, gy.Bit(0) == 1))
	if err != nil {
		t.Fatal(err)
	}

	s1, err := P256ECDH(priv, pubU)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := P256ECDH(priv, pubC)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(s1, s2) {
		t.Fatalf("compressed/uncompressed ECDH differ")
	}
}

func TestP256RandomRoundTrip(t *testing.T) {
	for i := 0; i < 16; i++ {
		priv, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw := priv.PublicKey().Bytes() // 65 字节未压缩
		x := new(big.Int).SetBytes(raw[1:33])
		y := new(big.Int).SetBytes(raw[33:65])

		if _, err := P256ParsePublicKey(raw); err != nil {
			t.Fatalf("uncompressed round-trip failed: %v", err)
		}
		if _, err := P256ParsePublicKey(p256Compressed(x, y.Bit(0) == 1)); err != nil {
			t.Fatalf("compressed round-trip failed: %v", err)
		}
	}
}

func TestP256RejectOffCurve(t *testing.T) {
	gx, gy := p256Generator(t)

	// 未压缩：翻转 y 的最后一个字节，点离开曲线。
	bad := p256Uncompressed(gx, new(big.Int).Xor(gy, big.NewInt(1)))
	if elliptic.P256().IsOnCurve(gx, new(big.Int).Xor(gy, big.NewInt(1))) {
		t.Fatal("test vector unexpectedly on curve")
	}
	if _, err := P256ParsePublicKey(bad); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("off-curve uncompressed: want ErrInvalidPublicKey, got %v", err)
	}

	// 压缩：取一个 rhs 为二次非剩余的 x，平方根不存在。
	var nonResidueX *big.Int
	for x := big.NewInt(1); ; x.Add(x, big.NewInt(1)) {
		if _, err := p256DecompressY(new(big.Int).Set(x), false); err != nil {
			nonResidueX = new(big.Int).Set(x)
			break
		}
	}
	if _, err := P256ParsePublicKey(p256Compressed(nonResidueX, false)); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("non-residue compressed point: want ErrInvalidPublicKey, got %v", err)
	}
}

func TestP256RejectNonCanonicalCoordinates(t *testing.T) {
	p := new(big.Int).Set(p256P)
	one := big.NewInt(1)
	max256 := new(big.Int).Sub(new(big.Int).Lsh(one, 256), one) // 2^256 - 1

	cases := map[string][]byte{
		"compressed x = p":          p256Compressed(p, false),
		"compressed x = p+1":        p256Compressed(new(big.Int).Add(p, one), false),
		"compressed x = 2^256-1":    p256Compressed(max256, false),
		"uncompressed x = p, y = 0": p256Uncompressed(p, big.NewInt(0)),
		"uncompressed y = p":        p256Uncompressed(big.NewInt(0), p),
		"uncompressed x = 2^256-1":  p256Uncompressed(max256, big.NewInt(0)),
		"uncompressed y = 2^256-1":  p256Uncompressed(big.NewInt(0), max256),
	}
	for name, enc := range cases {
		if _, err := P256ParsePublicKey(enc); !errors.Is(err, ErrInvalidPublicKey) {
			t.Fatalf("%s: want ErrInvalidPublicKey, got %v", name, err)
		}
	}
}

func TestP256RejectInfinityAndBadEncodings(t *testing.T) {
	gx, gy := p256Generator(t)
	good := p256Uncompressed(gx, gy)

	hybridEven := append([]byte{0x06}, good[1:]...)
	hybridOdd := append([]byte{0x07}, good[1:]...)
	compressedWithWrongTag := append([]byte{0x04}, p256Compressed(gx, false)[1:]...)

	cases := map[string][]byte{
		"infinity 0x00":          {0x00},
		"empty":                  {},
		"truncated uncompressed": good[:64],
		"trailing byte":          append(append([]byte{}, good...), 0x00),
		"truncated compressed":   p256Compressed(gx, false)[:32],
		"hybrid 0x06":            hybridEven,
		"hybrid 0x07":            hybridOdd,
		"compressed tag 0x04":    compressedWithWrongTag,
		"tag 0x05":               append([]byte{0x05}, good[1:33]...),
		"all zero 65 bytes":      make([]byte, 65),
	}
	for name, enc := range cases {
		if _, err := P256ParsePublicKey(enc); !errors.Is(err, ErrInvalidPublicKey) {
			t.Fatalf("%s: want ErrInvalidPublicKey, got %v", name, err)
		}
	}
}

func TestP256MutualAgreement(t *testing.T) {
	a, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// A 用压缩编码发送公钥，B 用未压缩编码发送。
	rawA := a.PublicKey().Bytes()
	ax := new(big.Int).SetBytes(rawA[1:33])
	ay := new(big.Int).SetBytes(rawA[33:65])
	encA := p256Compressed(ax, ay.Bit(0) == 1)
	encB := b.PublicKey().Bytes()

	secretA, err := P256SharedSecret(a, encB)
	if err != nil {
		t.Fatal(err)
	}
	secretB, err := P256SharedSecret(b, encA)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(secretA, secretB) {
		t.Fatal("P-256 shared secrets do not agree")
	}
}

func TestP256ECDHRejectsWrongCurveAndNil(t *testing.T) {
	p256Priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	xPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := P256ECDH(p256Priv, xPriv.PublicKey()); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("cross-curve key: want ErrInvalidPublicKey, got %v", err)
	}
	if _, err := P256ECDH(nil, p256Priv.PublicKey()); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("nil private key: want ErrInvalidPublicKey, got %v", err)
	}
	if _, err := P256ECDH(p256Priv, nil); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("nil public key: want ErrInvalidPublicKey, got %v", err)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 任意随机字节都不应触发 panic，只允许成功或返回校验错误。
func TestP256ParseNoPanicOnRandomInput(t *testing.T) {
	for i := 0; i < 1000; i++ {
		n := 1 + int(byte(i))
		if n > 66 {
			n = n % 66
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on random input %x: %v", buf, r)
				}
			}()
			_, _ = P256ParsePublicKey(buf)
		}()
	}
}
