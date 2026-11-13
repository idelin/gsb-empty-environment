package netsec

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"testing"
)

var errSeedBoom = errors.New("host entropy unavailable (test)")

// deterministicSeed 返回固定 32 字节种子，仅用于可复现实验；生产路径由 HostSeedProvider 提供。
type deterministicSeed struct {
	value [32]byte
	err   error
}

func (d deterministicSeed) Seed() ([32]byte, error) { return d.value, d.err }

func seedFromByte(b byte) [32]byte {
	var s [32]byte
	for i := range s {
		s[i] = b
	}
	return s
}

type fataler interface {
	Fatalf(format string, args ...any)
	Helper()
}

func mustSign(t fataler, priv, pub, msg []byte) Item {
	t.Helper()
	r, s, err := Sign(priv, msg, nil)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return Item{Pub: pub, R: r, S: s, Msg: msg}
}

// referenceInvalid 是独立的逐份参考实现，返回全部无效索引。
func referenceInvalid(t *testing.T, items []Item) []int {
	t.Helper()
	var bad []int
	for i, it := range items {
		ok, err := VerifySingle(it.Pub, it.R, it.S, it.Msg)
		if err != nil || !ok {
			bad = append(bad, i)
		}
	}
	return bad
}

func parseForTest(t *testing.T, items []Item) []parsedItem {
	t.Helper()
	parsed := make([]parsedItem, len(items))
	for i, it := range items {
		px, py, err := decodePoint(it.Pub)
		if err != nil {
			t.Fatalf("item %d pub: %v", i, err)
		}
		rx, ry, err := decodePoint(it.R)
		if err != nil {
			t.Fatalf("item %d R: %v", i, err)
		}
		sInt, err := decodeScalar(it.S)
		if err != nil {
			t.Fatalf("item %d s: %v", i, err)
		}
		pub := append([]byte(nil), it.Pub...)
		rEnc := append([]byte(nil), it.R...)
		sEnc := append([]byte(nil), it.S...)
		msg := append([]byte(nil), it.Msg...)
		parsed[i] = parsedItem{
			pub: pub, r: rEnc, s: sEnc, msg: msg,
			px: px, py: py, rx: rx, ry: ry,
			sInt: sInt, e: challenge(pub, rEnc, msg),
		}
	}
	return parsed
}

func onesCoeffs(n int) []*big.Int {
	out := make([]*big.Int, n)
	for i := range out {
		out[i] = big.NewInt(1)
	}
	return out
}

// naiveSumPasses 复现漏洞服务的做法：所有项系数恒为 1，直接把签名相加。
func naiveSumPasses(items []parsedItem) bool {
	return subsetEquation(items, onesCoeffs(len(items)), allIndices(len(items)))
}

func addQToScalar(enc []byte) []byte {
	v := new(big.Int).SetBytes(enc)
	v.Add(v, big.NewInt(1)).Mod(v, q)
	if v.Sign() == 0 {
		panic("unexpected zero scalar")
	}
	return encodeScalar(v)
}

// TestSingleVerifyRoundTrip 验证真实签名单验通过、篡改后失败。
func TestSingleVerifyRoundTrip(t *testing.T) {
	priv, pub, err := GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	msg := []byte("hello schnorr")
	item := mustSign(t, priv, pub, msg)

	ok, err := VerifySingle(item.Pub, item.R, item.S, item.Msg)
	if err != nil || !ok {
		t.Fatalf("valid signature rejected: ok=%v err=%v", ok, err)
	}

	tampered := append([]byte(nil), item.Msg...)
	tampered[0] ^= 0x01
	ok, err = VerifySingle(item.Pub, item.R, item.S, tampered)
	if err != nil || ok {
		t.Fatalf("tampered message accepted: ok=%v err=%v", ok, err)
	}
}

// TestAlgebraicCancellation 复现“两份错误项互相抵消”攻击，
// 并证明随机系数批量验证在不同种子下均拒绝、无效集合与逐份参考一致。
func TestAlgebraicCancellation(t *testing.T) {
	priv1, pub1, err := GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey 1: %v", err)
	}
	priv2, pub2, err := GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey 2: %v", err)
	}

	item1 := mustSign(t, priv1, pub1, []byte("payment-1"))
	item2 := mustSign(t, priv2, pub2, []byte("payment-2"))

	// 攻击：s0' = s0 + 1 mod q，s1' = s1 - 1 mod q。
	// R、P、消息均不变，两份签名单验都失效，但朴素相加的错误量 (+G) 与 (-G) 抵消。
	attacked := []Item{item1, item2}
	attacked[0].S = addQToScalar(item1.S)
	s1 := new(big.Int).SetBytes(item2.S)
	s1.Sub(s1, big.NewInt(1)).Mod(s1, q)
	attacked[1].S = encodeScalar(s1)

	for i, it := range attacked {
		ok, err := VerifySingle(it.Pub, it.R, it.S, it.Msg)
		if err != nil || ok {
			t.Fatalf("attacked item %d should fail single verify: ok=%v err=%v", i, ok, err)
		}
	}

	parsed := parseForTest(t, attacked)
	if !naiveSumPasses(parsed) {
		t.Fatal("vulnerable naive sum (all coefficients = 1) unexpectedly rejected the cancellation pair")
	}
	t.Log("naive direct-add batch accepted both invalid signatures (attack reproduced)")

	want := referenceInvalid(t, attacked)
	if !reflect.DeepEqual(want, []int{0, 1}) {
		t.Fatalf("reference invalid set = %v, want [0 1]", want)
	}

	// 随机系数使两份错误项带权 a0*G 与 -a1*G，仅在 a0=a1 时抵消，概率可忽略。
	for _, b := range []byte{0x11, 0x22} {
		seed := seedFromByte(b)
		res, err := VerifyBatch(attacked, deterministicSeed{value: seed})
		if err != nil {
			t.Fatalf("VerifyBatch seed %#x: %v", b, err)
		}
		if res.Status != StatusRejected {
			t.Fatalf("seed %#x: status=%s, want rejected", b, res.Status)
		}
		if !reflect.DeepEqual(res.Invalid, want) {
			t.Fatalf("seed %#x: invalid=%v want=%v", b, res.Invalid, want)
		}
		if len(res.Coefficients) != 2 || res.SeedDigest == nil || res.TranscriptDigest == nil {
			t.Fatalf("seed %#x: audit digests missing", b)
		}
		if bytes.Equal(res.Coefficients[0][:], res.Coefficients[1][:]) {
			t.Fatalf("seed %#x: coefficients unexpectedly equal", b)
		}
		t.Logf("seed %#x: rejected, invalid=%v, seedDigest=%x, transcriptDigest=%x",
			b, res.Invalid, res.SeedDigest[:8], res.TranscriptDigest[:8])
	}

	// 真实宿主随机种子同样拒绝，且每批新取、摘要不同。
	var prevDigest []byte
	for run := 0; run < 2; run++ {
		res, err := VerifyBatch(attacked, HostSeedProvider{})
		if err != nil {
			t.Fatalf("host seed run %d: %v", run, err)
		}
		if res.Status != StatusRejected || !reflect.DeepEqual(res.Invalid, want) {
			t.Fatalf("host run %d: status=%s invalid=%v", run, res.Status, res.Invalid)
		}
		if run == 1 && bytes.Equal(prevDigest, res.SeedDigest) {
			t.Fatal("host seed digest repeated across batches")
		}
		prevDigest = append(prevDigest[:0], res.SeedDigest...)
	}
}
