package reorder

import "fmt"

// CheckResult 重排检查结果。
type CheckResult struct {
	Permutation bool   // 候选是否为原始序列的排列
	MatchErr    string // 非排列时的原因
	IDOfCand    []int  // 候选位置 -> 原始序号
	PosOfOrig   []int  // 原始序号 -> 候选位置
	Violated    []Edge // 被违反的依赖边，按 (From, To) 升序
}

// Legal 报告候选是否为合法重排。
func (r CheckResult) Legal() bool {
	return r.Permutation && len(r.Violated) == 0
}

// Earliest 返回最早冲突依赖边（原始序列中源指令最靠前者）；无冲突时返回 false。
func (r CheckResult) Earliest() (Edge, bool) {
	if len(r.Violated) == 0 {
		return Edge{}, false
	}
	return r.Violated[0], true
}

// Check 验证 cand 是否为 orig 的合法重排。
//
// 首先按指令规范文本做多重集匹配（相同文本按出现次序配对），
// 确认候选是原始序列的排列；然后逐条检查原始序列的依赖边，
// 要求边两端在候选中保持相同的先后关系。
func Check(orig, cand []Inst) CheckResult {
	res := CheckResult{Permutation: true}
	if len(cand) != len(orig) {
		res.Permutation = false
		res.MatchErr = fmt.Sprintf("指令数不一致：原始 %d 条，候选 %d 条", len(orig), len(cand))
		return res
	}
	byText := make(map[string][]int)
	for idx, in := range orig {
		key := in.String()
		byText[key] = append(byText[key], idx)
	}
	used := make(map[string]int)
	res.IDOfCand = make([]int, len(cand))
	for pos, in := range cand {
		key := in.String()
		ids := byText[key]
		k := used[key]
		if k >= len(ids) {
			res.Permutation = false
			res.MatchErr = fmt.Sprintf("候选第 %d 条 %q 在原始序列中没有可匹配的指令", pos+1, key)
			return res
		}
		res.IDOfCand[pos] = ids[k]
		used[key] = k + 1
	}
	res.PosOfOrig = make([]int, len(orig))
	for pos, id := range res.IDOfCand {
		res.PosOfOrig[id] = pos
	}
	for _, e := range ComputeEdges(orig) {
		if res.PosOfOrig[e.From] >= res.PosOfOrig[e.To] {
			res.Violated = append(res.Violated, e)
		}
	}
	return res
}
