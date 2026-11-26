package dfa

import (
	"fmt"
	"sort"
)

// ROMRange 是原程序中禁止修改的只读指令区间，采用半开区间 [Start, End)。
// 插入点若落在该区间内（Start <= index < End），对应候选必须被排除。
type ROMRange struct {
	Start int
	End   int
}

// Gadget 是一段可复用的保护片段（符号化汇编）。片段内部标签必须以
// "g_" 开头，结尾通过 `jmp g_ok` 回到原程序的正常控制流；拼接器会
// 自动重命名标签，避免与原程序或其他片段冲突。
type Gadget struct {
	Name     string
	Source   string
	Scratch  []int // 片段会写坏的布尔寄存器（供文档/宿主展示）
	MinFault int   // 理论上绕过该片段所需的最少故障数
}

// Candidate 是一个带代价权重的候选插入点。
type Candidate struct {
	ID     string
	Index  int // 原程序指令下标：片段插在该下标指令之前
	Cost   int
	Gadget Gadget
}

// SigGuard 是单守卫片段：独立重读一次验签位，失败即 halt。
// 一次条件反转或一次 skip 即可击穿，仅作为对照基线。
func SigGuard(scratch int) Gadget {
	return Gadget{
		Name: "SigGuard",
		Source: fmt.Sprintf(`
lds r%d
jt r%d, g_ok
halt
g_ok:
`, scratch, scratch),
		Scratch:  []int{scratch},
		MinFault: 1,
	}
}

// TripleSigGuard 串联三个互相独立的验签守卫，分别使用不同的临时寄存器。
// 布局采用瀑布式串联：每个 jt 仅在验签通过时跳到下一道守卫的入口，
// 反转条件只会落到紧邻的 halt 上，而不是跳到共享汇聚点。因此每道守卫
// 都至少需要一次独立故障（反转 jt、skip lds 或翻转临时寄存器）才能越过，
// K=2 的攻击者无法击穿三道守卫。这正是对“单点保护被双跳/双故障击穿”
// 的直接修补原语。
func TripleSigGuard(scratch [3]int) Gadget {
	s := "lds r0\njt r0, g1\nhalt\n"
	s += "g1:\nlds r1\njt r1, g2\nhalt\n"
	s += "g2:\nlds r2\njt r2, g_ok\nhalt\n"
	s += "g_ok:\n"
	s = replaceRegs(s, scratch)
	return Gadget{
		Name:     "TripleSigGuard",
		Source:   s,
		Scratch:  append([]int(nil), scratch[:]...),
		MinFault: 3,
	}
}

func replaceRegs(src string, regs [3]int) string {
	out := src
	for i, r := range regs {
		placeholder := fmt.Sprintf("r%d", i)
		replacement := fmt.Sprintf("r%d", r)
		out = replaceToken(out, placeholder, replacement)
	}
	return out
}

func replaceToken(s, old, new string) string {
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// CounterGate 演示 4 位计数器 inc/dec/jz 的正确用法与“计数器语义零污染”
// 的拼接方式：假设进入时计数器为 0，先 inc k 次，再循环 dec 直到回到 0
// 才顺序落到 g_ok，成功路径末尾计数器自然恢复为进入时的值。
//
// 安全边界（双故障分析的重要结论）：本 ISA 只有一个共享计数器，任何
// 计数器关卡都能被一次 jz 条件反转、或一次 inc/dec skip 击穿，因此
// CounterGate 不构成故障防护原语，MinFault 如实标记为 1 仅表示“无故障
// 时不可越过”。抵御 K=2 必须使用 TripleSigGuard 这类基于独立只读 lds
// 的瀑布守卫——每个守卫读取攻击者无法篡改的验签位，故障无法在守卫间共享。
func CounterGate(k int) (Gadget, error) {
	if k < 1 || k > MaxInstrs/3 {
		return Gadget{}, asmErr(0, "CounterGate 关卡数 %d 非法", k)
	}
	s := ""
	for i := 0; i < k; i++ {
		s += "inc\n"
	}
	s += "g_loop:\n"
	s += "dec\n"
	s += "jz g_ok\n"
	s += "jmp g_loop\n"
	s += "g_ok:\n"
	// 每一道 dec/jz 对应一个关卡；halt 独立成块，避免共享标签被双跳利用。
	return Gadget{
		Name:     fmt.Sprintf("CounterGate%d", k),
		Source:   s,
		MinFault: 1,
	}, nil
}

// InROM 判断原程序下标 idx 是否落在任一只读区间内。
func InROM(idx int, ranges []ROMRange) bool {
	for _, r := range ranges {
		if idx >= r.Start && idx < r.End {
			return true
		}
	}
	return false
}

// ExcludedCandidate 记录候选被排除的原因。
type ExcludedCandidate struct {
	Candidate Candidate
	Reason    string
}

// Splice 把给定候选按 Index 稳定排序后插入原程序，返回链接后的新程序。
// 插入只新增指令，绝不修改原程序任何指令字节，因此只读 ROM 区间天然
// 不被改写；调用方仍应先用 InROM 过滤候选。
func Splice(base *Program, chosen []Candidate) (*Program, error) {
	basePT, err := ParseText(base.Disassemble())
	if err != nil {
		return nil, err
	}
	ordered := append([]Candidate(nil), chosen...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Index != ordered[j].Index {
			return ordered[i].Index < ordered[j].Index
		}
		return ordered[i].ID < ordered[j].ID
	})

	out := &ProgramText{Instrs: []Instr{}, Labels: map[string]int{}}
	// 原程序标签保留原名；片段标签统一加 "c<id>_" 前缀，g_ok/g_cont/g_fail
	// 也一并重命名，保证多片段共存无冲突。
	for name, addr := range basePT.Labels {
		out.Labels[name] = addr // 地址先占位，插入后统一重算。
	}
	insertAt := map[int][]Candidate{}
	for _, c := range ordered {
		if c.Index < 0 || c.Index > len(basePT.Instrs) {
			return nil, asmErr(0, "候选 %s 的插入下标 %d 越界", c.ID, c.Index)
		}
		insertAt[c.Index] = append(insertAt[c.Index], c)
	}

	// 先把原标签的地址清空，按最终发射顺序重新登记。
	out.Labels = map[string]int{}
	for idx, ins := range basePT.Instrs {
		// 该下标前可能挂了多个片段。
		for _, c := range insertAt[idx] {
			if err := emitGadget(out, c); err != nil {
				return nil, err
			}
		}
		// 若原指令处定义了标签，在发射该指令前登记新地址。
		for name, addr := range basePT.Labels {
			if addr == idx {
				out.Labels[name] = len(out.Instrs)
			}
		}
		out.Instrs = append(out.Instrs, ins)
	}
	// 允许在程序末尾插入。
	for _, c := range insertAt[len(basePT.Instrs)] {
		if err := emitGadget(out, c); err != nil {
			return nil, err
		}
	}

	if len(out.Instrs) > MaxInstrs {
		return nil, asmErr(0, "修补后共 %d 条指令，超过平台上限 %d", len(out.Instrs), MaxInstrs)
	}
	return out.Link(base.Width)
}

func emitGadget(out *ProgramText, c Candidate) error {
	pt, err := ParseText(c.Gadget.Source)
	if err != nil {
		return fmt.Errorf("候选 %s 片段解析失败: %w", c.ID, err)
	}
	prefix := "c" + sanitizeIdent(c.ID) + "_"
	rename := map[string]string{}
	for name := range pt.Labels {
		rename[name] = prefix + name
	}
	// 先登记重命名后的标签地址。
	for name, localAddr := range pt.Labels {
		out.Labels[rename[name]] = len(out.Instrs) + localAddr
	}
	for _, ins := range pt.Instrs {
		if ins.TargetLabel != "" {
			mapped, ok := rename[ins.TargetLabel]
			if !ok {
				return asmErr(ins.Line, "候选 %s 引用了片段外标签 %q", c.ID, ins.TargetLabel)
			}
			ins.TargetLabel = mapped
		}
		out.Instrs = append(out.Instrs, ins)
	}
	return nil
}

func sanitizeIdent(id string) string {
	b := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		ch := id[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '_':
			b = append(b, ch)
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "x"
	}
	return string(b)
}
