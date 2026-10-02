package main

import (
	"fmt"
	"sort"
	"strings"
)

// Comparison 是单个算子的估算 vs 实际比对结果。
type Comparison struct {
	ID          int
	Name        string // 如 Scan(users)
	Op          string
	Table       string
	EstRows     float64
	ActRows     int64
	RowQError   float64 // 行数偏差倍率
	EstCost     float64
	ActDuration float64 // 毫秒
	Severity    Severity
	Diagnosis   string // 诊断与调优建议，OK 时为空
}

// Alert 是一条需要人工关注的告警。
type Alert struct {
	Severity Severity
	Operator string
	Message  string
}

// Report 是整棵计划树的闭环校验报告。
type Report struct {
	Comparisons []Comparison
	Alerts      []Alert
}

// Compare 将优化器估算树与 EXPLAIN ANALYZE 实际树按算子 ID 逐节点对齐比对。
// 两棵树由同一物理计划派生，结构一致；若结构不一致则返回错误。
func Compare(est *EstNode, act *ActNode) (*Report, error) {
	estByID := make(map[int]*EstNode)
	if err := indexEst(est, estByID); err != nil {
		return nil, err
	}
	rep := &Report{}
	if err := alignWalk(estByID, act, rep); err != nil {
		return nil, err
	}
	sort.Slice(rep.Comparisons, func(i, j int) bool {
		return rep.Comparisons[i].ID < rep.Comparisons[j].ID
	})
	for _, c := range rep.Comparisons {
		if c.Severity != OK {
			rep.Alerts = append(rep.Alerts, Alert{
				Severity: c.Severity,
				Operator: c.Name,
				Message:  c.Diagnosis,
			})
		}
	}
	return rep, nil
}

func indexEst(n *EstNode, m map[int]*EstNode) error {
	if _, dup := m[n.ID]; dup {
		return fmt.Errorf("估算计划中存在重复算子 ID %d (%s)", n.ID, n.Name())
	}
	m[n.ID] = n
	for _, c := range n.Children {
		if err := indexEst(c, m); err != nil {
			return err
		}
	}
	return nil
}

func alignWalk(estByID map[int]*EstNode, act *ActNode, rep *Report) error {
	est, ok := estByID[act.ID]
	if !ok {
		return fmt.Errorf("实际执行树中的算子 ID %d (%s) 在估算计划中不存在", act.ID, act.Name())
	}
	if est.Op != act.Op || est.Table != act.Table {
		return fmt.Errorf("算子 ID %d 类型不匹配: 估算 %s vs vs 实际 %s", act.ID, est.Name(), act.Name())
	}

	q := QError(est.EstRows, float64(act.ActRows))
	sev := SeverityOf(q)
	c := Comparison{
		ID:          est.ID,
		Name:        est.Name(),
		Op:          est.Op,
		Table:       est.Table,
		EstRows:     est.EstRows,
		ActRows:     act.ActRows,
		RowQError:   q,
		EstCost:     est.EstCost,
		ActDuration: float64(act.ActDuration) / float64(1e6), // ns -> ms
		Severity:    sev,
	}
	if sev != OK {
		c.Diagnosis = diagnose(est, act, q)
	}
	rep.Comparisons = append(rep.Comparisons, c)

	for _, child := range act.Children {
		if err := alignWalk(estByID, child, rep); err != nil {
			return err
		}
	}
	return nil
}

// diagnose 根据算子类型与偏差方向生成诊断和调优建议。
func diagnose(est *EstNode, act *ActNode, q float64) string {
	underestimate := float64(act.ActRows) > est.EstRows
	direction := "高估"
	if underestimate {
		direction = "低估"
	}
	head := fmt.Sprintf("行数%s %.1f 倍（估算 %.0f vs 真实 %d）", direction, q, est.EstRows, act.ActRows)

	switch est.Op {
	case "Scan":
		if underestimate {
			return head + fmt.Sprintf("；表 %s 的统计信息/直方图已失效，优化器拿到的行数严重偏小。"+
				"建议执行 ANALYZE TABLE %s 重新收集统计信息（含直方图）后重试。", est.Table, est.Table)
		}
		return head + fmt.Sprintf("；表 %s 的统计信息可能过于陈旧（高估）。建议执行 ANALYZE TABLE %s 重新收集统计信息。", est.Table, est.Table)
	case "Filter":
		return head + "；过滤谓词选择率估算失准，可能存在数据倾斜或相关列。建议收集谓词列直方图或扩展统计信息。"
	case "HashJoin", "Join":
		return head + "；连接基数估算失准，可能由连接键倾斜或跨列相关性导致。建议检查连接键直方图并重新收集统计信息。"
	case "Aggregate":
		return head + "；分组基数（NDV）估算失准。建议重新收集分组列统计信息。"
	default:
		return head + "；建议重新收集相关统计信息。"
	}
}

// String 渲染为可读的文本报告。
func (r *Report) String() string {
	var b strings.Builder
	b.WriteString("==== 优化器闭环校验报告 (EXPLAIN ANALYZE vs 优化器估算) ====\n")
	fmt.Fprintf(&b, "%-4s %-18s %12s %12s %10s %10s %12s %s\n",
		"ID", "算子", "估算行数", "真实行数", "偏差倍率", "估算Cost", "真实耗时(ms)", "级别")
	for _, c := range r.Comparisons {
		fmt.Fprintf(&b, "%-4d %-18s %12.0f %12d %9.1fx %10.1f %12.3f %s\n",
			c.ID, c.Name, c.EstRows, c.ActRows, c.RowQError, c.EstCost, c.ActDuration, c.Severity)
	}
	if len(r.Alerts) == 0 {
		b.WriteString("\n无告警：所有算子估算均在可接受偏差范围内。\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n---- 告警 (%d 条) ----\n", len(r.Alerts))
	for _, a := range r.Alerts {
		fmt.Fprintf(&b, "[%s] %s: %s\n", a.Severity, a.Operator, a.Message)
	}
	return b.String()
}
