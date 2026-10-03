// Package arithmetic 实现基于概率分布的算术编码（Arithmetic Coding）。
//
// 编码器以大整数（math/big.Int）有理数维护当前状态区间 [Low, High)，
// 区间相对 2^precisionBits 缩放。每编码一个符号，根据累计概率分布
// （CDF）对区间做精确有理数细分；随后执行重归一化（E1/E2/E3 缩放），
// 把已确定的最高位溢写进位流，并用 pending 计数处理下溢（underflow）。
// 压缩率逼近香农熵极限。
package arithmetic

import (
	"encoding/binary"
	"math/big"
)

// precisionBits 为区间定点精度。只要远大于模型总频数即可保证
// 每个符号细分后区间非空，编解码两端算术完全一致。
const precisionBits = 62

var (
	one           = big.NewInt(1)
	fullRange     = new(big.Int).Lsh(one, precisionBits)   // 2^p
	halfRange     = new(big.Int).Lsh(one, precisionBits-1) // 2^(p-1)
	quarterRange  = new(big.Int).Lsh(one, precisionBits-2) // 2^(p-2)
	threeQuarters = new(big.Int).Mul(big.NewInt(3), quarterRange)
)

// Model 是静态概率模型：按累计频率（CDF）细分区间。
type Model struct {
	cum   []uint64 // cum[i] = 符号 < i 的累计频率，len = nSym+1
	total uint64
}

// NewModel 由每个符号的频率构建模型。频率必须非负且总和 > 0。
func NewModel(freqs []uint64) *Model {
	cum := make([]uint64, len(freqs)+1)
	for i, f := range freqs {
		cum[i+1] = cum[i] + f
	}
	if cum[len(freqs)] == 0 {
		panic("arithmetic: total frequency must be > 0")
	}
	return &Model{cum: cum, total: cum[len(freqs)]}
}

// NumSymbols 返回模型中的符号个数。
func (m *Model) NumSymbols() int { return len(m.cum) - 1 }

// Total 返回模型总频率。
func (m *Model) Total() uint64 { return m.total }

// findSymbol 返回满足 cum[s] <= v < cum[s+1] 的符号 s（二分查找）。
func (m *Model) findSymbol(v uint64) int {
	lo, hi := 0, len(m.cum)-1
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if m.cum[mid] <= v {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// ---------------------------------------------------------------------------
// 位流 I/O（MSB 优先）
// ---------------------------------------------------------------------------

type bitWriter struct {
	buf   []byte
	cur   byte
	nbits uint
}

func (w *bitWriter) writeBit(b uint) {
	w.cur <<= 1
	if b != 0 {
		w.cur |= 1
	}
	w.nbits++
	if w.nbits == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.nbits = 0
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nbits > 0 {
		w.cur <<= 8 - w.nbits
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.nbits = 0
	}
	return w.buf
}

type bitReader struct {
	data  []byte
	pos   int  // 字节位置
	nbits uint // 当前字节已消费位数
}

func (r *bitReader) readBit() uint {
	if r.pos >= len(r.data) {
		return 0 // 流尽后补 0
	}
	b := (r.data[r.pos] >> (7 - r.nbits)) & 1
	r.nbits++
	if r.nbits == 8 {
		r.pos++
		r.nbits = 0
	}
	return uint(b)
}

// ---------------------------------------------------------------------------
// 编码器
// ---------------------------------------------------------------------------

type encoder struct {
	w       *bitWriter
	low     *big.Int // 区间 [low, high)，相对 2^precisionBits 缩放
	high    *big.Int
	pending uint64 // E3 下溢挂起位计数
	tmp     *big.Int
}

func newEncoder(w *bitWriter) *encoder {
	return &encoder{
		w:    w,
		low:  new(big.Int),
		high: new(big.Int).Set(fullRange),
		tmp:  new(big.Int),
	}
}

// emit 输出一个位，并冲刷所有挂起的下溢位（取反）。
func (e *encoder) emit(bit uint) {
	e.w.writeBit(bit)
	for ; e.pending > 0; e.pending-- {
		e.w.writeBit(bit ^ 1)
	}
}

// encodeSymbol 按模型细分区间：设 range = high-low，则
//
//	[low, high) <- [low + range*cum[s]/total, low + range*cum[s+1]/total)
func (e *encoder) encodeSymbol(m *Model, sym int) {
	rng := new(big.Int).Sub(e.high, e.low)

	// high = low + range*cum[sym+1]/total
	e.tmp.Mul(rng, new(big.Int).SetUint64(m.cum[sym+1]))
	e.tmp.Div(e.tmp, new(big.Int).SetUint64(m.total))
	newHigh := new(big.Int).Add(e.low, e.tmp)

	// low = low + range*cum[sym]/total
	e.tmp.Mul(rng, new(big.Int).SetUint64(m.cum[sym]))
	e.tmp.Div(e.tmp, new(big.Int).SetUint64(m.total))
	e.low.Add(e.low, e.tmp)

	e.high = newHigh
	e.renormalize()
}

// renormalize 执行 E1/E2/E3 重归一化，把已收敛的最高位写入位流。
func (e *encoder) renormalize() {
	for {
		if e.high.Cmp(halfRange) <= 0 {
			// E1：区间整体落在下半区 [0, 1/2)
			e.emit(0)
		} else if e.low.Cmp(halfRange) >= 0 {
			// E2：区间整体落在上半区 [1/2, 1)
			e.emit(1)
			e.low.Sub(e.low, halfRange)
			e.high.Sub(e.high, halfRange)
		} else if e.low.Cmp(quarterRange) >= 0 && e.high.Cmp(threeQuarters) <= 0 {
			// E3：区间跨越中点但落在 [1/4, 3/4)，记下溢挂起位
			e.pending++
			e.low.Sub(e.low, quarterRange)
			e.high.Sub(e.high, quarterRange)
		} else {
			return
		}
		e.low.Lsh(e.low, 1)
		e.high.Lsh(e.high, 1)
	}
}

// flush 终止编码：输出足以唯一确定区间内一点的位。
func (e *encoder) flush() {
	e.pending++
	if e.low.Cmp(quarterRange) < 0 {
		e.emit(0)
	} else {
		e.emit(1)
	}
}

// ---------------------------------------------------------------------------
// 解码器
// ---------------------------------------------------------------------------

type decoder struct {
	r    *bitReader
	low  *big.Int
	high *big.Int
	code *big.Int // 当前窗口内已读入的位流值
	tmp  *big.Int
}

func newDecoder(r *bitReader) *decoder {
	d := &decoder{
		r:    r,
		low:  new(big.Int),
		high: new(big.Int).Set(fullRange),
		code: new(big.Int),
		tmp:  new(big.Int),
	}
	for i := 0; i < precisionBits; i++ {
		d.code.Lsh(d.code, 1)
		d.code.Add(d.code, big.NewInt(int64(r.readBit())))
	}
	return d
}

// decodeSymbol 反解一个符号：先算出缩放值
//
//	v = floor(((code - low + 1) * total - 1) / range)
//
// 再在 CDF 中二分定位符号，然后与编码器完全一致地细分区间。
func (d *decoder) decodeSymbol(m *Model) int {
	rng := new(big.Int).Sub(d.high, d.low)

	// v = ((code-low+1)*total - 1) / range
	v := new(big.Int).Sub(d.code, d.low)
	v.Add(v, one)
	v.Mul(v, new(big.Int).SetUint64(m.total))
	v.Sub(v, one)
	v.Div(v, rng)

	sym := m.findSymbol(v.Uint64())

	// 与编码器相同的区间细分
	d.tmp.Mul(rng, new(big.Int).SetUint64(m.cum[sym+1]))
	d.tmp.Div(d.tmp, new(big.Int).SetUint64(m.total))
	newHigh := new(big.Int).Add(d.low, d.tmp)

	d.tmp.Mul(rng, new(big.Int).SetUint64(m.cum[sym]))
	d.tmp.Div(d.tmp, new(big.Int).SetUint64(m.total))
	d.low.Add(d.low, d.tmp)

	d.high = newHigh
	d.renormalize()
	return sym
}

// renormalize 与编码器镜像，同步从位流读入新位。
func (d *decoder) renormalize() {
	for {
		if d.high.Cmp(halfRange) <= 0 {
			// E1
		} else if d.low.Cmp(halfRange) >= 0 {
			// E2
			d.low.Sub(d.low, halfRange)
			d.high.Sub(d.high, halfRange)
			d.code.Sub(d.code, halfRange)
		} else if d.low.Cmp(quarterRange) >= 0 && d.high.Cmp(threeQuarters) <= 0 {
			// E3
			d.low.Sub(d.low, quarterRange)
			d.high.Sub(d.high, quarterRange)
			d.code.Sub(d.code, quarterRange)
		} else {
			return
		}
		d.low.Lsh(d.low, 1)
		d.high.Lsh(d.high, 1)
		d.code.Lsh(d.code, 1)
		d.code.Add(d.code, big.NewInt(int64(d.r.readBit())))
	}
}

// ---------------------------------------------------------------------------
// 对外 API
// ---------------------------------------------------------------------------

// Encode 用给定模型压缩符号序列，返回自包含的字节流：
// 8 字节大端符号数头 + 算术编码位流。解码端需持有同一模型。
func Encode(symbols []byte, m *Model) []byte {
	w := &bitWriter{}
	enc := newEncoder(w)
	for _, s := range symbols {
		enc.encodeSymbol(m, int(s))
	}
	enc.flush()

	payload := w.bytes()
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint64(out, uint64(len(symbols)))
	return append(out, payload...)
}

// Decode 是 Encode 的逆过程：按同一模型从累计概率分布精确反解，
// 依据头部符号数完美终止。
func Decode(data []byte, m *Model) []byte {
	n := binary.BigEndian.Uint64(data[:8])
	r := &bitReader{data: data[8:]}
	dec := newDecoder(r)
	out := make([]byte, 0, n)
	for i := uint64(0); i < n; i++ {
		out = append(out, byte(dec.decodeSymbol(m)))
	}
	return out
}
