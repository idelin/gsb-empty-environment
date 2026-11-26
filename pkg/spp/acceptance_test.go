package spp

import (
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) *Config {
	t.Helper()
	cfg, err := LoadConfigFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("加载夹具 %s 失败: %v", name, err)
	}
	return cfg
}

func analyzeFixture(t *testing.T, name string) (*Report, *Graph) {
	t.Helper()
	cfg := loadFixture(t, name)
	report, err := Analyze(cfg, AnalysisOptions{
		WheelLimit: DefaultWheelLimit,
		Repair:     RepairOptions{MaxSize: DefaultMaxRepairSize, MaxWheelLimit: DefaultWheelLimit},
	})
	if err != nil {
		t.Fatalf("分析夹具 %s 失败: %v", name, err)
	}
	g, err := Load(cfg)
	if err != nil {
		t.Fatalf("重新加载夹具 %s 失败: %v", name, err)
	}
	return report, g
}

func verifyWheelProof(t *testing.T, g *Graph, w *DisputeWheel) {
	t.Helper()
	k := len(w.Pivots)
	if len(w.Spokes) != k || len(w.Rims) != k || len(w.Proof) != k {
		t.Fatal("争议轮各分量长度必须与 pivot 数一致")
	}
	pivotSet := make(map[int]bool, k)
	for _, u := range w.Pivots {
		pivotSet[u] = true
	}
	for i, u := range w.Pivots {
		next := w.Pivots[(i+1)%k]
		if len(w.Rims[i]) < 2 || w.Rims[i][0] != u || w.Rims[i][1] != next {
			t.Fatalf("R_%d 必须以 pivot %d 及其环上邻居 %d 开头", i, u, next)
		}
		if pathHasOtherPivot(w.Spokes[i], u, pivotSet) {
			t.Fatalf("Q_%d 不能经过其他 pivot", i)
		}
		if !g.Better(u, w.Rims[i], w.Spokes[i]) {
			t.Fatalf("必须满足 R_%d >_%d Q_%d", i, u, i)
		}
	}
}

func containsAssignment(list []Assignment, target map[int]Path) bool {
	for _, a := range list {
		match := true
		for as, want := range target {
			got, ok := a[as]
			if !ok {
				match = false
				break
			}
			if want == nil {
				if got != nil {
					match = false
					break
				}
				continue
			}
			if got == nil || !got.Equal(want) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func mutuallyExclusive(list []Assignment) bool {
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			for as, pi := range list[i] {
				pj := list[j][as]
				if (pi == nil) != (pj == nil) || (pi != nil && !pi.Equal(pj)) {
					return true
				}
			}
		}
	}
	return false
}

// TestBadGadgetNoStable 验收 1：经典 3-pivot Bad Gadget 必须无稳态，
// 并构造出覆盖全部三个 pivot 的争议轮。
func TestBadGadgetNoStable(t *testing.T) {
	report, g := analyzeFixture(t, "bad_gadget.json")

	if report.Outcome != OutcomeNone {
		t.Fatalf("期望无稳态, 得到 %s (%d 个分配)", report.Outcome, len(report.StableAssignments))
	}
	if len(report.StableAssignments) != 0 {
		t.Fatalf("Bad Gadget 不应存在稳态, 实际得到 %d 个", len(report.StableAssignments))
	}
	if !report.HasDisputeWheel || len(report.Wheels) == 0 {
		t.Fatal("Bad Gadget 必须检测到争议轮")
	}
	wheel := report.Wheels[0]
	if len(wheel.Pivots) != 3 {
		t.Fatalf("期望 3 个 pivot, 得到 %d", len(wheel.Pivots))
	}
	verifyWheelProof(t, g, wheel)

	if report.GR.StrictlyValleyFree() {
		t.Fatal("Bad Gadget 的跨对等转发路径不应通过严格无谷核验")
	}

	if report.Repair == nil || !report.Repair.UniqueStable {
		t.Fatal("最小修复后必须验证到唯一稳态")
	}
	if len(report.Repair.Actions) != 1 {
		t.Fatalf("Bad Gadget 单条偏好修改即可修复, 实际动作数 %d", len(report.Repair.Actions))
	}
	if report.Repair.RemainingWheels != 0 {
		t.Fatal("修复后争议轮必须清零")
	}
}

// TestDisagreeBistable 验收 2：4-AS Disagree 必须输出至少两个互斥稳态，
// 且存在争议轮；最小修复后唯一稳态。
func TestDisagreeBistable(t *testing.T) {
	report, g := analyzeFixture(t, "disagree.json")

	if report.Outcome != OutcomeMultiple {
		t.Fatalf("期望多稳态, 得到 %s", report.Outcome)
	}
	if len(report.StableAssignments) < 2 {
		t.Fatalf("至少需要两个稳态, 实际 %d 个", len(report.StableAssignments))
	}
	if !mutuallyExclusive(report.StableAssignments) {
		t.Fatal("输出的稳态之间必须互斥（至少一个 AS 选择不同路径）")
	}
	if !containsAssignment(report.StableAssignments, map[int]Path{
		0: {0}, 1: {1, 0}, 2: {2, 1, 0}, 3: {3, 1, 0},
	}) {
		t.Fatal("缺少稳态 A（AS1 阵营）{1->0, 2->1->0, 3->1->0}")
	}
	if !containsAssignment(report.StableAssignments, map[int]Path{
		0: {0}, 1: {1, 2, 0}, 2: {2, 0}, 3: {3, 2, 0},
	}) {
		t.Fatal("缺少稳态 B（AS2 阵营）{1->2->0, 2->0, 3->2->0}")
	}
	if !report.HasDisputeWheel {
		t.Fatal("Disagree 必须检测到争议轮")
	}
	verifyWheelProof(t, g, report.Wheels[0])
	if len(report.Wheels[0].Pivots) < 2 {
		t.Fatal("Disagree 争议轮至少包含两个 pivot")
	}

	if report.Repair == nil || !report.Repair.UniqueStable ||
		len(report.Repair.StableAssignments) != 1 {
		t.Fatal("最小修复后必须恰好存在一个稳态")
	}
	if len(report.Repair.Actions) != 1 {
		t.Fatalf("Disagree 单条修改即可修复, 实际动作数 %d", len(report.Repair.Actions))
	}
}

// TestCleanValleyFreeUnique 验收 3：标准无谷网络必须通过商业核验，
// 无争议轮且唯一稳态。
func TestCleanValleyFreeUnique(t *testing.T) {
	report, _ := analyzeFixture(t, "clean_valley_free.json")

	if !report.GR.StrictlyValleyFree() {
		t.Fatalf("干净网络必须严格无谷, 违规数 %d, 商业环 %d",
			len(report.GR.ValleyFreeViolations), len(report.GR.CommercialCycles))
	}
	if len(report.GR.MissingLegalPaths) != 0 {
		t.Fatalf("偏好序应覆盖全部无谷合法路径, 缺口 %d", len(report.GR.MissingLegalPaths))
	}
	if report.Outcome != OutcomeUnique || len(report.StableAssignments) != 1 {
		t.Fatalf("期望唯一稳态, 得到 %s / %d 个", report.Outcome, len(report.StableAssignments))
	}
	if report.HasDisputeWheel {
		t.Fatal("无谷单调网络不应出现争议轮")
	}
	if report.Repair != nil {
		t.Fatal("健康实例不需要修复方案")
	}
	a := report.StableAssignments[0]
	if !a[3].Equal(Path{3, 1, 0}) || !a[4].Equal(Path{4, 2, 0}) {
		t.Fatalf("唯一稳态选择异常: AS3=%v AS4=%v", a[3], a[4])
	}
}
