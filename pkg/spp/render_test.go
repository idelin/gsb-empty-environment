package spp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderAndJSON 覆盖文本渲染、JSON 序列化与空选择的 null 表达。
func TestRenderAndJSON(t *testing.T) {
	report, g := analyzeFixture(t, "bad_gadget.json")
	text := report.RenderText()
	for _, want := range []string{"无稳态", "争议轮", "循环偏好证明", "最小策略调整", "Gao-Rexford"} {
		if !strings.Contains(text, want) {
			t.Fatalf("文本报告缺少关键段 %q", want)
		}
	}

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["outcome"] != string(OutcomeNone) {
		t.Fatalf("JSON outcome 异常: %v", back["outcome"])
	}

	// 空选择必须序列化为 null，而不是缺失或空数组。
	a := Assignment{1: nil, 2: {2, 0}}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["1"] != nil {
		t.Fatalf("空选择应序列化为 null, 得到 %v", decoded["1"])
	}

	// 路径相等与排名工具。
	if !g.Better(1, Path{1, 3, 0}, Path{1, 0}) {
		t.Fatal("Bad Gadget 中 [1,3,0] 应严格优于 [1,0]")
	}
	if g.Rank(1, Path{1, 9, 0}) != -1 {
		t.Fatal("不允许的路径排名应为 -1")
	}
}

// TestLoadErrors 校验输入结构错误被明确拒绝。
func TestLoadErrors(t *testing.T) {
	base := loadFixture(t, "clean_valley_free.json")

	dup := *base
	dup.Nodes = append([]int(nil), base.Nodes...)
	dup.Nodes = append(dup.Nodes, base.Nodes[0])
	if _, err := Load(&dup); err == nil {
		t.Fatal("重复节点必须报错")
	}

	loop := *base
	loop.Edges = append([]EdgeConfig(nil), base.Edges...)
	loop.Edges = append(loop.Edges, EdgeConfig{From: 1, To: 1, Kind: "p2p"})
	if _, err := Load(&loop); err == nil {
		t.Fatal("自环边必须报错")
	}

	nonSimple := *base
	nonSimple.Preferences = map[string][]Path{
		"3": {{3, 1, 3, 0}},
	}
	if _, err := Load(&nonSimple); err == nil {
		t.Fatal("含重复 AS 的非简单路径必须报错")
	}

	if _, err := LoadConfig([]byte("{bad json")); err == nil {
		t.Fatal("非法 JSON 必须报错")
	}
}
