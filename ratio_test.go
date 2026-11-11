package netsec

import (
	"math/big"
	"slices"
	"testing"
)

func TestRatioThresholdExactBoundary(t *testing.T) {
	trees := newTestTrees(t)
	cfg := Config{
		QuasiIDs:         []string{"region", "job"},
		Trees:            trees,
		Levels:           []int{1, 1},
		L:                2,
		MaxRatio:         big.NewRat(1, 2),
		RatioDenominator: RatioOverAll,
	}
	// 流感 2/4 == 1/2，边界应判合规。
	records := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "上海", "教师", "流感"),
		rec("r3", "北京", "教师", "糖尿病"),
		rec("r4", "上海", "工程师", "胃病"),
	}
	report, err := Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if !report.Compliant() {
		t.Fatalf("占比恰好等于阈值应合规，违规: %+v", report.Violations)
	}

	// 加入一条流感后变为 3/5 > 1/2，必须报占比违规并给出精确证据。
	records = append(records, rec("r5", "北京", "工程师", "流感"))
	report, err = Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if report.Compliant() || len(report.Violations) != 1 {
		t.Fatalf("期望 1 个占比违规组，实际 %+v", report.Violations)
	}
	evidence := report.Violations[0].Ratios
	if len(evidence) != 1 || evidence[0].Value != "流感" {
		t.Fatalf("占比证据错误: %+v", evidence)
	}
	if evidence[0].Ratio.Cmp(big.NewRat(3, 5)) != 0 {
		t.Fatalf("违规占比应为 3/5，实际 %s", evidence[0].Ratio.RatString())
	}
	if !slices.Equal(evidence[0].Members, []string{"r1", "r2", "r5"}) {
		t.Fatalf("越界值成员证据错误: %v", evidence[0].Members)
	}
}

func TestUnknownDilutionAndRatioBasis(t *testing.T) {
	trees := newTestTrees(t)
	records := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "上海", "教师", "流感"),
		rec("r3", "北京", "教师", "糖尿病"),
		rec("u1", "上海", "工程师", UnknownSensitive),
		rec("u2", "北京", "工程师", UnknownSensitive),
	}

	// 全组分母 5：流感 2/5 <= 1/2，合规。
	cfgAll := Config{
		QuasiIDs:         []string{"region", "job"},
		Trees:            trees,
		Levels:           []int{1, 1},
		L:                2,
		MaxRatio:         big.NewRat(1, 2),
		RatioDenominator: RatioOverAll,
	}
	report, err := Verify(cfgAll, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if !report.Compliant() {
		t.Fatalf("全组分母下应合规: %+v", report.Violations)
	}

	// 仅已知分母 3：流感 2/3 > 1/2，违规；未知记录既不进分子也不进分母。
	cfgKnown := cfgAll
	cfgKnown.RatioDenominator = RatioOverKnown
	report, err = Verify(cfgKnown, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if report.Compliant() {
		t.Fatal("已知口径下 2/3 应超过 1/2 阈值")
	}
	ev := report.Violations[0].Ratios[0]
	if ev.Denominator != 3 || ev.Ratio.Cmp(big.NewRat(2, 3)) != 0 {
		t.Fatalf("已知口径证据错误: denominator=%d ratio=%s", ev.Denominator, ev.Ratio.RatString())
	}
}

func TestMinimalViolationIsSmallestGroup(t *testing.T) {
	trees := newTestTrees(t)
	cfg := Config{
		QuasiIDs:     []string{"region", "job"},
		Trees:        trees,
		Levels:       []int{0, 0},
		L:            2,
		MinGroupSize: 2,
	}
	records := []Record{
		// 北京/工程师：2 条 2 种值，满足 L=2 与 k=2，合规。
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "北京", "工程师", "糖尿病"),
		// 上海/教师：2 条但只有 1 种值，仅命中多样性违规。
		rec("r3", "上海", "教师", "胃病"),
		rec("r4", "上海", "教师", "胃病"),
		// 广州/医生：单记录组，同时命中组大小与多样性违规。
		rec("r5", "广州", "医生", "胃病"),
	}
	// “广州/医生”是新的具体准标识取值，需补进树。
	cfg.Trees[0], _ = NewTree("region", [][2]string{
		{"北京", "R*"}, {"上海", "R*"}, {"广州", "R*"},
	})
	cfg.Trees[1], _ = NewTree("job", [][2]string{
		{"工程师", "J*"}, {"教师", "J*"}, {"医生", "J*"},
	})
	report, err := Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if len(report.Violations) != 2 {
		t.Fatalf("期望 2 个违规组，实际 %d", len(report.Violations))
	}
	min := report.MinimalViolation()
	if min.Size != 1 {
		t.Fatalf("最小违规组大小应为 1，实际 %d", min.Size)
	}
	want := ReasonGroupSize | ReasonDiversity
	if min.Reasons&want != want {
		t.Fatalf("原因位掩码错误: %s", min.Reasons)
	}
	if got := min.Reasons.String(); got != "group-size+diversity" {
		t.Fatalf("原因字符串错误: %s", got)
	}
}

func TestInvalidRecordRejectedAtomically(t *testing.T) {
	trees := newTestTrees(t)
	v, err := New(Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    trees,
		Levels:   []int{0, 0},
		L:        1,
	})
	if err != nil {
		t.Fatalf("构建核验器失败: %v", err)
	}
	bad := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "火星", "教师", "胃病"),
	}
	if err := v.Replace(bad); err == nil {
		t.Fatal("含非法准标识值的记录集应被整体拒绝")
	}
	if v.RecordCount() != 0 {
		t.Fatal("原子替换失败后内部状态应保持为空")
	}

	dups := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r1", "上海", "教师", "胃病"),
	}
	if err := v.Replace(dups); err == nil {
		t.Fatal("重复 ID 应报错")
	}
}

func TestEmptyReport(t *testing.T) {
	trees := newTestTrees(t)
	report, err := Verify(Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    trees,
		Levels:   []int{0, 0},
		L:        1,
	}, nil)
	if err != nil {
		t.Fatalf("空记录集核验失败: %v", err)
	}
	if !report.Compliant() || len(report.Groups) != 0 || report.TotalRecords != 0 {
		t.Fatalf("空报告应为合规且无组: %+v", report)
	}
	if report.MinimalViolation() != nil {
		t.Fatal("空报告不存在最小违规组")
	}
}
