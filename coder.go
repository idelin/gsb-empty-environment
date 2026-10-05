package acx

// 整数算术编码核心（Witten-Neal-Cleary 方案，自定义格式，不兼容任何视频标准）。
//
// 位宽与舍入规则（编码器与解码器必须严格一致）：
//   - 码值寄存器 low/high/value 为 32 位无符号整数，存于 uint64 中，恒小于 2^32。
//   - 中间乘积 rng*c0 最大为 2^32 * 4096 = 2^44，使用 64 位无符号整数计算，不溢出。
//   - 区间划分使用向下取整（floor）整数除法：bound = rng*c0/total。
//   - 因为归一化不变式保证 rng > 2^30 > total，所以 1 <= bound <= rng-1，
//     两个子区间都非空。
const (
	codeBits = 32
	topValue = uint64(1)<<codeBits - 1 // 0xFFFFFFFF
	firstQtr = uint64(1) << (codeBits - 2)
	half     = 2 * firstQtr
	thirdQtr = 3 * firstQtr
)

// bitWriter 按 MSB-first 顺序把位写入字节流。
type bitWriter struct {
	out   []byte
	cur   byte
	nbits uint8
}

func (w *bitWriter) write(bit uint64) {
	w.cur = w.cur<<1 | byte(bit&1)
	w.nbits++
	if w.nbits == 8 {
		w.out = append(w.out, w.cur)
		w.cur = 0
		w.nbits = 0
	}
}

// align 用 0 位补齐最后一个不完整字节。
func (w *bitWriter) align() {
	if w.nbits > 0 {
		w.cur <<= 8 - w.nbits
		w.out = append(w.out, w.cur)
		w.cur = 0
		w.nbits = 0
	}
}

// bitReader 按 MSB-first 顺序读取；读越界时返回 0 位（视为隐含的零填充）。
type bitReader struct {
	data []byte
	pos  int
	bit  uint8
}

func (r *bitReader) read() uint64 {
	if r.pos >= len(r.data) {
		return 0
	}
	b := uint64(r.data[r.pos]>>(7-r.bit)) & 1
	r.bit++
	if r.bit == 8 {
		r.bit = 0
		r.pos++
	}
	return b
}

// coreEncoder 是算术编码器的状态机。
type coreEncoder struct {
	low    uint64 // 当前区间下界（含），32 位
	high   uint64 // 当前区间上界（含），32 位
	follow uint64 // 延迟进位：待输出的相反位计数（bit-plus-follow 技术）
	w      bitWriter
}

func newCoreEncoder() coreEncoder {
	return coreEncoder{low: 0, high: topValue}
}

// encodeBit 用累积频率 c0（符号 0 的计数）和 total 编码一位。
func (e *coreEncoder) encodeBit(c0, total uint32, bit uint8) {
	rng := e.high - e.low + 1
	bound := rng * uint64(c0) / uint64(total) // 向下取整，见文件头注释
	if bit == 0 {
		e.high = e.low + bound - 1
	} else {
		e.low = e.low + bound
	}
	e.renormalize()
}

// renormalize 执行 E1/E2/E3 归一化，并把确定的位（含延迟进位）写出。
func (e *coreEncoder) renormalize() {
	for {
		switch {
		case e.high < half: // E1：区间完全在下半部
			e.emit(0)
		case e.low >= half: // E2：区间完全在上半部
			e.emit(1)
			e.low -= half
			e.high -= half
		case e.low >= firstQtr && e.high < thirdQtr: // E3：区间跨越中点
			e.follow++
			e.low -= firstQtr
			e.high -= firstQtr
		default:
			return
		}
		e.low <<= 1
		e.high = e.high<<1 | 1
	}
}

// emit 输出一个确定位，随后输出全部延迟的相反位（处理进位传播）。
func (e *coreEncoder) emit(bit uint64) {
	e.w.write(bit)
	for ; e.follow > 0; e.follow-- {
		e.w.write(bit ^ 1)
	}
}

// finish 终止编码：输出足以把最终码值钉在当前区间内的位，然后字节对齐。
func (e *coreEncoder) finish() {
	e.follow++
	if e.low < firstQtr {
		e.emit(0)
	} else {
		e.emit(1)
	}
	e.w.align()
}

// coreDecoder 是算术解码器的状态机，与 coreEncoder 严格镜像。
type coreDecoder struct {
	low   uint64
	high  uint64
	value uint64 // 已读入的码值，32 位窗口
	r     bitReader
}

func newCoreDecoder(payload []byte) *coreDecoder {
	d := &coreDecoder{low: 0, high: topValue}
	d.r.data = payload
	// 预填 32 位窗口；载荷不足 4 字节时以 0 填充（终止位保证正确性）。
	for i := 0; i < codeBits; i++ {
		d.value = d.value<<1 | d.r.read()
	}
	return d
}

// decodeBit 用与编码器相同的累积频率解出一位。
func (d *coreDecoder) decodeBit(c0, total uint32) uint8 {
	rng := d.high - d.low + 1
	bound := rng * uint64(c0) / uint64(total) // 与编码器相同的向下取整
	var bit uint8
	if d.value-d.low < bound {
		bit = 0
		d.high = d.low + bound - 1
	} else {
		bit = 1
		d.low = d.low + bound
	}
	d.renormalize()
	return bit
}

func (d *coreDecoder) renormalize() {
	for {
		switch {
		case d.high < half:
		case d.low >= half:
			d.low -= half
			d.high -= half
			d.value -= half
		case d.low >= firstQtr && d.high < thirdQtr:
			d.low -= firstQtr
			d.high -= firstQtr
			d.value -= firstQtr
		default:
			return
		}
		d.low <<= 1
		d.high = d.high<<1 | 1
		d.value = d.value<<1 | d.r.read()
	}
}
