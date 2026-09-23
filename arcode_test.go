package arcode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

const testBudget = 1 << 30

func roundTrip(t *testing.T, data []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, data); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(&buf, testBudget)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("往返不一致: 原始 %d 字节, 解码 %d 字节", len(data), len(got))
	}
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	random64K := make([]byte, 64*1024)
	rng.Read(random64K)

	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}

	text := []byte("the quick brown fox jumps over the lazy dog. ")
	repeated := bytes.Repeat(text, 500)

	cases := map[string][]byte{
		"空数据":    {},
		"单字节":    {'x'},
		"短文本":    []byte("hello, arithmetic coding!"),
		"全部字节值":  allBytes,
		"重复文本":   repeated,
		"伪随机64K": random64K,
		"交替两符号":  bytes.Repeat([]byte("ab"), 20000),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, data) })
	}
}

// 长单一符号流：验证长时间运行区间不塌陷、不溢出，且能正确往返。
func TestLongSingleSymbolStream(t *testing.T) {
	data := bytes.Repeat([]byte{0x00}, 2<<20) // 2 MiB 全零
	var buf bytes.Buffer
	if err := Encode(&buf, data); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() >= len(data) {
		t.Fatalf("全零流未被压缩: 输入 %d, 输出 %d", len(data), buf.Len())
	}
	compressed := buf.Len()
	got, err := Decode(&buf, testBudget)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("长单一符号流往返不一致")
	}
	t.Logf("2 MiB 全零压缩到 %d 字节", compressed)
}

// 缩放后各已知符号保持正频率，且总频率不超过上限。
func TestModelScalingKeepsPositiveFreq(t *testing.T) {
	m := newModel()
	known := make(map[int]bool)
	rng := rand.New(rand.NewSource(7))
	// 偏斜分布：大量更新少数符号，触发多次缩放。
	for i := 0; i < 200000; i++ {
		s := 0
		if rng.Intn(10) == 0 {
			s = rng.Intn(numSymbols)
		}
		m.update(s)
		known[s] = true
		if m.total > maxTotalFreq {
			t.Fatalf("总频率 %d 超过上限 %d", m.total, maxTotalFreq)
		}
	}
	for s := range known {
		if m.freq[s] == 0 {
			t.Fatalf("已知符号 %d 缩放后频率为 0", s)
		}
	}
	var sum uint32
	for _, f := range m.freq {
		sum += f
	}
	if sum != m.total {
		t.Fatalf("频率和 %d 与 total %d 不一致", sum, m.total)
	}
}

// 偏斜分布大流往返：强制编解码双方多次同步缩放。
func TestSkewedStreamRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	data := make([]byte, 300000)
	for i := range data {
		if rng.Intn(16) == 0 {
			data[i] = byte(rng.Intn(256))
		} else {
			data[i] = 'A'
		}
	}
	roundTrip(t, data)
}

func encodeToBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, data); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

func TestBudgetEnforced(t *testing.T) {
	data := bytes.Repeat([]byte("budget"), 200)
	stream := encodeToBytes(t, data)
	_, err := Decode(bytes.NewReader(stream), 10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("期望 ErrTooLarge, 得到 %v", err)
	}
	// 预算刚好够时应当成功。
	got, err := Decode(bytes.NewReader(stream), uint64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("预算足够时解码失败: %v", err)
	}
}

func TestBadMagicAndVersion(t *testing.T) {
	stream := encodeToBytes(t, []byte("hello"))

	bad := bytes.Clone(stream)
	bad[0] = 'X'
	if _, err := Decode(bytes.NewReader(bad), testBudget); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("期望 ErrBadMagic, 得到 %v", err)
	}

	bad = bytes.Clone(stream)
	bad[4] = 99
	if _, err := Decode(bytes.NewReader(bad), testBudget); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("期望 ErrBadVersion, 得到 %v", err)
	}
}

// 篡改负载：解码必须报错（EOF 缺失或 CRC 不符），不能静默产出错误数据。
func TestCorruptPayload(t *testing.T) {
	data := []byte("some meaningful payload that will be corrupted in transit")
	stream := encodeToBytes(t, data)
	for _, pos := range []int{headerLen, headerLen + 1, len(stream) / 2, len(stream) - 2} {
		bad := bytes.Clone(stream)
		bad[pos] ^= 0xFF
		if _, err := Decode(bytes.NewReader(bad), testBudget); err == nil {
			t.Fatalf("篡改偏移 %d 未被检出", pos)
		}
	}
}

// 截断流必须报错而非挂起。
func TestTruncatedStream(t *testing.T) {
	data := bytes.Repeat([]byte("truncate me "), 100)
	stream := encodeToBytes(t, data)
	for _, n := range []int{0, 5, headerLen - 1, headerLen, len(stream) - 1} {
		if _, err := Decode(bytes.NewReader(stream[:n]), testBudget); err == nil {
			t.Fatalf("截断到 %d 字节未被检出", n)
		}
	}
}

// 篡改头部长度字段：必须报错。
func TestTamperedLength(t *testing.T) {
	data := []byte("length field integrity matters")
	stream := encodeToBytes(t, data)
	orig := binary.BigEndian.Uint64(stream[6:14])
	for _, length := range []uint64{orig - 1, orig + 1, orig + 100} {
		bad := bytes.Clone(stream)
		binary.BigEndian.PutUint64(bad[6:14], length)
		if _, err := Decode(bytes.NewReader(bad), testBudget); err == nil {
			t.Fatalf("长度篡改为 %d 未被检出", length)
		}
	}
}

// 纯垃圾输入：合法头部 + 随机负载，必须在有界时间内报错。
func TestGarbageStreamTerminates(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	stream := make([]byte, headerLen+4096)
	copy(stream[0:4], magic[:])
	stream[4] = FormatVersion
	binary.BigEndian.PutUint64(stream[6:14], 1000)
	rng.Read(stream[14:])
	if _, err := Decode(bytes.NewReader(stream), testBudget); err == nil {
		t.Fatal("垃圾流未被检出")
	}
}
