package main

import (
	"strings"
	"testing"
	"time"
)

func TestQError(t *testing.T) {
	cases := []struct {
		est, act, want float64
	}{
		{100, 100, 1},
		{100, 50000, 500},
		{50000, 100, 500},
		{0, 10, 10}, // 0 按 1 处理
		{10, 0, 10},
		{0, 0, 1},
	}
	for _, c := range cases {
		if got := QError(c.est, c.act); got != c.want {
			t.Errorf("QError(%v, %v) = %v, want %v", c.est, c.act, got, c.want)
		}
	}
}

func TestSeverityOf(t *testing.T) {
	if SeverityOf(1.5) != OK {
		t.Error("1.5x 应为 OK")
	}
	if SeverityOf(4) != Warn {
		t.Error("4x 应为 Warn")
	}
	if SeverityOf(10) != Critical {
		t.Error("10x 应为 Critical")
	}
	if SeverityOf(500) != Critical {
		t.Error("500x 应为 Critical")
	}
}

// 验收场景：含数据倾斜的查询，Scan(users) 估算 100 行、真实 50000 行，
// 比对器必须给出超标告警，提示直方图失效并建议重新收集统计信息。
func TestSkewedQueryAcceptance(t *testing.T) {
	users := BuildSkewedUsers()
	tables := map[string]*Table{"users": users}
	cat := StaleCatalog()

	pred := func(r Row) bool { return r["age"].(int) >= 30 }
	root := PlanQuery(cat, pred, 0.5)

	report, err := Compare(EstPlan(root), Execute(root, tables))
	if err != nil {
		t.Fatalf("Compare 失败: %v", err)
	}

	// 找到 Scan(users) 的比对结果
	var scan *Comparison
	for i := range report.Comparisons {
		if report.Comparisons[i].Name == "Scan(users)" {
			scan = &report.Comparisons[i]
		}
	}
	if scan == nil {
		t.Fatal("比对结果中缺少 Scan(users)")
	}
	if scan.EstRows != 100 {
		t.Errorf("Scan(users) 估算行数 = %v, 期望 100", scan.EstRows)
	}
	if scan.ActRows != 50000 {
		t.Errorf("Scan(users) 真实行数 = %v, 期望 50000", scan.ActRows)
	}
	if scan.RowQError != 500 {
		t.Errorf("Scan(users) 偏差倍率 = %v, 期望 500", scan.RowQError)
	}
	if scan.Severity != Critical {
		t.Errorf("Scan(users) 告警级别 = %v, 期望 CRITICAL", scan.Severity)
	}
	if !strings.Contains(scan.Diagnosis, "直方图已失效") {
		t.Errorf("诊断未提示直方图失效: %s", scan.Diagnosis)
	}
	if !strings.Contains(scan.Diagnosis, "ANALYZE TABLE users") ||
		!strings.Contains(scan.Diagnosis, "重新收集统计信息") {
		t.Errorf("诊断未建议重新收集统计信息: %s", scan.Diagnosis)
	}

	// 告警列表中必须包含 Scan(users) 的 CRITICAL 告警
	found := false
	for _, a := range report.Alerts {
		if a.Operator == "Scan(users)" && a.Severity == Critical {
			found = true
		}
	}
	if !found {
		t.Error("告警列表中缺少 Scan(users) 的 CRITICAL 告警")
	}
}

// 统计信息准确时不应产生告警。
func TestAccurateStatsNoAlert(t *testing.T) {
	users := BuildSkewedUsers()
	tables := map[string]*Table{"users": users}
	cat := &Catalog{Stats: map[string]TableStats{
		"users": {RowCount: 50000}, // 统计信息是新的
	}}

	// age >= 30 实际过滤出 40000/50000 行，选择率 0.8
	pred := func(r Row) bool { return r["age"].(int) >= 30 }
	root := PlanQuery(cat, pred, 0.8)

	report, err := Compare(EstPlan(root), Execute(root, tables))
	if err != nil {
		t.Fatalf("Compare 失败: %v", err)
	}
	for _, c := range report.Comparisons {
		if c.Name == "Scan(users)" && c.Severity != OK {
			t.Errorf("统计准确时 Scan(users) 不应告警, 实际: %v %s", c.Severity, c.Diagnosis)
		}
	}
}

// 结构不一致（算子 ID 对不上）时必须报错，而不是静默跳过。
func TestMismatchDetected(t *testing.T) {
	est := &EstNode{ID: 1, Op: "Scan", Table: "users", EstRows: 100}
	act := &ActNode{ID: 99, Op: "Scan", Table: "users", ActRows: 1, ActDuration: time.Millisecond}
	if _, err := Compare(est, act); err == nil {
		t.Error("算子 ID 不匹配时应返回错误")
	}
}

// 对齐应覆盖整棵树的所有算子。
func TestCompareCoversAllOperators(t *testing.T) {
	users := BuildSkewedUsers()
	tables := map[string]*Table{"users": users}
	root := PlanQuery(StaleCatalog(), func(r Row) bool { return true }, 1.0)

	report, err := Compare(EstPlan(root), Execute(root, tables))
	if err != nil {
		t.Fatalf("Compare 失败: %v", err)
	}
	if len(report.Comparisons) != 3 {
		t.Errorf("应比对 3 个算子, 实际 %d", len(report.Comparisons))
	}
}
