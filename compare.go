package reflector

import (
	"fmt"
	"sort"
	"strings"
)

// Severity 表示一条偏差告警的等级。
type Severity int

const (
	// SeverityOK 偏差在可接受范围内。
	SeverityOK Severity = iota
	// SeverityWarn 偏差超标，需要关注。
	SeverityWarn
	// SeverityCritical 偏差严重超标，极可能导致计划选择错误。
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityOK:
		return "OK"
	case SeverityWarn:
		return "WARN"
	case SeverityCritical:
		return "CRITICAL"
	}
	return "UNKNOWN"
}

// Options 控制比对器的告警阈值。
type Options struct {
	// WarnRatio 触发 WARN 的偏差倍率（取 max(est,act)/min(est,act)）。
	WarnRatio float64
	// CriticalRatio 触发 CRITICAL 的偏差倍率。
	CriticalRatio float64
}

// DefaultOptions 返回默认阈值：4 倍告警，10 倍严重告警。
func DefaultOptions() Options {
	return Options{WarnRatio: 4, CriticalRatio: 10}
}

// Finding 是单个算子的比对结论。
type Finding struct {
	NodeID      string
	Operator    string
	Table       string
	EstRows     float64
	ActRows     float64
	Deviation   float64 // 偏差倍率，恒 >= 1（估算准确时为 1）
	Direction   string  // "underestimate" / "overestimate" / "accurate"
	Severity    Severity
	Diagnosis   string   // 归因诊断
	Suggestions []string // 修复建议
}

// Name 返回算子的展示名，如 Scan(users)。
func (f Finding) Name() string {
	if f.Table != "" {
		return fmt.Sprintf("%s(%s)", f.Operator, f.Table)
	}
	return f.Operator
}

// Report 是一次闭环校验的完整结果。
type Report struct {
	Findings []Finding
	// MissingActual 列出估算计划中存在、但 EXPLAIN ANALYZE 未返回统计的算子 ID。
	MissingActual []string
}

// Worst 返回报告中最高告警等级。
func (r *Report) Worst() Severity {
	worst := SeverityOK
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// Alerts 返回所有非 OK 的发现，按偏差倍率降序。
func (r *Report) Alerts() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity != SeverityOK {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deviation > out[j].Deviation })
	return out
}

// Comparator 逐算子对齐估算计划与真实执行统计。
type Comparator struct {
	opts Options
}

// NewComparator 创建比对器；opts 为零值时使用默认阈值。
func NewComparator(opts Options) *Comparator {
	if opts.WarnRatio <= 0 {
		opts.WarnRatio = DefaultOptions().WarnRatio
	}
	if opts.CriticalRatio <= 0 {
		opts.CriticalRatio = DefaultOptions().CriticalRatio
	}
	return &Comparator{opts: opts}
}

// Compare 递归遍历估算计划树，按算子 ID 与真实执行统计对齐并逐节点比对。
func (c *Comparator) Compare(root *PlanNode, actual ActualPlan) *Report {
	rep := &Report{}
	var walk func(n *PlanNode)
	walk = func(n *PlanNode) {
		a, ok := actual[n.ID]
		if !ok {
			rep.MissingActual = append(rep.MissingActual, n.ID)
		} else {
			rep.Findings = append(rep.Findings, c.compareNode(n, a))
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	if root != nil {
		walk(root)
	}
	sort.Strings(rep.MissingActual)
	return rep
}

func (c *Comparator) compareNode(n *PlanNode, a ActualNode) Finding {
	f := Finding{
		NodeID:   n.ID,
		Operator: n.Operator,
		Table:    n.Table,
		EstRows:  n.EstRows,
		ActRows:  a.ActRows,
	}
	f.Deviation, f.Direction = deviation(n.EstRows, a.ActRows)
	f.Severity = c.severity(f.Deviation)
	if f.Severity != SeverityOK {
		f.Diagnosis, f.Suggestions = diagnose(n, f)
	}
	return f
}

func (c *Comparator) severity(dev float64) Severity {
	switch {
	case dev >= c.opts.CriticalRatio:
		return SeverityCritical
	case dev >= c.opts.WarnRatio:
		return SeverityWarn
	default:
		return SeverityOK
	}
}

// deviation 计算估算与真实的偏差倍率（>=1）及方向。
func deviation(est, act float64) (float64, string) {
	if est == act {
		return 1, "accurate"
	}
	if est == 0 || act == 0 {
		// 一端为 0 时视为无穷大偏差，用极大值表示。
		return 1e18, direction(est, act)
	}
	if est > act {
		return est / act, direction(est, act)
	}
	return act / est, direction(est, act)
}

func direction(est, act float64) string {
	if act > est {
		return "underestimate"
	}
	return "overestimate"
}

// diagnose 依据算子类型与偏差方向给出归因与修复建议。
func diagnose(n *PlanNode, f Finding) (string, []string) {
	name := f.Name()
	switch n.Operator {
	case "Scan":
		if f.Direction == "underestimate" {
			return fmt.Sprintf("%s 估算行数 %.0f 与真实行数 %.0f 偏差 %.0f 倍：列直方图未能刻画数据倾斜，统计信息已失效",
					name, f.EstRows, f.ActRows, f.Deviation),
				[]string{
					fmt.Sprintf("直方图失效：对表 %s 重新收集统计信息（ANALYZE TABLE %s）", n.Table, n.Table),
					fmt.Sprintf("提高列直方图桶数上限，或对高频值启用 most-common-values 统计"),
				}
		}
		return fmt.Sprintf("%s 真实行数远小于估算：统计信息可能基于过期数据采样", name),
			[]string{fmt.Sprintf("重新收集表 %s 的统计信息（ANALYZE TABLE %s）", n.Table, n.Table)}
	case "Filter":
		return fmt.Sprintf("%s 选择率估算偏差 %.0f 倍：多谓词间可能存在相关性", name, f.Deviation),
			[]string{"对相关谓词列创建扩展统计信息（多列相关统计）"}
	case "HashJoin", "MergeJoin", "NestedLoop":
		return fmt.Sprintf("%s 输出行数偏差 %.0f 倍：连接顺序或连接方式可能次优", name, f.Deviation),
			[]string{"检查子节点估算偏差来源，必要时重新收集基表统计信息"}
	default:
		return fmt.Sprintf("%s 估算偏差 %.0f 倍", name, f.Deviation),
			[]string{"检查该算子输入统计信息是否过期"}
	}
}

// String 渲染人类可读的校验报告。
func (r *Report) String() string {
	var b strings.Builder
	b.WriteString("==== 优化器闭环校验报告 ====\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "[%s] %s est=%.0f act=%.0f dev=%.1fx (%s)\n",
			f.Severity, f.Name(), f.EstRows, f.ActRows, f.Deviation, f.Direction)
		if f.Diagnosis != "" {
			fmt.Fprintf(&b, "    诊断: %s\n", f.Diagnosis)
		}
		for _, s := range f.Suggestions {
			fmt.Fprintf(&b, "    建议: %s\n", s)
		}
	}
	for _, id := range r.MissingActual {
		fmt.Fprintf(&b, "[MISSING] 算子 %s 缺少 EXPLAIN ANALYZE 实际统计\n", id)
	}
	fmt.Fprintf(&b, "==== 总体结论: %s ====\n", r.Worst())
	return b.String()
}
