// Package cabac 实现仿视频编码标准（H.264/AVC 风格）的上下文自适应二进制
// 算术编码（CABAC）内核：
//
//   - 每个语法元素（甚至同一语法元素的不同 bin 位置、不同邻接条件）持有独立
//     的 Context，各自维护 64 级概率状态编号 pStateIdx 与大概率符号 valMPS；
//   - 每编码一个 bin，按 H.264 的状态转移表动态微调 pStateIdx：命中 MPS 时
//     状态号 +1（概率更尖锐），命中 LPS 时沿 transIdxLPS 回落，状态 0 处
//     翻转 MPS，从而持续追踪符号流的局部统计特性；
//   - 区间细分查 rangeTabLPS（9 比特区间、4 档量化），配合比特级重归一化
//     与 outstanding 进位传播输出码流。
package cabac

// Context 是一个二进制概率模型：64 级概率状态 + 大概率符号取值。
// 零值即为合法初始状态（pStateIdx=0，valMPS=0，约等于 0.5 概率起步）。
type Context struct {
	pState uint8
	valMPS uint8
}

// rangeTabLPS 对应 H.264 Table 9-44：pStateIdx 与量化区间索引
// qCodIRangeIdx=(codIRange>>6)&3 共同索引出的 LPS 子区间宽度。
var rangeTabLPS = [64][4]uint16{
	{128, 176, 208, 240}, {128, 167, 197, 227}, {128, 158, 187, 216}, {123, 150, 178, 205},
	{116, 142, 169, 195}, {111, 135, 160, 185}, {105, 128, 152, 175}, {100, 122, 144, 166},
	{95, 116, 137, 158}, {90, 110, 130, 150}, {85, 104, 123, 142}, {81, 99, 117, 135},
	{77, 94, 111, 128}, {73, 89, 105, 122}, {69, 85, 100, 116}, {66, 80, 95, 110},
	{62, 76, 90, 104}, {59, 72, 86, 99}, {56, 69, 81, 94}, {53, 66, 77, 89},
	{51, 63, 73, 85}, {48, 60, 69, 80}, {46, 57, 66, 76}, {43, 54, 62, 72},
	{41, 51, 59, 69}, {39, 48, 56, 68}, {37, 45, 54, 64}, {35, 43, 51, 61},
	{33, 41, 48, 58}, {32, 39, 46, 55}, {30, 37, 44, 53}, {29, 35, 42, 50},
	{27, 33, 40, 48}, {26, 31, 38, 46}, {24, 30, 36, 44}, {23, 28, 34, 42},
	{22, 27, 32, 40}, {21, 26, 30, 38}, {20, 24, 29, 36}, {19, 23, 27, 34},
	{18, 22, 26, 32}, {17, 21, 25, 30}, {16, 20, 23, 29}, {15, 19, 22, 27},
	{14, 18, 21, 26}, {14, 17, 20, 25}, {13, 16, 19, 24}, {12, 15, 18, 23},
	{12, 14, 17, 21}, {11, 14, 16, 20}, {11, 13, 15, 19}, {10, 12, 15, 18},
	{10, 12, 14, 17}, {9, 11, 13, 16}, {9, 11, 12, 15}, {8, 10, 12, 14},
	{8, 9, 11, 13}, {7, 9, 10, 12}, {7, 8, 9, 11}, {6, 8, 9, 10},
	{6, 7, 8, 9}, {5, 6, 7, 8}, {5, 6, 6, 7}, {4, 5, 6, 6},
}

// transIdxLPS 对应 H.264 Table 9-47 的 LPS 列：命中小概率符号后的状态回落。
var transIdxLPS = [64]uint8{
	0, 0, 1, 2, 2, 4, 4, 5,
	6, 7, 8, 9, 9, 11, 11, 12,
	13, 13, 15, 15, 16, 16, 18, 18,
	19, 19, 21, 21, 22, 22, 23, 24,
	24, 25, 26, 26, 27, 27, 28, 29,
	29, 30, 30, 30, 31, 32, 32, 33,
	33, 33, 34, 34, 35, 35, 36, 36,
	36, 37, 37, 37, 38, 38, 63, 63,
}

// transIdxMPS 对应 MPS 列：命中大概率符号后状态号 +1，63 处饱和。
func transIdxMPS(pState uint8) uint8 {
	if pState < 63 {
		return pState + 1
	}
	return 63
}

const (
	renormThreshold = 256 // 区间下限：低于 2^8 即触发重归一化
	initRange       = 510 // 初始区间（9 比特窗口 [0,1024) 内）
)

// Encoder 是 CABAC 算术编码器。low 为 10 比特窗口内的码点下界，
// rng 为当前区间宽度，pending 记录待传播的进位跟随比特数。
type Encoder struct {
	bw       bitWriter
	low      int
	rng      int
	pending  int
	firstBit bool
}

// NewEncoder 创建编码器，初始区间 510，首个输出比特被吞掉
// （它恒为 0，解码端用 9 比特初始化正好对齐，与 H.264 一致）。
func NewEncoder() *Encoder {
	return &Encoder{rng: initRange, firstBit: true}
}

// EncodeBin 用给定上下文编码一个比特，并按命中情况迁移概率状态。
func (e *Encoder) EncodeBin(ctx *Context, binVal int) {
	rLPS := int(rangeTabLPS[ctx.pState][(e.rng>>6)&3])
	e.rng -= rLPS
	if binVal == int(ctx.valMPS) {
		ctx.pState = transIdxMPS(ctx.pState)
	} else {
		e.low += e.rng
		e.rng = rLPS
		if ctx.pState == 0 {
			ctx.valMPS ^= 1
		}
		ctx.pState = transIdxLPS[ctx.pState]
	}
	e.renorm()
}

// EncodeBinBypass 用固定的 0.5 概率编码一个比特（不更新任何状态），
// 作为无上下文静态模型的参照；解码端对应 DecodeBinBypass。
func (e *Encoder) EncodeBinBypass(binVal int) {
	rLPS := int(rangeTabLPS[0][(e.rng>>6)&3])
	e.rng -= rLPS
	if binVal != 0 {
		e.low += e.rng
		e.rng = rLPS
	}
	e.renorm()
}

func (e *Encoder) renorm() {
	for e.rng < renormThreshold {
		switch {
		case e.low < 256:
			e.putBit(0)
		case e.low >= 512:
			e.low -= 512
			e.putBit(1)
		default:
			e.low -= 256
			e.pending++
		}
		e.rng <<= 1
		e.low <<= 1
	}
}

// putBit 输出一个比特并冲刷此前挂起的跟随比特（进位传播）。
func (e *Encoder) putBit(b int) {
	if e.firstBit {
		e.firstBit = false
	} else {
		e.bw.writeBit(b)
	}
	for e.pending > 0 {
		e.bw.writeBit(1 - b)
		e.pending--
	}
}

// Flush 把窗口内剩余码点（low 的 10 个比特）写入流中收尾。
// 之后应调用 Bytes 取走码流，编码器不可再继续编码。
func (e *Encoder) Flush() {
	e.putBit((e.low >> 9) & 1)
	for i := 8; i >= 0; i-- {
		e.bw.writeBit((e.low >> i) & 1)
	}
}

// Bytes 返回收尾后的完整码流（末尾不足一字节处补零）。
func (e *Encoder) Bytes() []byte { return e.bw.finish() }

// BitsWritten 返回目前已物理写出的比特数（不含挂起的跟随比特），
// 用于编码过程中的码率观测。
func (e *Encoder) BitsWritten() int { return e.bw.total }

// Decoder 是 CABAC 算术解码器，与 Encoder 严格镜像。
type Decoder struct {
	br     bitReader
	offset int
	rng    int
}

// NewDecoder 创建解码器：区间置 510，并读入 9 比特初始化码点偏移。
func NewDecoder(data []byte) *Decoder {
	d := &Decoder{rng: initRange}
	d.br.data = data
	for i := 0; i < 9; i++ {
		d.offset = (d.offset << 1) | d.br.readBit()
	}
	return d
}

// DecodeBin 用给定上下文解码一个比特，状态迁移与编码端完全一致。
func (d *Decoder) DecodeBin(ctx *Context) int {
	rLPS := int(rangeTabLPS[ctx.pState][(d.rng>>6)&3])
	d.rng -= rLPS
	var binVal int
	if d.offset >= d.rng {
		binVal = 1 - int(ctx.valMPS)
		d.offset -= d.rng
		d.rng = rLPS
		if ctx.pState == 0 {
			ctx.valMPS ^= 1
		}
		ctx.pState = transIdxLPS[ctx.pState]
	} else {
		binVal = int(ctx.valMPS)
		ctx.pState = transIdxMPS(ctx.pState)
	}
	for d.rng < renormThreshold {
		d.rng <<= 1
		d.offset = (d.offset << 1) | d.br.readBit()
	}
	return binVal
}

// DecodeBinBypass 解码一个固定 0.5 概率编码的比特。
func (d *Decoder) DecodeBinBypass() int {
	rLPS := int(rangeTabLPS[0][(d.rng>>6)&3])
	d.rng -= rLPS
	var binVal int
	if d.offset >= d.rng {
		binVal = 1
		d.offset -= d.rng
		d.rng = rLPS
	}
	for d.rng < renormThreshold {
		d.rng <<= 1
		d.offset = (d.offset << 1) | d.br.readBit()
	}
	return binVal
}
