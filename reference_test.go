package acx

import (
	"encoding/binary"
	"math"
	"math/big"
	"testing"
)

// 高精度有理数参考实现：不做归一化、不做整数舍入，
// 在精确实数区间 [lo, hi) 上按同一模型切分，用于独立核对整数编码器的输出。

type ratCtx struct{ zero, one int64 }

func ratUpdate(c *ratCtx, bit uint8) {
	if bit == 0 {
		c.zero++
	} else {
		c.one++
	}
	if c.zero+c.one == maxTotal {
		c.zero = (c.zero + 1) / 2
		c.one = (c.one + 1) / 2
	}
}

func newRatCtxs(n int) []ratCtx {
	cs := make([]ratCtx, n)
	for i := range cs {
		cs[i] = ratCtx{1, 1}
	}
	return cs
}

// refInterval 计算事件序列对应的精确有理区间 [lo, hi)。
func refInterval(events []Event, numCtx int) (lo, hi *big.Rat) {
	ctxs := newRatCtxs(numCtx)
	lo, hi = big.NewRat(0, 1), big.NewRat(1, 1)
	for _, ev := range events {
		c := &ctxs[ev.Label]
		total := c.zero + c.one
		rng := new(big.Rat).Sub(hi, lo)
		bound := new(big.Rat).Mul(rng, big.NewRat(c.zero, total))
		bound.Add(bound, lo)
		if ev.Bit == 0 {
			hi = bound
		} else {
			lo = bound
		}
		ratUpdate(c, ev.Bit)
	}
	return lo, hi
}

// refDecode 用精确有理算术从码值 v 解出 n 个位（标签序列已知）。
func refDecode(v *big.Rat, labels []uint8, numCtx int) []uint8 {
	ctxs := newRatCtxs(numCtx)
	lo, hi := big.NewRat(0, 1), big.NewRat(1, 1)
	bits := make([]uint8, len(labels))
	for i, lb := range labels {
		c := &ctxs[lb]
		total := c.zero + c.one
		rng := new(big.Rat).Sub(hi, lo)
		bound := new(big.Rat).Mul(rng, big.NewRat(c.zero, total))
		bound.Add(bound, lo)
		if v.Cmp(bound) < 0 {
			bits[i] = 0
			hi = bound
		} else {
			bits[i] = 1
			lo = bound
		}
		ratUpdate(c, bits[i])
	}
	return bits
}

// streamValue 把载荷字节解释为二进制小数 0.xxxxx 的精确有理值。
func streamValue(payload []byte) *big.Rat {
	num := new(big.Int).SetBytes(payload)
	den := new(big.Int).Lsh(big.NewInt(1), uint(8*len(payload)))
	return new(big.Rat).SetFrac(num, den)
}

// framePayload 从帧中取出算术编码载荷（测试辅助）。
func framePayload(t *testing.T, frame []byte) []byte {
	t.Helper()
	pos := 4
	count, n := binary.Uvarint(frame[pos:])
	pos += n
	if _, n = binary.Uvarint(frame[pos:]); n <= 0 {
		t.Fatalf("bad frame")
	}
	pos += n
	payloadLen, n := binary.Uvarint(frame[pos:])
	pos += n
	pos += int(count) // labels
	pos += 4          // crc
	payload := frame[pos:]
	if uint64(len(payload)) != payloadLen {
		t.Fatalf("payload length mismatch")
	}
	return payload
}

func TestRationalReference(t *testing.T) {
	cases := []struct {
		name   string
		numCtx int
		events []Event
	}{
		{"单比特0", 1, []Event{{0, 0}}},
		{"单比特1", 1, []Event{{0, 1}}},
		{"全0×8", 1, repeat(0, 0, 8)},
		{"全1×8", 1, repeat(0, 1, 8)},
		{"交替01×16单上下文", 1, alternate(0, 16)},
		{"交替双上下文×16", 2, alternate2(16)},
		{"偏斜1/8×24", 1, skewed(8, 24)},
		{"偏斜1/31×40双上下文", 2, skewed2(31, 40)},
		{"固定模式0110110011", 1, pattern([]uint8{0, 1, 1, 0, 1, 1, 0, 0, 1, 1})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := encodeAll(t, tc.numCtx, tc.events)
			payload := framePayload(t, frame)

			// 1) 精确有理区间 [lo, hi)
			lo, hi := refInterval(tc.events, tc.numCtx)

			// 2) 整数编码器输出的码值
			v := streamValue(payload)

			// 3) 码值必须落在精确区间内
			if v.Cmp(lo) < 0 || v.Cmp(hi) > 0 {
				t.Fatalf("code value %v outside rational interval [%v, %v]\nframe=%s",
					v.FloatString(40), lo.FloatString(40), hi.FloatString(40), hexDump(frame))
			}

			// 4) 用有理参考解码器从码值还原位序列，必须与原序列一致
			labels := make([]uint8, len(tc.events))
			want := make([]uint8, len(tc.events))
			for i, ev := range tc.events {
				labels[i] = ev.Label
				want[i] = ev.Bit
			}
			got := refDecode(v, labels, tc.numCtx)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("rational decode mismatch at %d: got %d, want %d", i, got[i], want[i])
				}
			}
			t.Logf("%s: %d 事件, 区间宽度 2^%.2f, 载荷 %d 字节",
				tc.name, len(tc.events), log2Rat(new(big.Rat).Sub(hi, lo)), len(payload))
		})
	}
}

func log2Rat(r *big.Rat) float64 {
	f, _ := r.Float64()
	if f <= 0 {
		return 0
	}
	return math.Log2(f)
}

func repeat(label, bit uint8, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		ev[i] = Event{label, bit}
	}
	return ev
}

func alternate(label uint8, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		ev[i] = Event{label, uint8(i % 2)}
	}
	return ev
}

func alternate2(n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		ev[i] = Event{uint8(i % 2), uint8(i % 2)}
	}
	return ev
}

func skewed(period, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		b := uint8(0)
		if i%period == 0 {
			b = 1
		}
		ev[i] = Event{0, b}
	}
	return ev
}

func skewed2(period, n int) []Event {
	ev := make([]Event, n)
	for i := range ev {
		lb := uint8(i % 2)
		b := uint8(0)
		if i%period == 0 {
			b = 1
		}
		ev[i] = Event{lb, b}
	}
	return ev
}

func pattern(bits []uint8) []Event {
	ev := make([]Event, len(bits))
	for i, b := range bits {
		ev[i] = Event{0, b}
	}
	return ev
}
