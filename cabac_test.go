package cabac

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestTables 守护手工录入的 H.264 概率状态表的关键性质。
func TestTables(t *testing.T) {
	if got := rangeTabLPS[0]; got != [4]uint16{128, 176, 208, 240} {
		t.Fatalf("rangeTabLPS[0] = %v", got)
	}
	if got := rangeTabLPS[63]; got != [4]uint16{4, 5, 6, 6} {
		t.Fatalf("rangeTabLPS[63] = %v", got)
	}
	for s := 0; s < 64; s++ {
		for q := 0; q < 4; q++ {
			v := rangeTabLPS[s][q]
			if v < 4 || v > 240 {
				t.Fatalf("rangeTabLPS[%d][%d] = %d 超出合法区间", s, q, v)
			}
			if s < 63 && rangeTabLPS[s][q] < rangeTabLPS[s+1][q] {
				t.Fatalf("rangeTabLPS 第 %d 列在状态 %d 处非单调递减", q, s)
			}
		}
	}
	if transIdxLPS[0] != 0 || transIdxLPS[62] != 63 || transIdxLPS[63] != 63 {
		t.Fatalf("transIdxLPS 端点异常: %d %d %d", transIdxLPS[0], transIdxLPS[62], transIdxLPS[63])
	}
	for s := 0; s < 63; s++ {
		if transIdxLPS[s] > transIdxLPS[s+1] {
			t.Fatalf("transIdxLPS 在 %d 处非单调", s)
		}
	}
	for s := uint8(0); s < 63; s++ {
		if transIdxMPS(s) != s+1 {
			t.Fatalf("transIdxMPS(%d) = %d", s, transIdxMPS(s))
		}
	}
	if transIdxMPS(63) != 63 {
		t.Fatal("transIdxMPS(63) 应饱和于 63")
	}
}

// TestRoundTrip 对各种规模与形态的指令序列做编码-解码一致性校验。
func TestRoundTrip(t *testing.T) {
	var allOps []Instr
	for op := OpCode(0); op < opCount; op++ {
		for o := uint8(0); o < 16; o++ {
			if !op.hasOperand() && o != 0 {
				continue // NOP 不携带操作数
			}
			allOps = append(allOps, Instr{Op: op, Operand: o})
		}
	}
	cases := map[string][]Instr{
		"空序列":      {},
		"单条NOP":    {{Op: OpNop}},
		"单条带操作数":   {{Op: OpCall, Operand: 15}},
		"全操作码遍历":   allOps,
		"长NOP串":    repeat(Instr{Op: OpNop}, 100000),
		"结构化轨迹1k":  GenerateTrace(1000, 7),
		"结构化轨迹50k": GenerateTrace(50000, 99),
	}
	for name, trace := range cases {
		data, _ := EncodeTrace(trace, 1)
		got := DecodeTrace(data, len(trace))
		if !reflect.DeepEqual(got, trace) {
			t.Fatalf("%s: 往返不一致（%d 条指令，%d 字节）", name, len(trace), len(data))
		}
	}
}

func repeat(instr Instr, n int) []Instr {
	out := make([]Instr, n)
	for i := range out {
		out[i] = instr
	}
	return out
}

// TestRandomBinsRoundTrip 在裸 bin 层面用多个上下文做高强度随机往返，
// 压力覆盖长跟随比特链与频繁状态翻转。
func TestRandomBinsRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	const n = 300000
	bins := make([]int, n)
	ctxIdx := make([]int, n)
	enc := NewEncoder()
	var ctxs [4]Context
	for i := 0; i < n; i++ {
		c := rng.Intn(4)
		b := 0
		if rng.Float64() < 0.1 {
			b = 1
		}
		bins[i], ctxIdx[i] = b, c
		enc.EncodeBin(&ctxs[c], b)
	}
	enc.Flush()
	data := enc.Bytes()

	dec := NewDecoder(data)
	var dctxs [4]Context
	for i := 0; i < n; i++ {
		if got := dec.DecodeBin(&dctxs[ctxIdx[i]]); got != bins[i] {
			t.Fatalf("第 %d 个 bin 解码为 %d，应为 %d", i, got, bins[i])
		}
	}
}

// TestBypassRoundTrip 校验固定概率（静态）通路的往返一致性。
func TestBypassRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 50000
	bins := make([]int, n)
	enc := NewEncoder()
	for i := range bins {
		bins[i] = rng.Intn(2)
		enc.EncodeBinBypass(bins[i])
	}
	enc.Flush()
	dec := NewDecoder(enc.Bytes())
	for i, want := range bins {
		if got := dec.DecodeBinBypass(); got != want {
			t.Fatalf("第 %d 个 bypass bin 解码为 %d，应为 %d", i, got, want)
		}
	}
}

// TestConvergence 验证验收核心：随着编码推进，高频指令的有效编码
// 位长单调收敛到 0.2 比特附近。
func TestConvergence(t *testing.T) {
	const n = 200000
	trace := GenerateTrace(n, 2024)
	data, rates := EncodeTrace(trace, 8)

	if got := DecodeTrace(data, n); !reflect.DeepEqual(got, trace) {
		t.Fatal("往返不一致")
	}
	t.Logf("各窗口 比特/指令: %.3f -> %.3f -> %.3f -> %.3f -> %.3f -> %.3f -> %.3f -> %.3f",
		rates[0], rates[1], rates[2], rates[3], rates[4], rates[5], rates[6], rates[7])
	overall := float64(len(data)*8) / n
	t.Logf("整体码率 %.3f 比特/指令（%d 字节 / %d 条指令）", overall, len(data), n)

	first, last := rates[0], rates[len(rates)-1]
	if last >= first {
		t.Errorf("未观察到收敛：首窗口 %.3f，末窗口 %.3f", first, last)
	}
	if last > 0.25 {
		t.Errorf("高频指令有效位长收敛值 %.3f 超过 0.25 比特（目标 ≈0.2）", last)
	}
	if overall > 0.25 {
		t.Errorf("整体码率 %.3f 超过 0.25 比特/指令", overall)
	}
}

// TestCompressionVsBaselines 验证验收核心：CABAC 大幅领先无上下文模型。
func TestCompressionVsBaselines(t *testing.T) {
	const n = 100000
	trace := GenerateTrace(n, 7)
	cabacData, _ := EncodeTrace(trace, 1)
	single := EncodeInstrsSingleContext(trace)
	static := EncodeInstrsStatic(trace)
	fixed := FixedLengthBits(n) / 8

	cabacSize := len(cabacData)
	t.Logf("CABAC            : %7d 字节 (%.3f 比特/指令)", cabacSize, float64(cabacSize*8)/n)
	t.Logf("单上下文自适应    : %7d 字节 (%.3f 比特/指令)", len(single), float64(len(single)*8)/n)
	t.Logf("静态0.5模型      : %7d 字节 (%.3f 比特/指令)", len(static), float64(len(static)*8)/n)
	t.Logf("定长编码          : %7d 字节 (%.3f 比特/指令)", fixed, float64(fixed*8)/n)

	if cabacSize*2 >= len(single) {
		t.Errorf("CABAC(%d) 未比单上下文自适应模型(%d) 节省 50%% 以上", cabacSize, len(single))
	}
	if cabacSize*4 >= len(static) {
		t.Errorf("CABAC(%d) 未比静态模型(%d) 节省 75%% 以上", cabacSize, len(static))
	}
	if cabacSize*10 >= fixed {
		t.Errorf("CABAC(%d) 未比定长编码(%d) 节省 90%% 以上", cabacSize, fixed)
	}
}
