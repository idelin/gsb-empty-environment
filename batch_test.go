package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"reflect"
	"testing"
)

// TestTamperedMessageAndMixedBatch 构造多签名批次，单条消息被篡改，
// 要求递归二分定位精确命中，且与逐份参考无效集合一致。
func TestTamperedMessageAndMixedBatch(t *testing.T) {
	const n = 5
	items := make([]Item, n)
	for i := 0; i < n; i++ {
		priv, pub, err := GenerateKey(nil)
		if err != nil {
			t.Fatalf("GenerateKey %d: %v", i, err)
		}
		items[i] = mustSign(t, priv, pub, []byte("batch-msg-"+string(rune('A'+i))))
	}

	// 篡改索引 3 的消息（追加一个字节）。
	items[3].Msg = append(items[3].Msg, 0xFF)
	want := referenceInvalid(t, items)
	if !reflect.DeepEqual(want, []int{3}) {
		t.Fatalf("reference invalid = %v, want [3]", want)
	}

	res, err := VerifyBatch(items, deterministicSeed{value: seedFromByte(0x33)})
	if err != nil {
		t.Fatalf("VerifyBatch: %v", err)
	}
	if res.Status != StatusRejected || res.Method != MethodBisection {
		t.Fatalf("status=%s method=%s, want rejected/bisection", res.Status, res.Method)
	}
	if !reflect.DeepEqual(res.Invalid, want) {
		t.Fatalf("bisection invalid=%v want=%v", res.Invalid, want)
	}

	// 恢复消息后整批通过。
	items[3].Msg = items[3].Msg[:len(items[3].Msg)-1]
	res, err = VerifyBatch(items, HostSeedProvider{})
	if err != nil {
		t.Fatalf("VerifyBatch valid: %v", err)
	}
	if res.Status != StatusAccepted || res.Method != MethodBatch {
		t.Fatalf("valid batch: status=%s method=%s", res.Status, res.Method)
	}
}

// TestMultipleInvalidBisection 在多叶子批次中放置多个坏签名，验证多层二分。
func TestMultipleInvalidBisection(t *testing.T) {
	const n = 8
	items := make([]Item, n)
	for i := 0; i < n; i++ {
		priv, pub, _ := GenerateKey(nil)
		items[i] = mustSign(t, priv, pub, []byte{byte(i)})
	}
	items[1].S = addQToScalar(items[1].S)
	items[6].S = addQToScalar(items[6].S)

	want := referenceInvalid(t, items)
	if !reflect.DeepEqual(want, []int{1, 6}) {
		t.Fatalf("reference invalid = %v", want)
	}

	res, err := VerifyBatch(items, deterministicSeed{value: seedFromByte(0x44)})
	if err != nil {
		t.Fatalf("VerifyBatch: %v", err)
	}
	if res.Status != StatusRejected || !reflect.DeepEqual(res.Invalid, want) {
		t.Fatalf("status=%s invalid=%v want=%v", res.Status, res.Invalid, want)
	}
}

// TestDuplicateRejection 验证完整 (公钥,R,s,消息) 重复被拒绝并给出全部重复索引。
func TestDuplicateRejection(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	dup := mustSign(t, priv, pub, []byte("dup-msg"))
	otherPriv, otherPub, _ := GenerateKey(nil)
	other := mustSign(t, otherPriv, otherPub, []byte("other-msg"))

	items := []Item{dup, other, dup} // 索引 0 与 2 完全重复
	res, err := VerifyBatch(items, HostSeedProvider{})
	if err != nil {
		t.Fatalf("VerifyBatch: %v", err)
	}
	if res.Status != StatusDuplicates {
		t.Fatalf("status=%s, want duplicates-rejected", res.Status)
	}
	if !reflect.DeepEqual(res.Duplicates, []int{0, 2}) {
		t.Fatalf("duplicates=%v want [0 2]", res.Duplicates)
	}
	if res.SeedDigest != nil {
		t.Fatal("duplicate rejection must not draw a seed")
	}
	if res.TranscriptDigest == nil {
		t.Fatal("transcript digest should still be recorded for audit")
	}

	// 仅消息不同不算重复，应进入正常批量流程。
	nonDup := dup
	nonDup.Msg = append(append([]byte(nil), dup.Msg...), 0x01)
	res2, err := VerifyBatch([]Item{dup, nonDup}, HostSeedProvider{})
	if err != nil {
		t.Fatalf("non-duplicate batch: %v", err)
	}
	if res2.Status == StatusDuplicates {
		t.Fatal("distinct messages must not be treated as duplicates")
	}
}

// TestSeedMissingFallback 验证种子来源缺失/出错时整批退回逐份验证。
func TestSeedMissingFallback(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	good := mustSign(t, priv, pub, []byte("ok"))
	bad := mustSign(t, priv, pub, []byte("bad-later"))
	bad.Msg = append(bad.Msg, 0x01)
	items := []Item{good, bad}
	want := referenceInvalid(t, items)

	// provider 为 nil
	res, err := VerifyBatch(items, nil)
	if err != nil {
		t.Fatalf("nil provider: %v", err)
	}
	if res.Status != StatusRejected || res.Method != MethodSingleFallback {
		t.Fatalf("nil provider: status=%s method=%s", res.Status, res.Method)
	}
	if !reflect.DeepEqual(res.Invalid, want) {
		t.Fatalf("nil provider invalid=%v want=%v", res.Invalid, want)
	}
	if res.SeedDigest != nil || res.Coefficients != nil {
		t.Fatal("fallback must not consume a seed or coefficients")
	}

	// provider 返回错误
	res, err = VerifyBatch(items, deterministicSeed{err: errSeedBoom})
	if err != nil {
		t.Fatalf("failing provider: %v", err)
	}
	if res.Method != MethodSingleFallback || !reflect.DeepEqual(res.Invalid, want) {
		t.Fatalf("failing provider: method=%s invalid=%v", res.Method, res.Invalid)
	}

	// 全部有效时退回逐份验证也应接受
	allGood := []Item{
		mustSign(t, priv, pub, []byte("a")),
		mustSign(t, priv, pub, []byte("b")),
	}
	res, err = VerifyBatch(allGood, nil)
	if err != nil {
		t.Fatalf("all-good fallback: %v", err)
	}
	if res.Status != StatusAccepted || res.Method != MethodSingleFallback {
		t.Fatalf("all-good fallback: status=%s method=%s", res.Status, res.Method)
	}
}

// TestDifferentSeedsChangeCoefficients 验证不同宿主种子派生不同系数与种子摘要，
// 但对确定无效集合的结论一致；转录摘要只取决于批次内容。
func TestDifferentSeedsChangeCoefficients(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	items := []Item{
		mustSign(t, priv, pub, []byte("one")),
		mustSign(t, priv, pub, []byte("two")),
	}
	items[0].S = addQToScalar(items[0].S)
	want := referenceInvalid(t, items)

	r1, err := VerifyBatch(items, deterministicSeed{value: seedFromByte(0x55)})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := VerifyBatch(items, deterministicSeed{value: seedFromByte(0x66)})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(r1.Coefficients, r2.Coefficients) {
		t.Fatal("different seeds must yield different coefficients")
	}
	if bytes.Equal(r1.SeedDigest, r2.SeedDigest) {
		t.Fatal("different seeds must yield different seed digests")
	}
	if !bytes.Equal(r1.TranscriptDigest, r2.TranscriptDigest) {
		t.Fatal("same batch content must yield the same transcript digest")
	}
	if !reflect.DeepEqual(r1.Invalid, want) || !reflect.DeepEqual(r2.Invalid, want) {
		t.Fatalf("invalid sets drift: r1=%v r2=%v want=%v", r1.Invalid, r2.Invalid, want)
	}
}

// TestBatchLimitsAndMalformed 覆盖批次大小限制与条目格式错误。
func TestBatchLimitsAndMalformed(t *testing.T) {
	if _, err := VerifyBatch(nil, HostSeedProvider{}); err != errEmptyBatch {
		t.Fatalf("empty batch err=%v", err)
	}

	priv, pub, _ := GenerateKey(nil)
	base := mustSign(t, priv, pub, []byte("x"))
	oversize := make([]Item, MaxBatchSize+1)
	for i := range oversize {
		oversize[i] = base
	}
	if _, err := VerifyBatch(oversize, HostSeedProvider{}); err != errEmptyBatch {
		t.Fatalf("oversize err=%v", err)
	}

	badPoint := append([]byte(nil), base.Pub...)
	badPoint[0] = 0x02 // 非 0x04 非压缩前缀
	cases := []Item{
		{Pub: badPoint, R: base.R, S: base.S, Msg: base.Msg},
		{Pub: base.Pub, R: append([]byte(nil), base.R...), S: make([]byte, 32), Msg: base.Msg}, // s=0
		{Pub: base.Pub, R: base.R, S: append([]byte(nil), base.S[:31]...), Msg: base.Msg},      // 长度不足
	}
	for i, it := range cases {
		_, err := VerifyBatch([]Item{it}, HostSeedProvider{})
		if err == nil {
			t.Fatalf("malformed case %d expected error", i)
		}
		ie, ok := err.(*ItemError)
		if !ok || ie.Index != 0 {
			t.Fatalf("malformed case %d got %T %v", i, err, err)
		}
	}
}

// TestCanonicalPointRejection 验证压缩编码与曲线外点等非规范点被拒绝。
func TestCanonicalPointRejection(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	item := mustSign(t, priv, pub, []byte("canon"))

	// 压缩前缀 0x02
	compressed := append([]byte{0x02}, pub[1:33]...)
	if _, _, err := decodePoint(compressed); err == nil {
		t.Fatal("compressed point must be rejected")
	}
	// 曲线外点：x=1, y=0
	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	offCurve[1] = 1
	if _, _, err := decodePoint(offCurve); err == nil {
		t.Fatal("off-curve point must be rejected")
	}
	if _, err := VerifySingle(pub, compressed, item.S, item.Msg); err == nil {
		t.Fatal("single verify must reject compressed R/P encoding")
	}
}

// TestDeriveCoefficientsSpec 直接审计系数 KDF：确定性、非零、
// 按规格拼接输入，且不同索引/种子相互区分。
func TestDeriveCoefficientsSpec(t *testing.T) {
	seed := seedFromByte(0x77)
	transcript := []byte("canonical-transcript")
	n := 4

	c1 := deriveCoefficients(seed[:], transcript, n)
	c2 := deriveCoefficients(seed[:], transcript, n)
	for i := range c1 {
		if c1[i].Sign() == 0 || c1[i].Cmp(q) >= 0 {
			t.Fatalf("coefficient %d out of [1,q-1]", i)
		}
		if c1[i].Cmp(c2[i]) != 0 {
			t.Fatalf("coefficient derivation not deterministic at %d", i)
		}
		// 手工按规格重算 counter=0 的值
		var idx, ctr [4]byte
		binary.BigEndian.PutUint32(idx[:], uint32(i))
		h := sha256.New()
		h.Write([]byte(labelCoeff))
		h.Write(seed[:])
		h.Write(transcript)
		h.Write(idx[:])
		h.Write(ctr[:])
		want := new(big.Int).Mod(new(big.Int).SetBytes(h.Sum(nil)), q)
		if want.Sign() == 0 {
			t.Fatalf("test vector unexpectedly zero at %d", i)
		}
		if want.Cmp(c1[i]) != 0 {
			t.Fatalf("coefficient %d does not match spec hash construction", i)
		}
	}

	seed88 := seedFromByte(0x88)
	other := deriveCoefficients(seed88[:], transcript, n)
	for i := range c1 {
		if c1[i].Cmp(other[i]) == 0 {
			t.Fatalf("coefficient %d insensitive to seed", i)
		}
	}

	// 直接覆盖“遇 0 递增计数器”分支：前两个计数器返回 0，第三个返回 7。
	calls := 0
	v, counter := firstNonZero(0, func(c uint32) *big.Int {
		calls++
		if c < 2 {
			return big.NewInt(0)
		}
		return big.NewInt(7)
	})
	if v.Cmp(big.NewInt(7)) != 0 || counter != 2 || calls != 3 {
		t.Fatalf("bump logic: v=%v counter=%d calls=%d", v, counter, calls)
	}

	// 跨多组种子，派生系数必须始终非零且在域内。
	for b := 0; b < 64; b++ {
		sb := seedFromByte(byte(b))
		coeffs := deriveCoefficients(sb[:], transcript, 8)
		for _, c := range coeffs {
			if c.Sign() == 0 || c.Cmp(q) >= 0 {
				t.Fatalf("seed %d produced invalid coefficient", b)
			}
		}
	}
}

// TestTranscriptIncludesLengths 验证转录对消息长度敏感（防止长度混淆）。
func TestTranscriptIncludesLengths(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	item := mustSign(t, priv, pub, []byte("ab"))
	one := parseForTest(t, []Item{item})

	otherItem := item
	otherItem.Msg = []byte("a")
	two := parseForTest(t, []Item{otherItem})

	d1 := sha256.Sum256(buildTranscript(one))
	d2 := sha256.Sum256(buildTranscript(two))
	if bytes.Equal(d1[:], d2[:]) {
		t.Fatal("transcript must distinguish different message lengths")
	}
}

// TestSingleItemBatch 验证 n=1 的边界批次走正常批量等式路径并与单验一致。
func TestSingleItemBatch(t *testing.T) {
	priv, pub, _ := GenerateKey(nil)
	item := mustSign(t, priv, pub, []byte("solo"))

	res, err := VerifyBatch([]Item{item}, HostSeedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusAccepted || res.Method != MethodBatch {
		t.Fatalf("valid single batch: status=%s method=%s", res.Status, res.Method)
	}
	if len(res.Coefficients) != 1 || res.Coefficients[0] == [32]byte{} {
		t.Fatal("single-item batch must still derive a non-zero coefficient")
	}

	bad := item
	bad.Msg = append(append([]byte(nil), item.Msg...), 0x02)
	res, err = VerifyBatch([]Item{bad}, HostSeedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || !reflect.DeepEqual(res.Invalid, []int{0}) {
		t.Fatalf("invalid single batch: status=%s invalid=%v", res.Status, res.Invalid)
	}
	if res.Method != MethodBisection {
		t.Fatalf("leaf must be resolved by single verify, got method=%s", res.Method)
	}
}

// BenchmarkVerify256 给出 256 项全有效批次的性能基线。
func BenchmarkVerify256(b *testing.B) {
	items := make([]Item, MaxBatchSize)
	for i := range items {
		priv, pub, _ := GenerateKey(nil)
		items[i] = mustSign(b, priv, pub, []byte{byte(i >> 8), byte(i)})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := VerifyBatch(items, HostSeedProvider{})
		if err != nil || res.Status != StatusAccepted {
			b.Fatalf("unexpected status=%s err=%v", res.Status, err)
		}
	}
}
