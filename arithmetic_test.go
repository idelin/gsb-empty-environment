package arithmetic

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

// genBernoulli 生成 p0 概率为 0 的二元符号流（确定性种子）。
func genBernoulli(n int, p0 float64, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	out := make([]byte, n)
	for i := range out {
		if r.Float64() < p0 {
			out[i] = 0
		} else {
			out[i] = 1
		}
	}
	return out
}

// binaryModel 由数据统计构建二元模型（拉普拉斯平滑，避免零频率）。
func binaryModel(data []byte) *Model {
	freqs := []uint64{1, 1}
	for _, s := range data {
		freqs[s]++
	}
	return NewModel(freqs)
}

// entropyBits 计算二元分布的香农熵（比特/符号）。
func entropyBits(p0 float64) float64 {
	p1 := 1 - p0
	h := 0.0
	if p0 > 0 {
		h -= p0 * math.Log2(p0)
	}
	if p1 > 0 {
		h -= p1 * math.Log2(p1)
	}
	return h
}

// TestRoundTripBinary 验收核心：90% 为 0 的二元流，编解码必须完全一致。
func TestRoundTripBinary(t *testing.T) {
	for _, n := range []int{1, 2, 7, 100, 1000, 100000} {
		data := genBernoulli(n, 0.9, 42)
		m := binaryModel(data)
		enc := Encode(data, m)
		dec := Decode(enc, m)
		if !bytes.Equal(dec, data) {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
	}
}

// TestBeatsHuffman 验收边界：输出字节数必须显著低于
// 单符号霍夫曼编码的理论下限（二元符号 = 1 bit/符号）。
func TestBeatsHuffman(t *testing.T) {
	n := 100000
	data := genBernoulli(n, 0.9, 7)
	m := binaryModel(data)
	enc := Encode(data, m)

	huffmanBytes := float64(n) / 8.0 // 霍夫曼下限：1 bit/符号
	ratio := float64(len(enc)) / huffmanBytes
	t.Logf("原始 %d 字节, 霍夫曼下限 %.0f 字节, 算术编码 %d 字节 (%.1f%%)",
		n, huffmanBytes, len(enc), ratio*100)
	if ratio >= 0.6 {
		t.Fatalf("未显著低于霍夫曼下限: 占比 %.2f%% >= 60%%", ratio*100)
	}
}

// TestNearEntropy 压缩率必须逼近香农熵极限。
func TestNearEntropy(t *testing.T) {
	n := 100000
	data := genBernoulli(n, 0.9, 99)
	m := binaryModel(data)
	enc := Encode(data, m)

	zeros := 0
	for _, s := range data {
		if s == 0 {
			zeros++
		}
	}
	entropy := entropyBits(float64(zeros) / float64(n))
	gotBits := float64(len(enc)-8) * 8 / float64(n)
	t.Logf("香农熵 %.4f bit/符号, 实际 %.4f bit/符号 (开销 %.2f%%)",
		entropy, gotBits, (gotBits/entropy-1)*100)
	if gotBits > entropy*1.05 {
		t.Fatalf("超出熵极限 5%%: got %.4f, entropy %.4f", gotBits, entropy)
	}
}

// TestRoundTripMultiSymbol 多符号（非二元）模型同样精确往返。
func TestRoundTripMultiSymbol(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	data := make([]byte, 50000)
	freqs := []uint64{1, 1, 1, 1}
	cum := []float64{0.70, 0.85, 0.95, 1.0}
	for i := range data {
		x := r.Float64()
		for s, c := range cum {
			if x < c {
				data[i] = byte(s)
				freqs[s]++
				break
			}
		}
	}
	m := NewModel(freqs)
	enc := Encode(data, m)
	if dec := Decode(enc, m); !bytes.Equal(dec, data) {
		t.Fatal("multi-symbol round trip mismatch")
	}
}

// TestEdgeCases 空输入、单符号、全同符号。
func TestEdgeCases(t *testing.T) {
	m := NewModel([]uint64{9, 1})
	for _, data := range [][]byte{
		{},
		{0},
		{1},
		bytes.Repeat([]byte{0}, 10000),
		bytes.Repeat([]byte{1}, 10000),
	} {
		if dec := Decode(Encode(data, m), m); !bytes.Equal(dec, data) {
			t.Fatalf("edge case mismatch (len=%d)", len(data))
		}
	}
}

// TestSkewedExtreme 极端偏斜 (99%) 时压缩率更高。
func TestSkewedExtreme(t *testing.T) {
	n := 100000
	data := genBernoulli(n, 0.99, 3)
	m := binaryModel(data)
	enc := Encode(data, m)
	bitsPerSym := float64(len(enc)-8) * 8 / float64(n)
	t.Logf("p(0)=0.99: %.4f bit/符号 (熵 %.4f)", bitsPerSym, entropyBits(0.99))
	if bitsPerSym > 0.12 {
		t.Fatalf("极端偏斜下压缩率不足: %.4f bit/符号", bitsPerSym)
	}
}
