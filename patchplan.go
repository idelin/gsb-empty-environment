// Package netsec 提供函数入口热补丁的布局规划能力。
//
// 规划器本身不绑定任何具体指令集：指令解码器由调用方实现，规划器负责
// 在函数入口处按整条指令边界累积覆盖长度、拒绝落入覆盖区内部的已知入口
// 与分支目标，并把被覆盖的指令（含相对跳转）重定位到跳板后重新计算位移。
// 整个 Build 过程只读不写；只有 Apply 成功规划后才通过 Writer 落盘，
// 且先写跳板、再写入口跳转，保证任何失败都不会产生半成品写入。
package netsec

import (
	"errors"
	"fmt"
)

// 规划过程中可能返回的哨兵错误，调用方使用 errors.Is 判定具体类别。
var (
	// ErrInvalidConfig 表示规划配置本身不合法（空解码器、长度为零等）。
	ErrInvalidConfig = errors.New("netsec: invalid patch plan config")
	// ErrDecode 表示解码器无法在当前位置解码出一条完整指令，
	// 此时无法保证覆盖边界落在指令边界上，必须放弃规划。
	ErrDecode = errors.New("netsec: instruction decode failed")
	// ErrInteriorTarget 表示某个已知入口或分支目标严格落在覆盖区内部，
	// 覆盖后该目标将指向一条被截断的指令，必须拒绝。
	ErrInteriorTarget = errors.New("netsec: target falls inside covered region")
	// ErrDispRange 表示被覆盖指令复制到跳板后，重新计算的相对位移
	// 超出原指令位移字段的有符号表示范围（例如短跳 rel8 溢出）。
	ErrDispRange = errors.New("netsec: relocated displacement out of range")
	// ErrJumpRange 表示跳转编码器无法编码到达目标地址的跳转。
	ErrJumpRange = errors.New("netsec: jump target out of encoder reach")
	// ErrPatchTooLong 表示入口跳转编码后的长度超过覆盖区长度。
	ErrPatchTooLong = errors.New("netsec: entry jump longer than covered region")
	// ErrNoNop 表示入口跳转短于覆盖区，需要 NOP 填充但未提供 NopEncoder。
	ErrNoNop = errors.New("netsec: nop encoder required to pad covered region")
	// ErrOverlap 表示跳板区间与入口覆盖区间相交。
	ErrOverlap = errors.New("netsec: trampoline overlaps covered region")
	// ErrWrite 表示 Apply 阶段通过 Writer 写入失败。
	ErrWrite = errors.New("netsec: writer failed")
)

// Decoder 由调用方提供，按目标架构解码指定地址处的一条指令。
// 实现方自行持有镜像数据；规划器只依赖解码结果，不直接读取目标内存。
type Decoder interface {
	// DecodeAt 解码 addr 处的一条完整变长指令。
	// 指令被截断（镜像末尾字节不足）或操作码未知时应返回错误。
	DecodeAt(addr uint64) (Instr, error)
}

// Instr 描述一条解码后的指令。
type Instr struct {
	// Addr 是指令起始地址。
	Addr uint64
	// Bytes 是指令的完整机器码。规划器只复制、不改写该切片，
	// 重定位时在自己的副本上修改位移字段。
	Bytes []byte
	// Branch 非空时表示该指令携带相对位移控制流（跳转/调用）。
	Branch *Branch
}

// Branch 描述一条相对控制流指令的位移字段。
// 位移语义采用“相对指令末尾”的有符号位移（x86/AMD64 惯例），
// 字节序按小端编码；其他架构由调用方的解码器适配成该模型。
type Branch struct {
	// Target 是原指令处跳转到的绝对目标地址。
	Target uint64
	// DispSize 是位移字段的字节宽度，如短跳为 1、近跳为 4。
	// 支持 1、2、4、8 字节，有符号范围由宽度推导。
	DispSize int
	// DispEnd 是位移字段结束位置相对指令起始的字节偏移，
	// 字段区间为 [DispEnd-DispSize, DispEnd)。
	DispEnd int
}

// JumpEncoder 由调用方提供，把一条“跳转到 target”的指令编码到
// 地址 addr 处。max 为允许占用的最大字节数；max<=0 表示不限制长度，
// 但编码器仍应对不可达目标返回 ErrJumpRange。
type JumpEncoder interface {
	EncodeJump(addr, target uint64, max int) ([]byte, error)
}

// NopEncoder 由调用方提供，生成恰好 n 字节的 NOP 序列，
// 用于把入口跳转填充到完整覆盖区长度。
type NopEncoder interface {
	EncodeNop(n int) ([]byte, error)
}

// Writer 是 Apply 阶段唯一的落盘通道，语义同 io.WriterAt。
type Writer interface {
	WriteAt(addr uint64, p []byte) error
}

// Config 描述一次入口热补丁规划的输入。
type Config struct {
	// Entry 是被覆盖的函数入口绝对地址。
	Entry uint64
	// MinPatchLen 是覆盖区至少需要的字节数（通常为入口跳转宽度）。
	// 实际覆盖长度会向上取整到整条指令边界。
	MinPatchLen int
	// Decoder 是调用方提供的指令解码器，必填。
	Decoder Decoder
	// Jump 用于编码入口跳转与跳板回跳，必填。
	Jump JumpEncoder
	// Nop 在入口跳转短于覆盖区时填充剩余字节；可能需要填充时必填。
	Nop NopEncoder
	// TrampAddr 是跳板的起始绝对地址，不得与覆盖区相交。
	TrampAddr uint64
	// KnownTargets 是覆盖区外已知的其他函数入口、全局分支目标等。
	// 任何严格落入覆盖区内部的地址都会导致规划失败。
	KnownTargets []uint64
}

// Plan 是规划成功后的产物。Build 不产生任何写入，
// 调用方检查无误后再调用 Apply 落盘。
type Plan struct {
	// Entry 是被覆盖的函数入口地址。
	Entry uint64
	// StolenLen 是实际覆盖长度，始终为整条指令长度之和且 >= MinPatchLen。
	StolenLen int
	// TrampAddr 是跳板起始地址。
	TrampAddr uint64
	// Patch 是写入 Entry 的字节（入口跳转 + 必要的 NOP 填充），
	// 长度恰为 StolenLen。
	Patch []byte
	// Trampoline 是写入 TrampAddr 的跳板字节：被覆盖指令的重定位副本，
	// 末尾附一条跳回 Entry+StolenLen 的跳转。
	Trampoline []byte
	// Instrs 是被搬走指令的解码视图，按地址升序排列。
	Instrs []Instr
}

// Build 依据 cfg 规划入口热补丁。
//
// 执行步骤：
//  1. 从 Entry 起逐条解码，累积到总长度 >= MinPatchLen，
//     保证覆盖边界落在指令边界上，绝不截断一条变长指令；
//  2. 拒绝任何严格落入覆盖区内部的已知入口或分支目标；
//  3. 把被覆盖指令复制到跳板，相对跳转按新位置重算位移并做范围检查；
//  4. 跳板末尾编码回跳到覆盖区之后，并校验跳板与覆盖区不相交；
//  5. 编码入口跳转，必要时用 NOP 填充到 StolenLen。
//
// Build 全程不调用 Writer、不修改解码器返回的原始字节。
func Build(cfg Config) (*Plan, error) {
	if cfg.MinPatchLen <= 0 {
		return nil, fmt.Errorf("%w: MinPatchLen must be positive", ErrInvalidConfig)
	}
	if cfg.Decoder == nil {
		return nil, fmt.Errorf("%w: Decoder is required", ErrInvalidConfig)
	}
	if cfg.Jump == nil {
		return nil, fmt.Errorf("%w: Jump encoder is required", ErrInvalidConfig)
	}

	// 步骤 1：逐条累积，覆盖长度只在指令边界上收敛。
	instrs, stolen, err := stealInstructions(cfg)
	if err != nil {
		return nil, err
	}
	regionEnd := cfg.Entry + uint64(stolen)

	// 步骤 2：覆盖区内部不得存在任何已知入口或分支目标。
	if err := rejectInteriorTargets(cfg, instrs, regionEnd); err != nil {
		return nil, err
	}

	// 步骤 3：复制指令到跳板，重定位相对位移。
	trampoline, cursor, err := buildTrampoline(cfg, instrs)
	if err != nil {
		return nil, err
	}

	// 步骤 4：跳板末尾回跳到覆盖区之后的第一条原指令。
	jumpBack, err := cfg.Jump.EncodeJump(cursor, regionEnd, 0)
	if err != nil {
		return nil, fmt.Errorf("encode trampoline jump back to %#x: %w", regionEnd, err)
	}
	if len(jumpBack) == 0 {
		return nil, fmt.Errorf("%w: encoder returned empty jump", ErrJumpRange)
	}
	trampoline = append(trampoline, jumpBack...)

	if rangesOverlap(cfg.Entry, uint64(stolen), cfg.TrampAddr, uint64(len(trampoline))) {
		return nil, fmt.Errorf("%w: trampoline [%#x,%#x) intersects region [%#x,%#x)",
			ErrOverlap, cfg.TrampAddr, cfg.TrampAddr+uint64(len(trampoline)), cfg.Entry, regionEnd)
	}

	// 步骤 5：入口跳转必须放进覆盖区，不足部分用 NOP 填充。
	patch, err := buildEntryPatch(cfg, stolen)
	if err != nil {
		return nil, err
	}

	return &Plan{
		Entry:      cfg.Entry,
		StolenLen:  stolen,
		TrampAddr:  cfg.TrampAddr,
		Patch:      patch,
		Trampoline: trampoline,
		Instrs:     instrs,
	}, nil
}

// Apply 按“先跳板、后入口”的顺序写入规划结果。
// 跳板写入失败时入口覆盖区保持原样；只有跳板就位后才改写入口，
// 因此任何失败路径都不会把执行流引向未就绪的跳板。
func (p *Plan) Apply(w Writer) error {
	if w == nil {
		return fmt.Errorf("%w: Writer is required", ErrInvalidConfig)
	}
	if err := w.WriteAt(p.TrampAddr, p.Trampoline); err != nil {
		return fmt.Errorf("write trampoline at %#x: %w: %v", p.TrampAddr, ErrWrite, err)
	}
	if err := w.WriteAt(p.Entry, p.Patch); err != nil {
		return fmt.Errorf("write entry patch at %#x: %w: %v", p.Entry, ErrWrite, err)
	}
	return nil
}

// stealInstructions 从入口逐条解码，直到累积长度 >= MinPatchLen。
func stealInstructions(cfg Config) ([]Instr, int, error) {
	var instrs []Instr
	off := 0
	for off < cfg.MinPatchLen {
		addr := cfg.Entry + uint64(off)
		ins, err := cfg.Decoder.DecodeAt(addr)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: decode at %#x: %v", ErrDecode, addr, err)
		}
		if ins.Addr != addr {
			return nil, 0, fmt.Errorf("%w: decoder returned addr %#x, want %#x", ErrDecode, ins.Addr, addr)
		}
		if len(ins.Bytes) == 0 {
			return nil, 0, fmt.Errorf("%w: decoder returned empty instruction at %#x", ErrDecode, addr)
		}
		if ins.Branch != nil {
			if err := validateBranch(ins); err != nil {
				return nil, 0, err
			}
		}
		instrs = append(instrs, ins)
		off += len(ins.Bytes)
	}
	return instrs, off, nil
}

// validateBranch 校验位移字段描述自洽且宽度受支持。
func validateBranch(ins Instr) error {
	b := ins.Branch
	switch b.DispSize {
	case 1, 2, 4, 8:
	default:
		return fmt.Errorf("%w: unsupported displacement size %d at %#x", ErrDecode, b.DispSize, ins.Addr)
	}
	if b.DispEnd < b.DispSize || b.DispEnd > len(ins.Bytes) {
		return fmt.Errorf("%w: displacement field [%d,%d) out of instruction at %#x",
			ErrDecode, b.DispEnd-b.DispSize, b.DispEnd, ins.Addr)
	}
	return nil
}

// rejectInteriorTargets 检查已知目标与每条被覆盖指令的分支目标。
// 区间端点（Entry 本身、覆盖区之后的第一条指令地址）是合法目标。
func rejectInteriorTargets(cfg Config, instrs []Instr, regionEnd uint64) error {
	for _, target := range cfg.KnownTargets {
		if cfg.Entry < target && target < regionEnd {
			return fmt.Errorf("%w: known target %#x lies inside [%#x,%#x)",
				ErrInteriorTarget, target, cfg.Entry, regionEnd)
		}
	}
	for _, ins := range instrs {
		if ins.Branch == nil {
			continue
		}
		target := ins.Branch.Target
		if cfg.Entry < target && target < regionEnd {
			return fmt.Errorf("%w: branch at %#x targets %#x inside covered region",
				ErrInteriorTarget, ins.Addr, target)
		}
	}
	return nil
}

// buildTrampoline 逐条复制被覆盖指令；相对跳转按跳板上的新位置重算位移，
// 并立即检查是否仍在原位移字段的有符号范围内。
func buildTrampoline(cfg Config, instrs []Instr) ([]byte, uint64, error) {
	out := make([]byte, 0, len(instrs)*4)
	cursor := cfg.TrampAddr
	for _, ins := range instrs {
		n := len(ins.Bytes)
		chunk := append([]byte(nil), ins.Bytes...)
		if ins.Branch != nil {
			b := ins.Branch
			newDisp := int64(b.Target) - int64(cursor+uint64(n))
			if err := checkDisplacement(b.DispSize, newDisp); err != nil {
				return nil, 0, fmt.Errorf("relocate branch at %#x -> %#x: %w",
					ins.Addr, b.Target, err)
			}
			putDisplacement(chunk[b.DispEnd-b.DispSize:b.DispEnd], newDisp, b.DispSize)
		}
		out = append(out, chunk...)
		cursor += uint64(n)
	}
	return out, cursor, nil
}

// buildEntryPatch 编码入口跳转并把它填充到恰好 stolen 字节。
func buildEntryPatch(cfg Config, stolen int) ([]byte, error) {
	jump, err := cfg.Jump.EncodeJump(cfg.Entry, cfg.TrampAddr, stolen)
	if err != nil {
		return nil, fmt.Errorf("encode entry jump to %#x: %w", cfg.TrampAddr, err)
	}
	if len(jump) > stolen {
		return nil, fmt.Errorf("%w: jump %d bytes > covered region %d bytes",
			ErrPatchTooLong, len(jump), stolen)
	}
	if len(jump) == stolen {
		return jump, nil
	}
	if cfg.Nop == nil {
		return nil, fmt.Errorf("%w: %d nop bytes needed but no NopEncoder provided",
			ErrNoNop, stolen-len(jump))
	}
	nops, err := cfg.Nop.EncodeNop(stolen - len(jump))
	if err != nil {
		return nil, fmt.Errorf("encode %d nop bytes: %w", stolen-len(jump), err)
	}
	if len(nops) != stolen-len(jump) {
		return nil, fmt.Errorf("%w: nop encoder returned %d bytes, want %d",
			ErrInvalidConfig, len(nops), stolen-len(jump))
	}
	return append(append([]byte(nil), jump...), nops...), nil
}

// checkDisplacement 检查 disp 是否落在 width 字节的有符号范围内。
func checkDisplacement(width int, disp int64) error {
	bits := uint(width) * 8
	limit := int64(1) << (bits - 1)
	if disp < -limit || disp > limit-1 {
		return fmt.Errorf("%w: displacement %d does not fit int%d", ErrDispRange, disp, bits)
	}
	return nil
}

// putDisplacement 按小端写入有符号位移，width 只取 1/2/4/8。
func putDisplacement(field []byte, disp int64, width int) {
	for i := 0; i < width; i++ {
		field[i] = byte(disp >> (8 * uint(i)))
	}
}

// rangesOverlap 判断两个半开区间 [a,a+an) 与 [b,b+bn) 是否相交。
func rangesOverlap(a uint64, an uint64, b uint64, bn uint64) bool {
	return a < b+bn && b < a+an
}
