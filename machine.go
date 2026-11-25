package leaklab

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// SlotKind 标识一个输入字节所在的存储位置类型。
type SlotKind uint8

const (
	// SlotReg 表示输入字节位于寄存器（Index 为寄存器编号 0..7）。
	SlotReg SlotKind = iota
	// SlotMem 表示输入字节位于内存（Index 为内存地址 0..255）。
	SlotMem
)

func (k SlotKind) String() string {
	if k == SlotReg {
		return "reg"
	}
	return "mem"
}

// MarshalJSON 以 "reg"/"mem" 文本输出位置类型，提升见证可读性。
func (k SlotKind) MarshalJSON() ([]byte, error) {
	return []byte(`"` + k.String() + `"`), nil
}

// UnmarshalJSON 解析 "reg"/"mem" 位置类型。
func (k *SlotKind) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case `"reg"`:
		*k = SlotReg
	case `"mem"`:
		*k = SlotMem
	default:
		return fmt.Errorf("invalid slot kind %s", data)
	}
	return nil
}

// Slot 定位一个输入字节：寄存器编号或内存地址。
type Slot struct {
	Kind  SlotKind `json:"kind"`
	Index uint8    `json:"index"`
}

func (s Slot) String() string {
	return fmt.Sprintf("%s[%d]", s.Kind, s.Index)
}

// InputSpec 声明一次分析的输入布局：至多两个公开字节、恰好一个秘密字节，
// 以及可选的固定初始内存（长度必须为 0 或 MemSize，其余初值固定为零）。
type InputSpec struct {
	Public  []Slot `json:"public"`
	Secret  Slot   `json:"secret"`
	BaseMem []byte `json:"base_mem,omitempty"`
}

// Validate 检查输入数量、位置合法性与公开/秘密位置互不重叠。
func (s InputSpec) Validate() error {
	if len(s.Public) > 2 {
		return fmt.Errorf("at most 2 public input bytes allowed, got %d", len(s.Public))
	}
	seen := map[Slot]bool{}
	for i, p := range s.Public {
		if err := checkSlot(p); err != nil {
			return fmt.Errorf("public slot %d: %w", i, err)
		}
		if seen[p] {
			return fmt.Errorf("public slot %d duplicates another slot: %s", i, p)
		}
		seen[p] = true
	}
	if err := checkSlot(s.Secret); err != nil {
		return fmt.Errorf("secret slot: %w", err)
	}
	if seen[s.Secret] {
		return fmt.Errorf("secret slot %s overlaps a public slot", s.Secret)
	}
	if len(s.BaseMem) != 0 && len(s.BaseMem) != MemSize {
		return fmt.Errorf("BaseMem must be empty or length %d, got %d", MemSize, len(s.BaseMem))
	}
	return nil
}

func checkSlot(s Slot) error {
	if s.Kind == SlotReg && s.Index >= RegCount {
		return fmt.Errorf("register index %d out of range [0,%d)", s.Index, RegCount)
	}
	if s.Kind != SlotReg && s.Kind != SlotMem {
		return fmt.Errorf("unknown slot kind %d", s.Kind)
	}
	return nil
}

// RunInput 是一次具体运行的输入取值。
type RunInput struct {
	Public []uint8 `json:"public"` // 长度必须与 InputSpec.Public 一致
	Secret uint8   `json:"secret"`
}

// MarshalJSON 强制公开字节以数字数组输出，避免 []uint8 被编码成 base64。
func (in RunInput) MarshalJSON() ([]byte, error) {
	pub := in.Public
	if pub == nil {
		pub = []uint8{}
	}
	return []byte(fmt.Sprintf(`{"public":[%s],"secret":%d}`, joinUint8(pub), in.Secret)), nil
}

// UnmarshalJSON 解析数字数组形式的公开字节。
func (in *RunInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		Public []uint8 `json:"public"`
		Secret uint8   `json:"secret"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	in.Public, in.Secret = raw.Public, raw.Secret
	return nil
}

func joinUint8(xs []uint8) string {
	var b []byte
	for i, x := range xs {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendUint(b, uint64(x), 10)
	}
	return string(b)
}

// ProbeEvent 是一次 probe 指令留下的记录。Set 为题面规定的地址/16 整数桶，
// Addr 额外保留原始 8 位地址，方便审查服务复核。
type ProbeEvent struct {
	Set  uint8 `json:"set"`
	Addr uint8 `json:"addr"`
}

// 逐步记录的类别：架构退休指令、推测指令、推测窗口终止标记。
const (
	StepArch    = "arch"
	StepSpec    = "spec"
	StepSpecEnd = "spec-end"
)

// StepRecord 是单条逐步记录，足以离线重放整次运行。
type StepRecord struct {
	// Kind 为 StepArch / StepSpec / StepSpecEnd。
	Kind string `json:"kind"`
	// Step 为架构退休序号（1 基）；推测记录回填其所属误预测的架构序号。
	Step int `json:"step"`
	// SpecStep 为推测窗口内序号（1 基），架构记录为 0。
	SpecStep int `json:"spec_step"`
	// PC 为指令下标。
	PC uint8 `json:"pc"`
	// Op 为该步操作码。
	Op Op `json:"op"`
	// Taken 仅对 jmpeq 有效，记录按当前寄存器实际计算出的条件。
	Taken bool `json:"taken"`
	// Mispredict 仅对架构 jmpeq 有效，标记该步选择了相反分支。
	Mispredict bool `json:"mispredict"`
	// NextPC 为架构执行后的下一条指令下标。
	NextPC uint8 `json:"next_pc"`
	// HasProbe 标记该步是否执行了 probe。
	HasProbe bool `json:"has_probe"`
	// Probe 为该步的 probe 记录，仅 HasProbe 为真时有效。
	Probe ProbeEvent `json:"probe"`
	// Note 用于 spec-end 记录终止原因（barrier/halt/spec-window/spec-pc-out-of-range）。
	Note string `json:"note,omitempty"`
}

// 运行终止状态。
const (
	// StatusHalt 为执行 halt 正常结束。
	StatusHalt = "halt"
	// StatusOutOfRange 为架构 PC 越出程序范围而结束（非未知）。
	StatusOutOfRange = "pc-out-of-range"
	// StatusStepBudget 为架构步数超过 MaxArchSteps，按题面记为未知。
	StatusStepBudget = "step-budget"
)

// RunOptions 控制单次运行的误预测选择。
type RunOptions struct {
	// MispredictPC 为要反转的条件跳转指令下标；-1 表示不做任何误预测。
	// 仅当该 jmpeq 在本次运行中实际被退休时生效，且全程至多一次。
	MispredictPC int
}

// RunResult 为一次运行的完整可重放结果。
type RunResult struct {
	Regs         [RegCount]uint8 `json:"regs"`
	Mem          [MemSize]byte   `json:"mem"`
	ArchTrace    []ProbeEvent    `json:"arch_trace"`
	SpecTrace    []ProbeEvent    `json:"spec_trace"`
	Steps        []StepRecord    `json:"steps"`
	ArchSteps    int             `json:"arch_steps"`
	Status       string          `json:"status"`
	MispredictPC int             `json:"mispredict_pc"`
	Mispredicted bool            `json:"mispredicted"`
}

type machine struct {
	prog *Program
	regs [RegCount]uint8
	mem  [MemSize]byte
}

func newMachine(prog *Program, spec InputSpec, in RunInput) (*machine, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if len(in.Public) != len(spec.Public) {
		return nil, fmt.Errorf("expected %d public bytes, got %d", len(spec.Public), len(in.Public))
	}
	m := &machine{prog: prog}
	if len(spec.BaseMem) == MemSize {
		copy(m.mem[:], spec.BaseMem)
	}
	for i, slot := range spec.Public {
		m.writeSlot(slot, in.Public[i])
	}
	m.writeSlot(spec.Secret, in.Secret)
	return m, nil
}

func (m *machine) writeSlot(s Slot, v uint8) {
	if s.Kind == SlotReg {
		m.regs[s.Index] = v
		return
	}
	m.mem[s.Index] = v
}

func (m *machine) snapshot() ([RegCount]uint8, [MemSize]byte) {
	return m.regs, m.mem
}

// Run 在给定程序、输入布局与具体取值上执行一次运行。
//
// 架构执行始终按真实条件跳转；当 opts.MispredictPC 指向的 jmpeq 被退休时，
// 该步选择相反分支，并进入至多 MaxSpecSteps 步的推测窗口，窗口内其他跳转
// 仍按实际条件执行，遇到 barrier/halt、窗口用尽或推测 PC 越界即结束。
// 结束后回滚寄存器与内存（probe 记录保留），从原分支的正确后继继续架构执行。
func Run(prog *Program, spec InputSpec, in RunInput, opts RunOptions) (*RunResult, error) {
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	m, err := newMachine(prog, spec, in)
	if err != nil {
		return nil, err
	}
	res := &RunResult{MispredictPC: -1}
	pc := 0
	usedMisprediction := false
	for {
		if res.ArchSteps >= MaxArchSteps {
			res.Status = StatusStepBudget
			break
		}
		if pc < 0 || pc >= len(m.prog.Instr) {
			res.Status = StatusOutOfRange
			break
		}
		ins := m.prog.Instr[pc]
		res.ArchSteps++
		rec := StepRecord{Kind: StepArch, Step: res.ArchSteps, PC: uint8(pc), Op: ins.Op, NextPC: uint8(pc + 1)}
		nextPC := pc + 1
		switch ins.Op {
		case OpConst:
			m.regs[ins.D] = ins.Imm
		case OpAdd:
			m.regs[ins.D] = m.regs[ins.A] + m.regs[ins.B]
		case OpXor:
			m.regs[ins.D] = m.regs[ins.A] ^ m.regs[ins.B]
		case OpLoad:
			m.regs[ins.D] = m.mem[m.regs[ins.A]]
		case OpProbe:
			ev := ProbeEvent{Addr: m.regs[ins.A], Set: m.regs[ins.A] / ProbeSetCount}
			res.ArchTrace = append(res.ArchTrace, ev)
			rec.HasProbe = true
			rec.Probe = ev
		case OpJmpEq:
			taken := m.regs[ins.A] == m.regs[ins.B]
			rec.Taken = taken
			archNext := pc + 1
			if taken {
				archNext = int(ins.Target)
			}
			wantMispredict := !usedMisprediction && opts.MispredictPC == pc
			if wantMispredict {
				// 选择相反分支：taken 时落到 fallthrough，not-taken 时跳 target。
				usedMisprediction = true
				res.Mispredicted = true
				res.MispredictPC = pc
				rec.Mispredict = true
				specEntry := archNext
				if taken {
					specEntry = pc + 1
				} else {
					specEntry = int(ins.Target)
				}
				res.Steps = append(res.Steps, rec)
				savedRegs, savedMem := m.snapshot()
				endNote := m.runSpeculation(specEntry, res, res.ArchSteps)
				m.regs, m.mem = savedRegs, savedMem
				res.Steps = append(res.Steps, StepRecord{
					Kind: StepSpecEnd, Step: res.ArchSteps, PC: uint8(specEntry),
					Op: OpSpecEnd, Note: endNote,
				})
				nextPC = archNext
				rec.NextPC = uint8(archNext)
				pc = archNext
				continue
			}
			nextPC = archNext
		case OpBarrier:
			// 架构态下为空操作。
		case OpHalt:
			res.Steps = append(res.Steps, rec)
			res.Status = StatusHalt
			res.Regs, res.Mem = m.regs, m.mem
			return res, nil
		}
		if ins.Op == OpJmpEq {
			rec.NextPC = uint8(nextPC)
		}
		res.Steps = append(res.Steps, rec)
		pc = nextPC
	}
	res.Regs, res.Mem = m.regs, m.mem
	return res, nil
}

// runSpeculation 在当前机器状态上执行推测窗口，probe 直接写入 res.SpecTrace
// 与逐步记录；寄存器/内存修改由调用方在窗口结束后回滚。返回终止原因。
func (m *machine) runSpeculation(entryPC int, res *RunResult, archStep int) string {
	pc := entryPC
	for specStep := 1; specStep <= MaxSpecSteps; specStep++ {
		if pc < 0 || pc >= len(m.prog.Instr) {
			return "spec-pc-out-of-range"
		}
		ins := m.prog.Instr[pc]
		rec := StepRecord{
			Kind: StepSpec, Step: archStep, SpecStep: specStep,
			PC: uint8(pc), Op: ins.Op, NextPC: uint8(pc + 1),
		}
		nextPC := pc + 1
		switch ins.Op {
		case OpConst:
			m.regs[ins.D] = ins.Imm
		case OpAdd:
			m.regs[ins.D] = m.regs[ins.A] + m.regs[ins.B]
		case OpXor:
			m.regs[ins.D] = m.regs[ins.A] ^ m.regs[ins.B]
		case OpLoad:
			m.regs[ins.D] = m.mem[m.regs[ins.A]]
		case OpProbe:
			ev := ProbeEvent{Addr: m.regs[ins.A], Set: m.regs[ins.A] / ProbeSetCount}
			res.SpecTrace = append(res.SpecTrace, ev)
			rec.HasProbe = true
			rec.Probe = ev
		case OpJmpEq:
			// 推测窗口内的其他条件跳转按实际条件执行。
			taken := m.regs[ins.A] == m.regs[ins.B]
			rec.Taken = taken
			if taken {
				nextPC = int(ins.Target)
			}
			rec.NextPC = uint8(nextPC)
		case OpBarrier:
			res.Steps = append(res.Steps, rec)
			return "barrier"
		case OpHalt:
			res.Steps = append(res.Steps, rec)
			return "halt"
		}
		if ins.Op == OpJmpEq {
			rec.NextPC = uint8(nextPC)
		}
		res.Steps = append(res.Steps, rec)
		pc = nextPC
	}
	return "spec-window"
}
