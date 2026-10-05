package sccp

import (
	"fmt"
	"math/rand"
)

// ValidationResult 汇总穷举执行对照的结果。
type ValidationResult struct {
	Runs       int      // 实际执行的输入向量数
	EdgeChecks int      // 经过的边检查次数
	ValChecks  int      // 操作数常量核对次数
	Violations []string // 所有反例描述
}

// OK 报告对照是否全部通过。
func (r ValidationResult) OK() bool { return len(r.Violations) == 0 }

// claimKey 定位一处常量替换断言。
type claimKey struct {
	block int
	instr int
	slot  string
}

// ValidateExhaustive 用具体执行对照 SCCP 的输出：
//
//   - 任何被判定删除的边，在所有具体执行中都不得被经过；
//   - 任何常量替换断言，在具体执行到该程序点时必须与实际值一致。
//
// 输入变量是 8 位的，输入空间为 256^n。当总空间不超过 maxRuns 时
// 做完全穷举；否则先用边界值集合 {0,1,2,127,128,255} 做笛卡尔积，
// 仍超出则用固定种子的随机抽样补足。循环可能不终止，每次执行
// 受 stepBound 限制；超限前经过的边与值仍然参与对照。
func ValidateExhaustive(cfg *CFG, a *Analyzer, rep *Report, maxRuns, stepBound int) ValidationResult {
	deleted := make(map[Edge]string, len(rep.DeletedEdges))
	for _, d := range rep.DeletedEdges {
		deleted[Edge{d.FromID, d.ToID}] = d.Reason
	}
	claims := make(map[claimKey]ConstReplacement, len(rep.Replacements))
	for _, r := range rep.Replacements {
		claims[claimKey{r.BlockID, r.Instr, r.Slot}] = r
	}

	var res ValidationResult
	for _, vec := range inputVectors(cfg.Inputs, maxRuns) {
		inputs := make(map[string]uint8, len(cfg.Inputs))
		for i, name := range cfg.Inputs {
			inputs[name] = vec[i]
		}
		res.Runs++

		opHook := func(blockID, instr int, slot string, op Operand, concrete uint8) {
			if op.IsConst {
				return
			}
			if c, ok := claims[claimKey{blockID, instr, slot}]; ok {
				res.ValChecks++
				if concrete != c.Value {
					res.Violations = append(res.Violations, fmt.Sprintf(
						"输入 %v：块 %s 指令 %d 槽位 %s：SCCP 断言 %s=%d，具体执行为 %d",
						vec, cfg.block(blockID).Name, instr, slot, c.Var, c.Value, concrete))
				}
			}
		}
		edgeHook := func(e Edge) {
			res.EdgeChecks++
			if reason, bad := deleted[e]; bad {
				res.Violations = append(res.Violations, fmt.Sprintf(
					"输入 %v：具体执行经过了被删除的边 %s -> %s（删除理由：%s）",
					vec, cfg.block(e.From).Name, cfg.block(e.To).Name, reason))
			}
		}
		Run(cfg, inputs, stepBound, opHook, edgeHook)
	}
	return res
}

// inputVectors 生成输入向量集合。
func inputVectors(inputs []string, maxRuns int) [][]uint8 {
	if len(inputs) == 0 {
		return [][]uint8{{}}
	}
	// 完全穷举：256^n <= maxRuns。
	total := 1
	for range inputs {
		total *= 256
	}
	if total <= maxRuns {
		return enumerate(len(inputs), nil)
	}
	// 边界值笛卡尔积。
	interesting := []uint8{0, 1, 2, 127, 128, 255}
	if pow(len(interesting), len(inputs)) <= maxRuns {
		return enumerateValues(len(inputs), interesting)
	}
	// 固定种子随机抽样。
	rng := rand.New(rand.NewSource(42))
	out := make([][]uint8, 0, maxRuns)
	for len(out) < maxRuns {
		vec := make([]uint8, len(inputs))
		for i := range vec {
			vec[i] = uint8(rng.Intn(256))
		}
		out = append(out, vec)
	}
	return out
}

func pow(base, exp int) int {
	p := 1
	for i := 0; i < exp; i++ {
		p *= base
	}
	return p
}

// enumerate 生成 n 个输入变量的全部 256^n 种组合。
func enumerate(n int, prefix []uint8) [][]uint8 {
	if n == 0 {
		return [][]uint8{append([]uint8(nil), prefix...)}
	}
	var out [][]uint8
	for v := 0; v < 256; v++ {
		out = append(out, enumerate(n-1, append(prefix, uint8(v)))...)
	}
	return out
}

// enumerateValues 生成给定候选值集合的笛卡尔积。
func enumerateValues(n int, values []uint8) [][]uint8 {
	if n == 0 {
		return [][]uint8{{}}
	}
	var out [][]uint8
	var rec func(prefix []uint8, k int)
	rec = func(prefix []uint8, k int) {
		if k == 0 {
			out = append(out, append([]uint8(nil), prefix...))
			return
		}
		for _, v := range values {
			rec(append(prefix, v), k-1)
		}
	}
	rec(nil, n)
	return out
}
