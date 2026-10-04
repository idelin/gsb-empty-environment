package reorder

import (
	"fmt"
	"sort"
	"strings"
)

// CellDiff 一个最终状态单元上的分歧。
type CellDiff struct {
	Loc  Location
	Orig uint64
	Cand uint64
}

// Divergence 一次可观察的发散：初始状态 + 最终状态差异。
type Divergence struct {
	Init  *State
	Diffs []CellDiff
}

// ExhaustiveResult 有限初始状态核对结果。
type ExhaustiveResult struct {
	Tested     int         // 实际测试的初始状态数
	Exhaustive bool        // 是否覆盖了 domain^cells 的全部状态
	Divergence *Divergence // 发现的首个发散；nil 表示未发现
}

// TouchedCells 收集若干序列触及的全部寄存器与内存字节（排序后返回）。
func TouchedCells(seqs ...[]Inst) (regs []string, addrs []uint64) {
	regSet := make(map[string]bool)
	addrSet := make(map[uint64]bool)
	for _, seq := range seqs {
		for _, in := range seq {
			locs := append(in.Reads(), in.Writes()...)
			for _, l := range locs {
				if l.IsReg {
					regSet[l.Reg] = true
				} else {
					for a := l.Addr; a < l.Addr+l.Size; a++ {
						addrSet[a] = true
					}
				}
			}
		}
	}
	for r := range regSet {
		regs = append(regs, r)
	}
	sort.Strings(regs)
	for a := range addrSet {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	return regs, addrs
}

// ExhaustiveCheck 在有限取值域上枚举初始状态，分别执行两个序列并比较
// 最终状态（全部触及单元）。
//
// 当 domain^单元数 不超过 maxStates 时做完全穷举；否则退化为确定性模式
// （全同值、单点扫描）加固定种子伪随机采样，此时 Exhaustive=false。
//
// 注意：未发现发散不能证明两序列等价，更不能推翻依赖分析的结论——
// 碰巧结果相同不等于无依赖。
func ExhaustiveCheck(orig, cand []Inst, domain []uint64, maxStates int) ExhaustiveResult {
	if len(domain) == 0 {
		domain = []uint64{0}
	}
	regs, addrs := TouchedCells(orig, cand)
	n := len(regs) + len(addrs)

	total := 1
	exhaustive := true
	for k := 0; k < n; k++ {
		total *= len(domain)
		if total > maxStates {
			exhaustive = false
			break
		}
	}

	res := ExhaustiveResult{Exhaustive: exhaustive}
	vals := make([]uint64, n)

	try := func() bool {
		res.Tested++
		init := buildState(regs, addrs, vals)
		s1 := init.Clone()
		Execute(orig, s1)
		s2 := init.Clone()
		Execute(cand, s2)
		if diffs := diffStates(s1, s2, regs, addrs); len(diffs) > 0 {
			res.Divergence = &Divergence{Init: init, Diffs: diffs}
			return false
		}
		return true
	}

	if exhaustive {
		// 混合进制计数器枚举 domain^n
		for i := 0; i < total; i++ {
			x := i
			for c := 0; c < n; c++ {
				vals[c] = domain[x%len(domain)]
				x /= len(domain)
			}
			if !try() {
				return res
			}
		}
		return res
	}

	// 退化路径：确定性模式 + 伪随机采样，预算 maxStates。
	budget := maxStates
	zero := vals
	for c := range zero {
		zero[c] = domain[0]
	}
	patterns := [][]uint64{}
	for _, v := range domain {
		row := make([]uint64, n)
		for c := range row {
			row[c] = v
		}
		patterns = append(patterns, row)
	}
	for c := 0; c < n; c++ {
		for _, v := range domain {
			row := make([]uint64, n)
			copy(row, zero)
			row[c] = v
			patterns = append(patterns, row)
		}
	}
	for _, row := range patterns {
		if budget <= 0 {
			return res
		}
		budget--
		copy(vals, row)
		if !try() {
			return res
		}
	}
	// LCG 伪随机采样（固定种子，结果可复现）
	seed := uint64(0x9e3779b97f4a7c15)
	for budget > 0 {
		budget--
		for c := 0; c < n; c++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			vals[c] = domain[seed>>33%uint64(len(domain))]
		}
		if !try() {
			return res
		}
	}
	return res
}

func buildState(regs []string, addrs []uint64, vals []uint64) *State {
	s := NewState()
	for i, r := range regs {
		s.Regs[r] = vals[i]
	}
	for j, a := range addrs {
		s.Mem[a] = uint8(vals[len(regs)+j])
	}
	return s
}

func diffStates(a, b *State, regs []string, addrs []uint64) []CellDiff {
	var diffs []CellDiff
	for _, r := range regs {
		if a.Regs[r] != b.Regs[r] {
			diffs = append(diffs, CellDiff{Loc: Reg(r), Orig: a.Regs[r], Cand: b.Regs[r]})
		}
	}
	for _, addr := range addrs {
		if a.Mem[addr] != b.Mem[addr] {
			diffs = append(diffs, CellDiff{Loc: Mem(addr, 1), Orig: uint64(a.Mem[addr]), Cand: uint64(b.Mem[addr])})
		}
	}
	return diffs
}

// Describe 人类可读地描述一次发散。
func (d *Divergence) Describe() string {
	var sb strings.Builder
	sb.WriteString("初始状态：")
	first := true
	write := func(name string, v uint64) {
		if !first {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString(fmt.Sprintf("%s=%d", name, v))
	}
	regs := make([]string, 0, len(d.Init.Regs))
	for r := range d.Init.Regs {
		regs = append(regs, r)
	}
	sort.Strings(regs)
	for _, r := range regs {
		write(r, d.Init.Regs[r])
	}
	addrs := make([]uint64, 0, len(d.Init.Mem))
	for a := range d.Init.Mem {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	for _, a := range addrs {
		write(fmt.Sprintf("[%d]", a), uint64(d.Init.Mem[a]))
	}
	sb.WriteString("\n最终状态分歧：")
	for i, df := range d.Diffs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s: 原始=%d 候选=%d", df.Loc, df.Orig, df.Cand))
	}
	return sb.String()
}
