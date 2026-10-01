package masking

import (
	"regexp"
	"strings"
	"testing"
)

func newTestExporter(t *testing.T) *Exporter {
	t.Helper()
	ps, err := ParsePolicySet(DefaultPolicyJSON)
	if err != nil {
		t.Fatalf("加载策略失败: %v", err)
	}
	return NewExporter(ps).WithRandSeed(42)
}

// 验收：财务角色可查看薪资明细，手机号保留前3后4，身份证被遮蔽。
func TestFinanceExport(t *testing.T) {
	out, err := newTestExporter(t).Export("finance", "confidential", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("期望 4 条记录, 实际 %d", len(out))
	}
	phoneRe := regexp.MustCompile(`^1\d{2}\*{4}\d{4}$`)
	for i, row := range out {
		phone, _ := row["phone"].(string)
		if !phoneRe.MatchString(phone) {
			t.Errorf("记录 %d 手机号未按前3后4掩码: %q", i, phone)
		}
		if row["id_card"] != "***" {
			t.Errorf("记录 %d 身份证未被遮蔽: %v", i, row["id_card"])
		}
	}
	// 薪资明细保持精确值
	if out[0]["salary"] != 23500.0 || out[2]["salary"] != 27800.0 {
		t.Errorf("财务角色薪资明细被改动: %v / %v", out[0]["salary"], out[2]["salary"])
	}
	// 手机号前3后4与原始一致
	if out[0]["phone"] != "138****5678" {
		t.Errorf("手机号掩码结果不符合预期: %v", out[0]["phone"])
	}
}

// 验收：外包角色薪资变为区间范围，姓名假名化。
func TestContractorExport(t *testing.T) {
	out, err := newTestExporter(t).Export("contractor", "restricted", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	salaryRange := regexp.MustCompile(`^\d+-\d+$`)
	names := map[string]string{}
	for i, row := range out {
		salary, _ := row["salary"].(string)
		if !salaryRange.MatchString(salary) {
			t.Errorf("记录 %d 薪资未区间化: %q", i, salary)
		}
		name, _ := row["name"].(string)
		if !strings.HasPrefix(name, "NAME-") {
			t.Errorf("记录 %d 姓名未假名化: %q", i, name)
		}
		if row["phone"] != "N/A" || row["id_card"] != "N/A" {
			t.Errorf("记录 %d 敏感字段未遮蔽: %v / %v", i, row["phone"], row["id_card"])
		}
		names[name] = name
	}
	// 薪资区间校验：23500 -> 20000-25000, 15400 -> 15000-20000
	if out[0]["salary"] != "20000-25000" {
		t.Errorf("薪资区间错误: %v", out[0]["salary"])
	}
	if out[3]["salary"] != "15000-20000" {
		t.Errorf("薪资区间错误: %v", out[3]["salary"])
	}
	// 区间化后不得泄露精确薪资
	for _, row := range out {
		if row["salary"] == 23500.0 || row["salary"] == 18200.0 {
			t.Errorf("区间化后仍泄露精确薪资: %v", row["salary"])
		}
	}
}

// 验收：相同假名在单次导出中具有关联一致性（两条"张伟"记录假名相同）。
func TestPseudonymConsistencyWithinExport(t *testing.T) {
	out, err := newTestExporter(t).Export("contractor", "restricted", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if out[0]["name"] != out[3]["name"] {
		t.Errorf("相同姓名在单次导出中假名不一致: %v vs %v", out[0]["name"], out[3]["name"])
	}
	if out[0]["name"] == out[1]["name"] {
		t.Errorf("不同姓名产生了相同假名: %v", out[0]["name"])
	}
}

// 不同次导出使用不同会话密钥，假名不可跨导出关联。
func TestPseudonymDiffersAcrossExports(t *testing.T) {
	e := newTestExporter(t)
	first, err := e.Export("contractor", "restricted", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	second, err := e.Export("contractor", "restricted", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if first[0]["name"] == second[0]["name"] {
		t.Errorf("不同导出的假名不应可关联: %v", first[0]["name"])
	}
}

// 差分加噪：输出接近原值但不等于原值，且种子固定时可复现。
func TestNoiseExport(t *testing.T) {
	out, err := newTestExporter(t).Export("analyst", "statistical", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	orig := SampleRoster()
	for i, row := range out {
		noisy, _ := row["salary"].(float64)
		base := orig[i]["salary"].(float64)
		if noisy == base {
			t.Errorf("记录 %d 薪资未加噪", i)
		}
		if diff := noisy - base; diff > 20000 || diff < -20000 {
			t.Errorf("记录 %d 噪声幅度异常: %v -> %v", i, base, noisy)
		}
	}
	// 同种子可复现
	again, err := newTestExporter(t).Export("analyst", "statistical", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	for i := range out {
		if out[i]["salary"] != again[i]["salary"] {
			t.Errorf("相同种子下加噪结果不可复现: %v vs %v", out[i]["salary"], again[i]["salary"])
		}
	}
}

// 未声明字段默认不导出（默认拒绝）。
func TestUndeclaredFieldsDropped(t *testing.T) {
	out, err := newTestExporter(t).Export("analyst", "statistical", SampleRoster())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	for i, row := range out {
		if _, ok := row["name"]; ok {
			t.Errorf("记录 %d 泄露了未声明字段 name", i)
		}
		if _, ok := row["phone"]; ok {
			t.Errorf("记录 %d 泄露了未声明字段 phone", i)
		}
		if _, ok := row["id_card"]; ok {
			t.Errorf("记录 %d 泄露了未声明字段 id_card", i)
		}
	}
}

// 未知角色/级别组合必须拒绝导出。
func TestUnknownRoleRejected(t *testing.T) {
	if _, err := newTestExporter(t).Export("ceo", "internal", SampleRoster()); err == nil {
		t.Error("未知角色应被拒绝导出")
	}
	if _, err := newTestExporter(t).Export("finance", "public", SampleRoster()); err == nil {
		t.Error("未配置的合规级别应被拒绝导出")
	}
}

// 掩码转换的边界行为。
func TestMaskKeepEdgeCases(t *testing.T) {
	short := MaskKeep(3, 4, '*')("12345")
	if short != "*****" {
		t.Errorf("短字符串应全部掩码, 实际 %q", short)
	}
	id := MaskKeep(4, 4, '*')("110101199003077788")
	if id != "1101**********7788" {
		t.Errorf("身份证掩码错误: %q", id)
	}
}
