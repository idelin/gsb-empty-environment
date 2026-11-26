package spp

import (
	"fmt"
	"strings"
)

// RenderText 输出中文结构化分析报告。
func (r *Report) RenderText() string {
	var b strings.Builder
	name := r.InstanceName
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(&b, "===== SPP 稳定性分析报告: %s =====\n", name)
	fmt.Fprintf(&b, "目标前缀源站 d = AS%d\n", r.Destination)
	fmt.Fprintf(&b, "结局分类: %s\n", r.OutcomeText)
	if r.Truncated {
		b.WriteString("警告: 枚举命中规模上限，结果可能不完整\n")
	}

	b.WriteString("\n-- 1. Gao-Rexford 无谷核验 --\n")
	if len(r.GR.CommercialCycles) == 0 {
		b.WriteString("客户-供应商商业环: 无\n")
	} else {
		fmt.Fprintf(&b, "客户-供应商商业环: 检测到 %d 个\n", len(r.GR.CommercialCycles))
		for _, c := range r.GR.CommercialCycles {
			fmt.Fprintf(&b, "  商业环: %s\n", joinAS(c))
		}
	}
	if len(r.GR.ValleyFreeViolations) == 0 {
		b.WriteString("无谷/导出过滤违规: 无\n")
	} else {
		fmt.Fprintf(&b, "无谷/导出过滤违规: %d 条\n", len(r.GR.ValleyFreeViolations))
		for _, v := range r.GR.ValleyFreeViolations {
			fmt.Fprintf(&b, "  AS%d 路径 [%s]: %s\n", v.AS, v.Path, v.Reason)
		}
	}
	if len(r.GR.MissingLegalPaths) > 0 {
		fmt.Fprintf(&b, "未纳入偏好序的合法无谷路径: %d 条\n", len(r.GR.MissingLegalPaths))
		for _, gap := range r.GR.MissingLegalPaths {
			fmt.Fprintf(&b, "  AS%d 缺少 [%s]\n", gap.AS, gap.Path)
		}
	}

	b.WriteString("\n-- 2. 稳定路径分配 (SPA) --\n")
	if len(r.StableAssignments) == 0 {
		b.WriteString("不存在任何稳态分配：系统必然永久震荡\n")
	} else {
		for i, a := range r.StableAssignments {
			fmt.Fprintf(&b, "稳态 #%d:\n", i+1)
			for _, as := range sortedAssignmentKeys(a) {
				p := a[as]
				if p == nil {
					fmt.Fprintf(&b, "  AS%d -> 无可用路由\n", as)
				} else {
					fmt.Fprintf(&b, "  AS%d -> [%s]\n", as, p)
				}
			}
		}
	}

	b.WriteString("\n-- 3. 争议轮 (Dispute Wheel) --\n")
	if len(r.Wheels) == 0 {
		b.WriteString("未构造出争议轮\n")
	} else {
		for i, w := range r.Wheels {
			fmt.Fprintf(&b, "争议轮 #%d (k=%d) pivot 环: %s\n", i+1, w.Size(), joinAS(w.Pivots))
			for j, u := range w.Pivots {
				fmt.Fprintf(&b, "  Q_%d @AS%d = [%s] (rank=%d)\n", j, u, w.Spokes[j], w.SpokeRanks[j])
				fmt.Fprintf(&b, "  R_%d @AS%d = [%s] (rank=%d)\n", j, u, w.Rims[j], w.RimRanks[j])
			}
			b.WriteString("  循环偏好证明:\n")
			for _, line := range w.Proof {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}

	b.WriteString("\n-- 4. 最小策略调整 --\n")
	if r.Repair == nil {
		b.WriteString("无需修复或在动作上限内未找到可行修复\n")
	} else {
		fmt.Fprintf(&b, "修复动作数: %d\n", len(r.Repair.Actions))
		for _, d := range r.Repair.ActionDescriptions {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
		fmt.Fprintf(&b, "修复后剩余争议轮: %d\n", r.Repair.RemainingWheels)
		fmt.Fprintf(&b, "修复后稳态数量: %d (唯一稳态=%v)\n", len(r.Repair.StableAssignments), r.Repair.UniqueStable)
		fmt.Fprintf(&b, "修复后严格无谷: %v\n", r.Repair.RestoresValleyFree)
		for i, a := range r.Repair.StableAssignments {
			fmt.Fprintf(&b, "修复后稳态 #%d:\n", i+1)
			for _, as := range sortedAssignmentKeys(a) {
				p := a[as]
				if p == nil {
					fmt.Fprintf(&b, "  AS%d -> 无可用路由\n", as)
				} else {
					fmt.Fprintf(&b, "  AS%d -> [%s]\n", as, p)
				}
			}
		}
	}
	return b.String()
}

func joinAS(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("AS%d", x)
	}
	return strings.Join(parts, " -> ")
}

func sortedAssignmentKeys(a Assignment) []int {
	out := make([]int, 0, len(a))
	for as := range a {
		out = append(out, as)
	}
	// 简单插入排序，避免额外依赖。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
