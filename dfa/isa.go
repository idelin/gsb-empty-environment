package dfa

// 自定义安全启动 VM 的指令集与汇编/链接层。
//
// 平台约束：
//   - 单程序至多 32 条指令（MaxInstrs）
//   - 6 个布尔寄存器 r0..r5（NumBoolRegs）
//   - 1 个有界 4 位计数器 c0，取值模 16，支持 inc/dec/jz
//   - 至多 8 个公开输入位 p0..p7（MaxInputBits）
//   - 至多 64 个执行步（MaxSteps）
//   - emit(tag) 的 tag 为 4 位无符号数 0..15

const (
	MaxInstrs    = 32
	MaxBoolRegs  = 6
	MaxInputBits = 8
	MaxSteps     = 64
	MaxEmitTag   = 15
	CounterMod   = 16
)

// Op 是操作码助记符，直接以字符串保存以便调试与 diff。
type Op string

const (
	OpInp    Op = "inp"  // inp rd, pN      读公开输入位
	OpLds    Op = "lds"  // lds rd          读验签结果位（只读，故障不可改）
	OpAnd    Op = "and"  // and rd, rs
	OpNot    Op = "not"  // not rd, rs
	OpInc    Op = "inc"  // inc             计数器 +1（模 16）
	OpDec    Op = "dec"  // dec             计数器 -1（模 16）
	OpJz     Op = "jz"   // jz label        计数器为 0 时跳转
	OpJt     Op = "jt"   // jt rs, label    布尔寄存器为 1 时跳转
	OpJmp    Op = "jmp"  // jmp label       无条件跳转
	OpEmit   Op = "emit" // emit tag
	OpUnlock Op = "unlock"
	OpHalt   Op = "halt"
)

// Instr 是一条符号化指令。链接前 Target 保存目标标签；链接后 Target
// 保存具体指令地址，TargetLabel 仍保留原值用于反汇编。
type Instr struct {
	Op          Op
	Dst         int    // 写回的布尔寄存器（inp/lds/and/not）
	Src         int    // 源布尔寄存器（and/not/jt）
	InputBit    int    // 公开输入位编号（inp）
	Tag         int    // emit 的 4 位 tag
	Target      int    // 链接后的跳转地址
	TargetLabel string // 链接前的符号标签
	Line        int    // 汇编源行号，报错定位用
}

// Program 是链接后的可执行程序。
type Program struct {
	Instrs []Instr
	Labels map[string]int
	Width  int // 公开输入位数量（0..8）
}

// ProgramText 是解析后但尚未链接的符号化程序。
type ProgramText struct {
	Instrs []Instr
	Labels map[string]int // 标签 -> 指令下标
}

var arityOK = map[Op]bool{
	OpInp: true, OpLds: true, OpAnd: true, OpNot: true,
	OpInc: true, OpDec: true, OpJz: true, OpJt: true, OpJmp: true,
	OpEmit: true, OpUnlock: true, OpHalt: true,
}

// ParseText 解析分号或换行分隔的汇编文本。
// 标签写作 "name:"；注释以 '#' 或 ';' 开始直到行尾。
func ParseText(src string) (*ProgramText, error) {
	pt := &ProgramText{Labels: map[string]int{}}
	lines := splitLines(src)
	for li, raw := range lines {
		line := stripComment(raw)
		toks, err := tokenize(line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 0 {
			continue
		}
		// 同一物理行允许 "label: instr ..."。
		for len(toks) > 0 && isLabelToken(toks[0]) {
			label := toks[0][:len(toks[0])-1]
			if !identRe.MatchString(label) {
				return nil, asmErr(li+1, "非法标签 %q", toks[0])
			}
			if _, dup := pt.Labels[label]; dup {
				return nil, asmErr(li+1, "标签重复定义 %q", label)
			}
			pt.Labels[label] = len(pt.Instrs)
			toks = toks[1:]
		}
		if len(toks) == 0 {
			continue
		}
		ins, err := parseInstr(toks, li+1)
		if err != nil {
			return nil, err
		}
		pt.Instrs = append(pt.Instrs, ins)
	}
	return pt, nil
}

func parseInstr(toks []string, line int) (Instr, error) {
	ins := Instr{Op: Op(toks[0]), Line: line, Target: -1}
	if !arityOK[ins.Op] {
		return ins, asmErr(line, "未知操作码 %q", toks[0])
	}
	args := toks[1:]
	switch ins.Op {
	case OpInp:
		if len(args) != 2 {
			return ins, asmErr(line, "inp 需要 rd 与 pN 两个参数")
		}
		rd, ok := parseReg(args[0])
		if !ok {
			return ins, asmErr(line, "非法寄存器 %q", args[0])
		}
		bit, ok := parseInputBit(args[1])
		if !ok {
			return ins, asmErr(line, "非法输入位 %q", args[1])
		}
		ins.Dst, ins.InputBit = rd, bit
	case OpLds:
		if len(args) != 1 {
			return ins, asmErr(line, "lds 需要一个目标寄存器")
		}
		rd, ok := parseReg(args[0])
		if !ok {
			return ins, asmErr(line, "非法寄存器 %q", args[0])
		}
		ins.Dst = rd
	case OpAnd, OpNot:
		if len(args) != 2 {
			return ins, asmErr(line, "%s 需要 rd, rs 两个参数", ins.Op)
		}
		rd, ok1 := parseReg(args[0])
		rs, ok2 := parseReg(args[1])
		if !ok1 || !ok2 {
			return ins, asmErr(line, "%s 参数必须是布尔寄存器", ins.Op)
		}
		ins.Dst, ins.Src = rd, rs
	case OpJt:
		if len(args) != 2 {
			return ins, asmErr(line, "jt 需要 rs 与 label 两个参数")
		}
		rs, ok := parseReg(args[0])
		if !ok {
			return ins, asmErr(line, "非法寄存器 %q", args[0])
		}
		ins.Src, ins.TargetLabel = rs, args[1]
	case OpJz, OpJmp:
		if len(args) != 1 {
			return ins, asmErr(line, "%s 需要一个标签参数", ins.Op)
		}
		ins.TargetLabel = args[0]
	case OpEmit:
		if len(args) != 1 {
			return ins, asmErr(line, "emit 需要一个 4 位 tag")
		}
		tag, ok := parseTag(args[0])
		if !ok {
			return ins, asmErr(line, "非法 emit tag %q", args[0])
		}
		ins.Tag = tag
	case OpInc, OpDec, OpUnlock, OpHalt:
		if len(args) != 0 {
			return ins, asmErr(line, "%s 不接受参数", ins.Op)
		}
	}
	return ins, nil
}

// Link 解析符号标签为具体地址，并校验全部平台资源约束。
func (pt *ProgramText) Link(width int) (*Program, error) {
	if width < 0 || width > MaxInputBits {
		return nil, asmErr(0, "输入位宽度 %d 超出 0..%d", width, MaxInputBits)
	}
	n := len(pt.Instrs)
	if n == 0 {
		return nil, asmErr(0, "程序为空")
	}
	if n > MaxInstrs {
		return nil, asmErr(0, "程序有 %d 条指令，超过上限 %d", n, MaxInstrs)
	}
	out := make([]Instr, n)
	copy(out, pt.Instrs)
	for i, ins := range out {
		switch ins.Op {
		case OpJz, OpJt, OpJmp:
			addr, ok := pt.Labels[ins.TargetLabel]
			if !ok {
				return nil, asmErr(ins.Line, "未定义标签 %q", ins.TargetLabel)
			}
			out[i].Target = addr
		}
	}
	// 资源约束二次校验，防止手工构造 ProgramText 绕过解析器。
	for i, ins := range out {
		switch ins.Op {
		case OpInp:
			if ins.Dst < 0 || ins.Dst >= MaxBoolRegs || ins.InputBit < 0 || ins.InputBit >= MaxInputBits {
				return nil, asmErr(ins.Line, "inp 资源越界")
			}
		case OpLds, OpAnd, OpNot:
			if ins.Dst < 0 || ins.Dst >= MaxBoolRegs {
				return nil, asmErr(ins.Line, "目标寄存器越界")
			}
			if ins.Op != OpLds && (ins.Src < 0 || ins.Src >= MaxBoolRegs) {
				return nil, asmErr(ins.Line, "源寄存器越界")
			}
		case OpJt:
			if ins.Src < 0 || ins.Src >= MaxBoolRegs {
				return nil, asmErr(ins.Line, "jt 寄存器越界")
			}
			if ins.Target < 0 || ins.Target >= n {
				return nil, asmErr(i, "跳转地址越界")
			}
		case OpJz, OpJmp:
			if ins.Target < 0 || ins.Target >= n {
				return nil, asmErr(i, "跳转地址越界")
			}
		case OpEmit:
			if ins.Tag < 0 || ins.Tag > MaxEmitTag {
				return nil, asmErr(ins.Line, "emit tag 超出 4 位范围")
			}
		}
	}
	labels := make(map[string]int, len(pt.Labels))
	for k, v := range pt.Labels {
		labels[k] = v
	}
	return &Program{Instrs: out, Labels: labels, Width: width}, nil
}

// MustParse 是测试/示例用的便捷构造，出错即 panic。
func MustParse(src string, width int) *Program {
	pt, err := ParseText(src)
	if err != nil {
		panic(err)
	}
	prog, err := pt.Link(width)
	if err != nil {
		panic(err)
	}
	return prog
}

// Disassemble 把链接后的程序还原成规范汇编文本。
func (p *Program) Disassemble() string {
	addrLabel := map[int]string{}
	for name, addr := range p.Labels {
		addrLabel[addr] = name
	}
	var b []byte
	for i, ins := range p.Instrs {
		if name, ok := addrLabel[i]; ok {
			b = append(b, name+":\n"...)
		}
		b = append(b, "  "...)
		b = append(b, string(ins.Op)...)
		switch ins.Op {
		case OpInp:
			b = append(b, " r"+itoa(ins.Dst)+", p"+itoa(ins.InputBit)...)
		case OpLds:
			b = append(b, " r"+itoa(ins.Dst)...)
		case OpAnd, OpNot:
			b = append(b, " r"+itoa(ins.Dst)+", r"+itoa(ins.Src)...)
		case OpJt:
			b = append(b, " r"+itoa(ins.Src)+", "+ins.TargetLabel...)
		case OpJz, OpJmp:
			b = append(b, " "+ins.TargetLabel...)
		case OpEmit:
			b = append(b, " "+itoa(ins.Tag)...)
		}
		b = append(b, '\n')
	}
	return string(b)
}
