package cabac

import "math/rand"

// OpCode 是一个极简指令集的操作码。
type OpCode uint8

const (
	OpNop OpCode = iota // 空操作（热路径上的高频指令），无操作数
	OpMov               // 以下指令均携带 4 比特操作数（寄存器号/立即数）
	OpAdd
	OpSub
	OpLoad
	OpStore
	OpJmp
	OpCall
	opCount = 8
)

func (op OpCode) hasOperand() bool { return op != OpNop }

func (op OpCode) String() string {
	switch op {
	case OpNop:
		return "NOP"
	case OpMov:
		return "MOV"
	case OpAdd:
		return "ADD"
	case OpSub:
		return "SUB"
	case OpLoad:
		return "LOAD"
	case OpStore:
		return "STORE"
	case OpJmp:
		return "JMP"
	case OpCall:
		return "CALL"
	default:
		return "???"
	}
}

// Instr 是一条指令：操作码 + 4 比特操作数（仅部分操作码使用）。
type Instr struct {
	Op      OpCode
	Operand uint8
}

// numPrevClasses 是"上一条指令"条件上下文的类别数。
const numPrevClasses = 4

// ContextSet 为不同语法元素维护相互独立的上下文组：
//   - opBin0：操作码一元码第 1 个 bin，按上一条指令类别分 4 个上下文，
//     用于榨取连续指令之间的统计相关冗余；
//   - opBin1 / opTail：一元码第 2 个 bin 与后续尾部 bin；
//   - operand：操作数定长 4 个 bin，按比特位置各一个上下文。
type ContextSet struct {
	opBin0  [numPrevClasses]Context
	opBin1  Context
	opTail  Context
	operand [4]Context
}

// ctxFor 按上下文位置（与 binarizeTagged 的 ctxPos 约定一致）取上下文：
// 0 为一元码首 bin，1 为第二 bin，2..6 为一元码尾部，7..10 为操作数各位。
func (cs *ContextSet) ctxFor(pos, class int) *Context {
	switch {
	case pos == 0:
		return &cs.opBin0[class]
	case pos == 1:
		return &cs.opBin1
	case pos <= 6:
		return &cs.opTail
	default:
		return &cs.operand[pos-7]
	}
}

// prevClass 把"上一条指令"映射为条件上下文类别。
func prevClass(prev OpCode, first bool) int {
	if first {
		return 0
	}
	switch prev {
	case OpNop:
		return 1
	case OpMov:
		return 2
	default:
		return 3
	}
}

// taggedBin 是一个待编码的 bin：取值 + 它在语法结构中的上下文位置。
// 操作数 bin 的 ctxPos 固定为 7..10，与一元码实际长度无关，
// 保证编码端与解码端（InstrDecoder 的决策树）选择同一个上下文。
type taggedBin struct {
	val    int
	ctxPos int
}

// binarizeTagged 把一条指令二值化：操作码用截断一元码
// （0..6 为 i 个 1 后跟 0，7 为七个 1），有操作数时追加 4 个定长 bin。
func binarizeTagged(instr Instr) []taggedBin {
	bins := make([]taggedBin, 0, 11)
	op := int(instr.Op)
	for i := 0; i < 7; i++ {
		if op == i {
			bins = append(bins, taggedBin{0, i})
			break
		}
		bins = append(bins, taggedBin{1, i})
	}
	if instr.Op.hasOperand() {
		for i := 3; i >= 0; i-- {
			bins = append(bins, taggedBin{int(instr.Operand>>i) & 1, 7 + (3 - i)})
		}
	}
	return bins
}

// binarize 返回纯取值的 bin 序列（供无上下文基线模型使用）。
func binarize(instr Instr) []int {
	tagged := binarizeTagged(instr)
	bins := make([]int, len(tagged))
	for i, tb := range tagged {
		bins[i] = tb.val
	}
	return bins
}

// InstrEncoder 在算术编码器之上按语法元素驱动上下文编码指令流。
type InstrEncoder struct {
	enc   *Encoder
	ctx   *ContextSet
	prev  OpCode
	first bool
}

func NewInstrEncoder(enc *Encoder, ctx *ContextSet) *InstrEncoder {
	return &InstrEncoder{enc: enc, ctx: ctx, first: true}
}

func (ie *InstrEncoder) Encode(instr Instr) {
	class := prevClass(ie.prev, ie.first)
	for _, tb := range binarizeTagged(instr) {
		ie.enc.EncodeBin(ie.ctx.ctxFor(tb.ctxPos, class), tb.val)
	}
	ie.prev = instr.Op
	ie.first = false
}

// InstrDecoder 与 InstrEncoder 严格镜像。
type InstrDecoder struct {
	dec   *Decoder
	ctx   *ContextSet
	prev  OpCode
	first bool
}

func NewInstrDecoder(dec *Decoder, ctx *ContextSet) *InstrDecoder {
	return &InstrDecoder{dec: dec, ctx: ctx, first: true}
}

func (id *InstrDecoder) Decode() Instr {
	class := prevClass(id.prev, id.first)
	op := 0
	if id.dec.DecodeBin(&id.ctx.opBin0[class]) != 0 {
		op = 1
		if id.dec.DecodeBin(&id.ctx.opBin1) != 0 {
			op = 7
			for i := 2; i < 7; i++ {
				if id.dec.DecodeBin(&id.ctx.opTail) == 0 {
					op = i
					break
				}
			}
		}
	}
	var operand uint8
	if OpCode(op).hasOperand() {
		for i := 0; i < 4; i++ {
			operand = operand<<1 | uint8(id.dec.DecodeBin(&id.ctx.operand[i]))
		}
	}
	id.prev = OpCode(op)
	id.first = false
	return Instr{Op: OpCode(op), Operand: operand}
}

// EncodeTrace 编码整条指令序列，返回码流与按等分窗口统计的
// "比特/指令"收敛曲线（windows 个采样点）。
func EncodeTrace(instrs []Instr, windows int) ([]byte, []float64) {
	enc := NewEncoder()
	ie := NewInstrEncoder(enc, &ContextSet{})
	n := len(instrs)
	rates := make([]float64, windows)
	prevBits := 0
	for w := 0; w < windows; w++ {
		start := w * n / windows
		end := (w + 1) * n / windows
		for _, instr := range instrs[start:end] {
			ie.Encode(instr)
		}
		if end > start {
			rates[w] = float64(enc.BitsWritten()-prevBits) / float64(end-start)
		}
		prevBits = enc.BitsWritten()
	}
	enc.Flush()
	return enc.Bytes(), rates
}

// DecodeTrace 解码 n 条指令。
func DecodeTrace(data []byte, n int) []Instr {
	id := NewInstrDecoder(NewDecoder(data), &ContextSet{})
	out := make([]Instr, n)
	for i := range out {
		out[i] = id.Decode()
	}
	return out
}

// GenerateTrace 生成一段结构化指令序列：由"热区/冷区"两态马尔可夫链驱动，
// 热区连续产出高频 NOP（模拟紧凑循环体），冷区偶发其他带操作数的指令，
// 操作数取值向小寄存器号倾斜。相邻指令间存在强统计相关。
func GenerateTrace(n int, seed int64) []Instr {
	rng := rand.New(rand.NewSource(seed))
	trace := make([]Instr, 0, n)
	hot := true
	for len(trace) < n {
		if hot {
			trace = append(trace, Instr{Op: OpNop})
			if rng.Float64() < 0.012 {
				hot = false
			}
		} else {
			op := OpCode(1 + rng.Intn(int(opCount-1)))
			var operand uint8
			switch r := rng.Float64(); {
			case r < 0.5:
				operand = uint8(rng.Intn(2))
			case r < 0.85:
				operand = uint8(2 + rng.Intn(2))
			default:
				operand = uint8(4 + rng.Intn(12))
			}
			trace = append(trace, Instr{Op: op, Operand: operand})
			if rng.Float64() < 0.9 {
				hot = true
			}
		}
	}
	return trace
}
