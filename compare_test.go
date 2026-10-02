package reflector

import (
	"strings"
	"testing"
	"time"
)

// skewedPlan 构造一段含数据倾斜的查询的估算计划：
//
//	SELECT u.city, COUNT(*)
//	FROM users u JOIN orders o ON o.user_id = u.id
//	WHERE u.city = 'Beijing'
//	GROUP BY u.city
//
// users.city 列存在严重倾斜（'Beijing' 实际占 50000 行），
// 但直方图失效，优化器按均匀分布估算仅 100 行。
func skewedPlan() *PlanNode {
	return &PlanNode{
		ID: "1", Operator: "Aggregate", EstRows: 1, EstCost: 220,
		Children: []*PlanNode{
			{
				ID: "2", Operator: "HashJoin", EstRows: 100, EstCost: 210,
				Children: []*PlanNode{
					{ID: "3", Operator: "Scan", Table: "users", EstRows: 100, EstCost: 10},
					{ID: "4", Operator: "Scan", Table: "orders", EstRows: 1000000, EstCost: 180},
				},
			},
		},
	}
}

func skewedActual() ActualPlan {
	return ActualPlan{
		"1": {ID: "1", ActRows: 1, ActTime: 900 * time.Millisecond, Loops: 1},
		"2": {ID: "2", ActRows: 50000, ActTime: 850 * time.Millisecond, Loops: 1},
		"3": {ID: "3", ActRows: 50000, ActTime: 300 * time.Millisecond, Loops: 1},
		"4": {ID: "4", ActRows: 1050000, ActTime: 400 * time.Millisecond, Loops: 1},
	}
}

func findingByID(rep *Report, id string) (Finding, bool) {
	for _, f := range rep.Findings {
		if f.NodeID == id {
			return f, true
		}
	}
	return Finding{}, false
}

// 验收场景：比对器必须精准指出 Scan(users) 估算 100 vs 真实 50000 偏差超标，
// 并提示直方图失效、建议重新收集统计。
func TestScanUsersSkewDetected(t *testing.T) {
	rep := NewComparator(DefaultOptions()).Compare(skewedPlan(), skewedActual())

	f, ok := findingByID(rep, "3")
	if !ok {
		t.Fatal("未找到 Scan(users) 的比对结果")
	}
	if f.Name() != "Scan(users)" {
		t.Fatalf("算子名 = %q, 期望 Scan(users)", f.Name())
	}
	if f.EstRows != 100 || f.ActRows != 50000 {
		t.Fatalf("行数对齐错误: est=%.0f act=%.0f", f.EstRows, f.ActRows)
	}
	if f.Deviation != 500 {
		t.Fatalf("偏差倍率 = %.1f, 期望 500", f.Deviation)
	}
	if f.Direction != "underestimate" {
		t.Fatalf("偏差方向 = %q, 期望 underestimate", f.Direction)
	}
	if f.Severity != SeverityCritical {
		t.Fatalf("告警等级 = %v, 期望 CRITICAL", f.Severity)
	}
	if !strings.Contains(f.Diagnosis, "直方图") {
		t.Fatalf("诊断未提示直方图失效: %s", f.Diagnosis)
	}
	joined := strings.Join(f.Suggestions, ";")
	if !strings.Contains(joined, "重新收集统计信息") || !strings.Contains(joined, "ANALYZE TABLE users") {
		t.Fatalf("建议未包含重新收集统计: %s", joined)
	}
}

// 估算准确的算子不应产生告警。
func TestAccurateNodeIsOK(t *testing.T) {
	rep := NewComparator(DefaultOptions()).Compare(skewedPlan(), skewedActual())
	f, ok := findingByID(rep, "4")
	if !ok {
		t.Fatal("未找到 Scan(orders) 的比对结果")
	}
	if f.Severity != SeverityOK {
		t.Fatalf("Scan(orders) 偏差 %.1fx 被误报为 %v", f.Deviation, f.Severity)
	}
	if len(f.Suggestions) != 0 {
		t.Fatalf("OK 节点不应附带建议: %v", f.Suggestions)
	}
}

// 上游 HashJoin 因下游 Scan 偏差传导而超标，应给出连接侧诊断。
func TestJoinDeviationPropagates(t *testing.T) {
	rep := NewComparator(DefaultOptions()).Compare(skewedPlan(), skewedActual())
	f, ok := findingByID(rep, "2")
	if !ok {
		t.Fatal("未找到 HashJoin 的比对结果")
	}
	if f.Severity != SeverityCritical {
		t.Fatalf("HashJoin 告警等级 = %v, 期望 CRITICAL", f.Severity)
	}
	if !strings.Contains(f.Diagnosis, "连接") {
		t.Fatalf("HashJoin 诊断不符合预期: %s", f.Diagnosis)
	}
}

// 缺失实际统计的算子应列入 MissingActual。
func TestMissingActualNode(t *testing.T) {
	actual := skewedActual()
	delete(actual, "4")
	rep := NewComparator(DefaultOptions()).Compare(skewedPlan(), actual)
	if len(rep.MissingActual) != 1 || rep.MissingActual[0] != "4" {
		t.Fatalf("MissingActual = %v, 期望 [4]", rep.MissingActual)
	}
}

// 告警阈值边界：4 倍 WARN，10 倍 CRITICAL。
func TestSeverityThresholds(t *testing.T) {
	c := NewComparator(DefaultOptions())
	cases := []struct {
		est, act float64
		want     Severity
	}{
		{100, 399, SeverityOK},
		{100, 400, SeverityWarn},
		{100, 999, SeverityWarn},
		{100, 1000, SeverityCritical},
		{100, 25, SeverityWarn},   // 高估 4 倍同样告警
		{100, 10, SeverityCritical}, // 高估 10 倍
		{100, 100, SeverityOK},
	}
	for _, tc := range cases {
		plan := &PlanNode{ID: "x", Operator: "Scan", Table: "t", EstRows: tc.est}
		rep := c.Compare(plan, ActualPlan{"x": {ID: "x", ActRows: tc.act}})
		if got := rep.Findings[0].Severity; got != tc.want {
			t.Errorf("est=%.0f act=%.0f => %v, 期望 %v", tc.est, tc.act, got, tc.want)
		}
	}
}

// 报告整体结论应取最高告警等级，且告警按偏差降序排列。
func TestReportWorstAndOrdering(t *testing.T) {
	rep := NewComparator(DefaultOptions()).Compare(skewedPlan(), skewedActual())
	if rep.Worst() != SeverityCritical {
		t.Fatalf("Worst = %v, 期望 CRITICAL", rep.Worst())
	}
	alerts := rep.Alerts()
	if len(alerts) != 2 {
		t.Fatalf("告警数 = %d, 期望 2", len(alerts))
	}
	if alerts[0].Deviation < alerts[1].Deviation {
		t.Fatalf("告警未按偏差降序: %.1f, %.1f", alerts[0].Deviation, alerts[1].Deviation)
	}
	if !strings.Contains(rep.String(), "CRITICAL") {
		t.Fatal("报告文本缺少 CRITICAL 结论")
	}
}
