package leaklab

// 示例程序使用的寄存器约定（仅示例自身约束，与机器语义无关）。
const (
	RPub    uint8 = 0 // 公开输入：越界查表的下标
	RZero   uint8 = 1 // 恒零，用于合成无条件跳转
	RTrash  uint8 = 2 // 仅在推测窗口存活的临时寄存器（回滚后必须为零）
	RAddr   uint8 = 3 // 仅在推测窗口存活的访存地址
	RSecret uint8 = 4 // 寄存器版秘密
	RPtr    uint8 = 5 // 持有内存槽地址的寄存器
)

// 示例输入布局使用的固定内存槽。
const (
	MemSecret uint8 = 40 // 秘密字节所在内存地址
	MemPublic uint8 = 41 // 公开字节所在内存地址（内存版示例）
)

// OOBIndex 是示例中触发越界的公开下标：16 项表的合法下标为 0..15。
const OOBIndex uint8 = 20

// LeakySpecMem 是越界查表示例的输入布局：
// 一个公开字节（内存 41）与一个秘密字节（内存 40），位置互不重叠。
func LeakySpecMem() InputSpec {
	return InputSpec{
		Public: []Slot{{Kind: SlotMem, Index: MemPublic}},
		Secret: Slot{Kind: SlotMem, Index: MemSecret},
	}
}

// LeakySpecReg 是寄存器版输入布局，用于覆盖“输入位于寄存器”的路径。
func LeakySpecReg() InputSpec {
	return InputSpec{
		Public: []Slot{{Kind: SlotReg, Index: RPub}},
		Secret: Slot{Kind: SlotReg, Index: RSecret},
	}
}

// LeakyProgram 返回“越界查表被分支预测隐藏”的示例程序（内存槽输入版）。
//
// 序言把公开字节从内存 41 装入 r0；守卫在公开下标等于 20（越界）时为真，
// 架构正确路径跳到 safe_halt，fallthrough 是架构不可达的越界查表块。该跳转
// 一旦被误预测，推测窗口沿相反分支进入越界块：从内存 40 读秘密、加进越界
// 地址后 load + probe，probe 桶随秘密变化，寄存器/内存修改在窗口后回滚。
// 秘密只在推测窗口内被读入寄存器，架构路径从不触碰，故回滚后无任何架构
// 寄存器持有秘密。由于越界块不在架构正确路径上，架构 probe 序列恒为空。
//
// 插入 withBarrier 后，推测窗口第一条指令即 barrier，泄漏被封堵。
func LeakyProgram(withBarrier bool) *Program {
	text := "const r5, " + itoa(MemPublic) + "\n" +
		"load r0, r5\n" + // 公开下标进入 r0
		"const r2, " + itoa(OOBIndex) + "\n" +
		"jmpeq r0, r2, safe_halt\n"
	if withBarrier {
		text += "barrier\n"
	}
	text += "const r5, " + itoa(MemSecret) + "\n" + // 推测块：读秘密
		"load r2, r5\n" +
		"add r3, r0, r2\n" +
		"load r2, r3\n" +
		"probe r3\n" +
		"jmpeq r1, r1, safe_halt\n" +
		"safe_halt:\n" +
		"halt\n"
	prog, err := ParseProgram(text)
	if err != nil {
		panic(err) // 仅由本包内静态构建触发，构建正确时不会发生
	}
	return prog
}

// LeakyProgramReg 返回寄存器槽输入版：公开字节与秘密都在寄存器中，
// 用于覆盖“输入位于寄存器”的路径；秘密寄存器在架构路径上不被读取。
func LeakyProgramReg(withBarrier bool) *Program {
	b := NewAsmBuilder()
	b.Const(RTrash, OOBIndex)
	b.JmpEqLabel(RPub, RTrash, "safe_halt")
	if withBarrier {
		b.Barrier()
	}
	// r4 为秘密寄存器，仅在推测窗口参与地址计算，回滚后 r2/r3 复原。
	b.Add(RAddr, RPub, RSecret)
	b.Load(RTrash, RAddr)
	b.Probe(RAddr)
	b.JmpEqLabel(RZero, RZero, "safe_halt")
	b.Label("safe_halt")
	b.Halt()
	prog, err := b.Build()
	if err != nil {
		panic(err)
	}
	return prog
}

// PublicLeakProgram 返回“直接公开泄密”对照程序：probe 地址只由公开输入决定，
// 秘密既不进入架构 probe 也不进入推测 probe，因此不构成本题漏洞。
func PublicLeakProgram() *Program {
	text := "const r5, " + itoa(MemPublic) + "\n" +
		"load r0, r5\n" +
		"const r2, " + itoa(OOBIndex) + "\n" +
		"jmpeq r0, r2, safe_halt\n" +
		// 推测块只使用公开下标，不触碰秘密，两侧秘密的 probe 完全相同。
		"load r2, r0\n" +
		"probe r0\n" +
		"jmpeq r1, r1, safe_halt\n" +
		"safe_halt:\n" +
		"halt\n"
	prog, err := ParseProgram(text)
	if err != nil {
		panic(err)
	}
	return prog
}

// LeakyProgramText 返回泄漏版示例的规范汇编文本，便于文档与 CLI 展示。
func LeakyProgramText() string {
	return LeakyProgram(false).Text()
}

func itoa(v uint8) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
