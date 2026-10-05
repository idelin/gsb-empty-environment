package main

import (
	"fmt"
	"strings"
)

// String 渲染完整分析报告。
func (r *Report) String() string {
	m := r.Model
	var b strings.Builder
	fmt.Fprintf(&b, "状态空间: %d 个状态（%d 个队列", m.NumStates, len(m.queues))
	for _, f := range m.fields {
		fmt.Fprintf(&b, " × %d 个 %s 取值", len(f.Values), f.Name)
	}
	fmt.Fprintf(&b, "），%d 条规则\n\n", len(m.rules))

	b.WriteString("== 终止性 ==\n")
	if r.Terminating {
		b.WriteString("✓ 所有输入都会终止\n")
	} else {
		b.WriteString("✗ 存在不终止的输入\n")
		fmt.Fprintf(&b, "  最小输入: %s\n", m.StateString(r.TermInput))
		entry := r.TermInput
		if len(r.TermPrefix) > 0 {
			b.WriteString("  到达循环的路径:\n")
			fmt.Fprintf(&b, "    %s\n", m.StateString(r.TermInput))
			for _, st := range r.TermPrefix {
				fmt.Fprintf(&b, "    --[%s]--> %s\n", st.Rule, m.StateString(st.State))
			}
			entry = r.TermPrefix[len(r.TermPrefix)-1].State
		}
		b.WriteString("  循环:\n")
		fmt.Fprintf(&b, "    %s\n", m.StateString(entry))
		for i, st := range r.TermCycle {
			suffix := ""
			if i == len(r.TermCycle)-1 {
				suffix = "   <-- 回到循环起点"
			}
			fmt.Fprintf(&b, "    --[%s]--> %s%s\n", st.Rule, m.StateString(st.State), suffix)
		}
	}

	b.WriteString("\n== 最终队列唯一性 ==\n")
	if r.Unique {
		b.WriteString("✓ 所有终止执行的最终队列唯一\n")
	} else {
		b.WriteString("✗ 存在可到达不同最终队列的输入\n")
		fmt.Fprintf(&b, "  最小输入: %s\n", m.StateString(r.UniqInput))
		r.writeBranch(&b, "A", r.UniqQueueA, r.UniqPathA)
		r.writeBranch(&b, "B", r.UniqQueueB, r.UniqPathB)
	}
	if !r.Terminating {
		b.WriteString("（唯一性仅针对会终止的执行路径判定）\n")
	}
	return b.String()
}

func (r *Report) writeBranch(b *strings.Builder, label string, queue int, path []Step) {
	m := r.Model
	fmt.Fprintf(b, "  分支 %s（最终队列 %s）:\n", label, m.QueueName(queue))
	fmt.Fprintf(b, "    %s\n", m.StateString(r.UniqInput))
	for _, st := range path {
		fmt.Fprintf(b, "    --[%s]--> %s\n", st.Rule, m.StateString(st.State))
	}
}
