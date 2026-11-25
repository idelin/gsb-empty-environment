package leaklab

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 本包实现一个刻意简化的微型指令集（不宣称模拟任何真实 CPU 的微架构），
// 用于在解释器优化合入前，检测“越界读被分支预测隐藏”的推测执行泄漏。

const (
	// RegCount 为架构寄存器数量（8 个 8 位寄存器，编号 0..7）。
	RegCount = 8
	// MemSize 为可寻址内存字节数（256 字节，地址按 8 位回绕）。
	MemSize = 256
	// MaxProgram 为单段程序允许的最大指令数。
	MaxProgram = 64
	// MaxArchSteps 为每次运行允许架构退休的最大步数，超限结果为未知。
	MaxArchSteps = 64
	// MaxSpecSteps 为单次误预测窗口内允许推测执行的最大指令数。
	MaxSpecSteps = 8
	// ProbeSetCount 为 probe 记录的地址桶数量（地址 / 16）。
	ProbeSetCount = MemSize / 16
)

// Op 为微型指令集的操作码。
type Op uint8

const (
	OpConst   Op = iota // const rd, imm        ; rd = imm
	OpAdd               // add rd, ra, rb       ; rd = (ra + rb) mod 256
	OpXor               // xor rd, ra, rb       ; rd = ra ^ rb
	OpLoad              // load rd, ra          ; rd = mem[ra]（8 位地址回绕）
	OpProbe             // probe ra             ; 记录内存访问桶 ra/16
	OpJmpEq             // jmpeq ra, rb, target ; ra == rb 时跳转到 target
	OpBarrier           // barrier              ; 立即结束推测；架构态下为空操作
	OpHalt              // halt                 ; 正常停机
	OpSpecEnd Op = 255  // 仅用于逐步记录：推测窗口终止标记，非真实指令
)

// String 返回操作码的规范助记符。
func (op Op) String() string {
	switch op {
	case OpConst:
		return "const"
	case OpAdd:
		return "add"
	case OpXor:
		return "xor"
	case OpLoad:
		return "load"
	case OpProbe:
		return "probe"
	case OpJmpEq:
		return "jmpeq"
	case OpBarrier:
		return "barrier"
	case OpHalt:
		return "halt"
	case OpSpecEnd:
		return "spec-end"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// MarshalJSON 以助记符文本输出操作码。
func (op Op) MarshalJSON() ([]byte, error) {
	return []byte(`"` + op.String() + `"`), nil
}

// UnmarshalJSON 按助记符解析操作码。
func (op *Op) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	for _, candidate := range []Op{OpConst, OpAdd, OpXor, OpLoad, OpProbe, OpJmpEq, OpBarrier, OpHalt, OpSpecEnd} {
		if candidate.String() == text {
			*op = candidate
			return nil
		}
	}
	return fmt.Errorf("invalid opcode %q", text)
}

// Instr 为一条解码后的指令。
//
// 字段语义按操作码不同而不同：
//
//	const        使用 D 与 Imm
//	add/xor      使用 D, A, B
//	load         使用 D, A
//	probe        使用 A
//	jmpeq        使用 A, B, Target（Target 为指令下标）
//	barrier/halt 无操作数
type Instr struct {
	Op     Op
	D      uint8
	A      uint8
	B      uint8
	Imm    uint8
	Target uint8
}

// String 输出规范汇编文本，与 ParseProgram 可逆。
func (ins Instr) String() string {
	switch ins.Op {
	case OpConst:
		return fmt.Sprintf("const r%d, %d", ins.D, ins.Imm)
	case OpAdd:
		return fmt.Sprintf("add r%d, r%d, r%d", ins.D, ins.A, ins.B)
	case OpXor:
		return fmt.Sprintf("xor r%d, r%d, r%d", ins.D, ins.A, ins.B)
	case OpLoad:
		return fmt.Sprintf("load r%d, r%d", ins.D, ins.A)
	case OpProbe:
		return fmt.Sprintf("probe r%d", ins.A)
	case OpJmpEq:
		return fmt.Sprintf("jmpeq r%d, r%d, %d", ins.A, ins.B, ins.Target)
	case OpBarrier:
		return "barrier"
	case OpHalt:
		return "halt"
	default:
		return fmt.Sprintf("op(%d)", uint8(ins.Op))
	}
}

// Program 为已校验的指令序列。
type Program struct {
	Instr []Instr
}

// Text 返回整段程序的规范汇编文本（每行一条）。
func (p *Program) Text() string {
	var b strings.Builder
	for i, ins := range p.Instr {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ins.String())
	}
	return b.String()
}

// Validate 校验程序长度、寄存器编号与跳转目标，返回错误描述具体位置。
func (p *Program) Validate() error {
	if len(p.Instr) == 0 {
		return errors.New("program is empty")
	}
	if len(p.Instr) > MaxProgram {
		return fmt.Errorf("program has %d instructions, max is %d", len(p.Instr), MaxProgram)
	}
	for i, ins := range p.Instr {
		checkReg := func(name string, r uint8) error {
			if r >= RegCount {
				return fmt.Errorf("instruction %d (%s): register %s=r%d out of range", i, ins.Op, name, r)
			}
			return nil
		}
		switch ins.Op {
		case OpConst:
			if err := checkReg("d", ins.D); err != nil {
				return err
			}
		case OpAdd, OpXor:
			if err := checkReg("d", ins.D); err != nil {
				return err
			}
			if err := checkReg("a", ins.A); err != nil {
				return err
			}
			if err := checkReg("b", ins.B); err != nil {
				return err
			}
		case OpLoad:
			if err := checkReg("d", ins.D); err != nil {
				return err
			}
			if err := checkReg("a", ins.A); err != nil {
				return err
			}
		case OpProbe:
			if err := checkReg("a", ins.A); err != nil {
				return err
			}
		case OpJmpEq:
			if err := checkReg("a", ins.A); err != nil {
				return err
			}
			if err := checkReg("b", ins.B); err != nil {
				return err
			}
			if int(ins.Target) >= len(p.Instr) {
				return fmt.Errorf("instruction %d (jmpeq): target %d out of range [0,%d)", i, ins.Target, len(p.Instr))
			}
		case OpBarrier, OpHalt:
			// 无操作数。
		default:
			return fmt.Errorf("instruction %d: unknown opcode %d", i, uint8(ins.Op))
		}
	}
	return nil
}

// ParseProgram 解析规范汇编文本，支持空行、# / ; 注释与行首标签（"name:"）。
// jmpeq 的第三操作数既可以是数字下标，也可以是已定义的标签名。
func ParseProgram(text string) (*Program, error) {
	rawLines := strings.Split(text, "\n")
	type parsed struct {
		ins    Instr
		target string // jmpeq 的符号目标（空表示直接使用数字 Target）
	}
	var out []parsed
	labels := map[string]int{}
	for lineNo, raw := range rawLines {
		line := raw
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 行首标签（标签可独占一行，也允许与指令同行）。
		for {
			colon := strings.IndexByte(line, ':')
			if colon < 0 {
				break
			}
			label := strings.TrimSpace(line[:colon])
			if label == "" || strings.ContainsAny(label, " \t") {
				break
			}
			if _, dup := labels[label]; dup {
				return nil, fmt.Errorf("line %d: duplicate label %q", lineNo+1, label)
			}
			labels[label] = len(out)
			line = strings.TrimSpace(line[colon+1:])
			if line == "" {
				break
			}
		}
		if line == "" {
			continue
		}
		toks := strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ','
		})
		if len(toks) == 0 {
			continue
		}
		ins, target, err := parseInstr(toks)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		out = append(out, parsed{ins: ins, target: target})
	}
	if len(out) == 0 {
		return nil, errors.New("program is empty")
	}
	prog := &Program{Instr: make([]Instr, len(out))}
	for i, item := range out {
		ins := item.ins
		if item.target != "" {
			idx, ok := labels[item.target]
			if !ok {
				return nil, fmt.Errorf("jmpeq at instruction %d references undefined label %q", i, item.target)
			}
			ins.Target = uint8(idx)
		}
		prog.Instr[i] = ins
	}
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	return prog, nil
}

func parseInstr(toks []string) (Instr, string, error) {
	var ins Instr
	target := ""
	switch strings.ToLower(toks[0]) {
	case "const":
		if len(toks) != 3 {
			return ins, "", errors.New("const expects: const rd, imm")
		}
		d, err := reg(toks[1])
		if err != nil {
			return ins, "", err
		}
		imm, err := imm8(toks[2])
		if err != nil {
			return ins, "", err
		}
		ins.Op, ins.D, ins.Imm = OpConst, d, imm
	case "add", "xor":
		if len(toks) != 4 {
			return ins, "", fmt.Errorf("%s expects: %s rd, ra, rb", toks[0], toks[0])
		}
		d, err := reg(toks[1])
		if err != nil {
			return ins, "", err
		}
		a, err := reg(toks[2])
		if err != nil {
			return ins, "", err
		}
		bb, err := reg(toks[3])
		if err != nil {
			return ins, "", err
		}
		if toks[0] == "add" {
			ins.Op = OpAdd
		} else {
			ins.Op = OpXor
		}
		ins.D, ins.A, ins.B = d, a, bb
	case "load":
		if len(toks) != 3 {
			return ins, "", errors.New("load expects: load rd, ra")
		}
		d, err := reg(toks[1])
		if err != nil {
			return ins, "", err
		}
		a, err := reg(toks[2])
		if err != nil {
			return ins, "", err
		}
		ins.Op, ins.D, ins.A = OpLoad, d, a
	case "probe":
		if len(toks) != 2 {
			return ins, "", errors.New("probe expects: probe ra")
		}
		a, err := reg(toks[1])
		if err != nil {
			return ins, "", err
		}
		ins.Op, ins.A = OpProbe, a
	case "jmpeq":
		if len(toks) != 4 {
			return ins, "", errors.New("jmpeq expects: jmpeq ra, rb, target")
		}
		a, err := reg(toks[1])
		if err != nil {
			return ins, "", err
		}
		bb, err := reg(toks[2])
		if err != nil {
			return ins, "", err
		}
		t, err := strconv.Atoi(toks[3])
		if err != nil {
			// 符号标签，留待第二遍解析。
			target = toks[3]
			t = 0
		} else if t < 0 || t > 255 {
			return ins, "", fmt.Errorf("jump target %d out of uint8 range", t)
		}
		ins.Op, ins.A, ins.B, ins.Target = OpJmpEq, a, bb, uint8(t)
	case "barrier":
		if len(toks) != 1 {
			return ins, "", errors.New("barrier takes no operands")
		}
		ins.Op = OpBarrier
	case "halt":
		if len(toks) != 1 {
			return ins, "", errors.New("halt takes no operands")
		}
		ins.Op = OpHalt
	default:
		return ins, "", fmt.Errorf("unknown mnemonic %q", toks[0])
	}
	return ins, target, nil
}

func reg(s string) (uint8, error) {
	if len(s) < 2 || (s[0] != 'r' && s[0] != 'R') {
		return 0, fmt.Errorf("expected register like r0, got %q", s)
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 0 || n >= RegCount {
		return 0, fmt.Errorf("invalid register %q", s)
	}
	return uint8(n), nil
}

func imm8(s string) (uint8, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return 0, fmt.Errorf("invalid 8-bit immediate %q", s)
	}
	return uint8(n), nil
}

// AsmBuilder 是带标签的程序构建器，降低手工计算跳转下标出错的概率。
type AsmBuilder struct {
	instr  []Instr
	labels map[string]int
	ref    []struct {
		index int
		label string
	}
}

// NewAsmBuilder 创建空构建器。
func NewAsmBuilder() *AsmBuilder {
	return &AsmBuilder{labels: map[string]int{}}
}

// Label 在当前指令位置打上标签。
func (b *AsmBuilder) Label(name string) *AsmBuilder {
	b.labels[name] = len(b.instr)
	return b
}

// Const 发射 const rd, imm。
func (b *AsmBuilder) Const(d uint8, imm uint8) *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpConst, D: d, Imm: imm})
	return b
}

// Add 发射 add rd, ra, rb。
func (b *AsmBuilder) Add(d, a, bb uint8) *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpAdd, D: d, A: a, B: bb})
	return b
}

// Xor 发射 xor rd, ra, rb。
func (b *AsmBuilder) Xor(d, a, bb uint8) *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpXor, D: d, A: a, B: bb})
	return b
}

// Load 发射 load rd, ra。
func (b *AsmBuilder) Load(d, a uint8) *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpLoad, D: d, A: a})
	return b
}

// Probe 发射 probe ra。
func (b *AsmBuilder) Probe(a uint8) *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpProbe, A: a})
	return b
}

// JmpEqLabel 发射跳转到标签的 jmpeq。
func (b *AsmBuilder) JmpEqLabel(a, bb uint8, label string) *AsmBuilder {
	idx := len(b.instr)
	b.instr = append(b.instr, Instr{Op: OpJmpEq, A: a, B: bb})
	b.ref = append(b.ref, struct {
		index int
		label string
	}{idx, label})
	return b
}

// Barrier 发射 barrier。
func (b *AsmBuilder) Barrier() *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpBarrier})
	return b
}

// Halt 发射 halt。
func (b *AsmBuilder) Halt() *AsmBuilder {
	b.instr = append(b.instr, Instr{Op: OpHalt})
	return b
}

// Build 解析标签引用并校验程序，返回可执行的 Program。
func (b *AsmBuilder) Build() (*Program, error) {
	prog := &Program{Instr: append([]Instr(nil), b.instr...)}
	for _, r := range b.ref {
		idx, ok := b.labels[r.label]
		if !ok {
			return nil, fmt.Errorf("jmpeq at instruction %d references undefined label %q", r.index, r.label)
		}
		prog.Instr[r.index].Target = uint8(idx)
	}
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	return prog, nil
}
