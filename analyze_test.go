package main

import (
	"testing"
)

func rule(name, prefix string, protos Protocol, lo, hi uint16, action Action) Rule {
	return Rule{Name: name, Prefix: pfx(prefix), Protos: protos, PortMin: lo, PortMax: hi, Action: action}
}

func TestShadowedBySingleRule(t *testing.T) {
	rules := []Rule{
		rule("block", "10.0.0.0/8", ProtoTCP, 0, 65535, ActionDeny),
		rule("app", "10.1.2.0/24", ProtoTCP, 8080, 8080, ActionAllow),
	}
	rep := Analyze(rules)
	if rep.Rules[1].Reachable {
		t.Fatal("规则 #1 应被 #0 完全遮蔽")
	}
	if len(rep.Rules[1].CoveredBy) != 1 || rep.Rules[1].CoveredBy[0] != 0 {
		t.Fatalf("覆盖者应为 [0]，得到 %v", rep.Rules[1].CoveredBy)
	}
	// 被截获示例包应命中前规则 #0。
	if got := Match(rules, *rep.Rules[1].Captured); got != 0 {
		t.Fatalf("被截获示例包应命中 #0，实际命中 #%d", got)
	}
	// 完全遮蔽不应再报部分重叠。
	if len(rep.Overlaps) != 0 {
		t.Fatalf("完全遮蔽不应报重叠，得到 %v", rep.Overlaps)
	}
}

func TestShadowedByUnion(t *testing.T) {
	rules := []Rule{
		rule("low", "172.16.0.0/16", ProtoTCP, 0, 1023, ActionDeny),
		rule("high", "172.16.0.0/16", ProtoTCP, 1024, 65535, ActionDeny),
		rule("open", "172.16.0.0/16", ProtoTCP, 0, 65535, ActionAllow),
	}
	rep := Analyze(rules)
	if rep.Rules[2].Reachable {
		t.Fatal("规则 #2 应被 #0+#1 联合遮蔽")
	}
	if len(rep.Rules[2].CoveredBy) != 2 {
		t.Fatalf("覆盖者应为两条规则，得到 %v", rep.Rules[2].CoveredBy)
	}
}

func TestPartialOverlapReported(t *testing.T) {
	rules := []Rule{
		rule("web", "10.0.0.0/8", ProtoTCP, 80, 80, ActionAllow),
		rule("web2", "10.0.0.0/8", ProtoTCP, 80, 8080, ActionDeny),
	}
	rep := Analyze(rules)
	if !rep.Rules[1].Reachable {
		t.Fatal("规则 #1 只被部分覆盖，应仍可达")
	}
	if len(rep.Overlaps) != 1 {
		t.Fatalf("应报告 1 条重叠，得到 %d", len(rep.Overlaps))
	}
	ov := rep.Overlaps[0]
	if !ov.Conflict {
		t.Fatal("动作不同应标记为冲突")
	}
	// 交集示例包须同时匹配两条规则。
	if !rules[0].Matches(ov.Example) || !rules[1].Matches(ov.Example) {
		t.Fatalf("重叠示例包 %v 未同时命中两规则", ov.Example)
	}
	// 见证包须命中 #1 而不命中 #0。
	if got := Match(rules, *rep.Rules[1].Witness); got != 1 {
		t.Fatalf("见证包应命中 #1，实际命中 #%d", got)
	}
}

func TestOverlapSameActionNotConflict(t *testing.T) {
	rules := []Rule{
		rule("a", "10.0.0.0/8", ProtoTCP, 1, 100, ActionAllow),
		rule("b", "10.0.0.0/8", ProtoTCP, 50, 200, ActionAllow),
	}
	rep := Analyze(rules)
	if len(rep.Overlaps) != 1 || rep.Overlaps[0].Conflict {
		t.Fatalf("同动作部分重叠应报告但不标冲突: %+v", rep.Overlaps)
	}
}

func TestFamilyIsolation(t *testing.T) {
	rules := []Rule{
		rule("v4-all-deny", "0.0.0.0/0", ProtoAny, 0, 65535, ActionDeny),
		rule("v6-web", "2001:db8::/32", ProtoTCP, 443, 443, ActionAllow),
		rule("v6-all-deny", "::/0", ProtoAny, 0, 65535, ActionDeny),
		rule("v4-web", "192.0.2.0/24", ProtoTCP, 80, 80, ActionAllow),
	}
	rep := Analyze(rules)
	if !rep.Rules[1].Reachable {
		t.Fatal("v4 全拒规则不应遮蔽 v6 规则")
	}
	if !rep.Rules[2].Reachable {
		t.Fatal("v6 全拒规则应可达")
	}
	if rep.Rules[3].Reachable {
		t.Fatal("v4 全拒规则应遮蔽其后的 v4 规则")
	}
	for _, ov := range rep.Overlaps {
		a, b := rules[ov.Earlier], rules[ov.Later]
		if a.Family() != b.Family() {
			t.Fatalf("跨族重叠不应出现: #%d 与 #%d", ov.Earlier, ov.Later)
		}
	}
	// 匹配器层：v4 包永不命中 v6 规则，反之亦然。
	v4pkt := *rep.Rules[3].Captured
	if rules[2].Matches(v4pkt) {
		t.Fatal("v6 规则不应匹配 v4 包")
	}
}

func TestWitnessReachesRule(t *testing.T) {
	rules := demoRules()
	rep := Analyze(rules)
	for _, rr := range rep.Rules {
		if rr.Reachable {
			if got := Match(rules, *rr.Witness); got != rr.RuleIndex {
				t.Errorf("规则 #%d 的见证包实际命中 #%d", rr.RuleIndex, got)
			}
		} else {
			if got := Match(rules, *rr.Captured); got == rr.RuleIndex {
				t.Errorf("被遮蔽规则 #%d 的示例包不应命中自身", rr.RuleIndex)
			}
		}
	}
}

func TestNoShadowNoOverlap(t *testing.T) {
	rules := []Rule{
		rule("a", "10.0.0.0/8", ProtoTCP, 80, 80, ActionAllow),
		rule("b", "10.0.0.0/8", ProtoUDP, 80, 80, ActionAllow), // 协议不同
		rule("c", "11.0.0.0/8", ProtoTCP, 80, 80, ActionAllow), // 前缀不同
	}
	rep := Analyze(rules)
	for _, rr := range rep.Rules {
		if !rr.Reachable {
			t.Errorf("规则 #%d 不应被遮蔽", rr.RuleIndex)
		}
	}
	if len(rep.Overlaps) != 0 {
		t.Fatalf("不应有重叠，得到 %v", rep.Overlaps)
	}
}
