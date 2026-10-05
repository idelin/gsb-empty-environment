package main

import (
	"reflect"
	"testing"
)

func analyze(t *testing.T, in *Input) *Report {
	t.Helper()
	rep, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze 失败: %v", err)
	}
	verifyWitnesses(t, in, rep)
	return rep
}

func statusOf(t *testing.T, rep *Report, name string) RuleReport {
	t.Helper()
	for _, r := range rep.Rules {
		if r.Rule == name {
			return r
		}
	}
	t.Fatalf("报告中缺少规则 %s", name)
	return RuleReport{}
}

// verifyWitnesses 用语义匹配校验报告中每个见证：它必须匹配本规则，
// 且不匹配任何同 (主体, 动作) 下适用的前序规则。
func verifyWitnesses(t *testing.T, in *Input, rep *Report) {
	t.Helper()
	for i, rr := range rep.Rules {
		if rr.Witness == nil {
			continue
		}
		w := rr.Witness
		path := "/" + joinPath(w.Path)
		self := in.Rules[indexOfRule(t, in, rr.Rule)]
		if !appliesTo(self, w.Principal, w.Action) {
			t.Errorf("%s: 见证 (%s, %s) 不在规则适用域内", rr.Rule, w.Principal, w.Action)
		}
		ok, err := MatchPath(self.Path, path)
		if err != nil || !ok {
			t.Errorf("%s: 见证路径 %s 不匹配自身模式 %s", rr.Rule, path, self.Path)
		}
		for j := 0; j < i; j++ {
			prev := in.Rules[j]
			if !appliesTo(prev, w.Principal, w.Action) {
				continue
			}
			hit, err := MatchPath(prev.Path, path)
			if err == nil && hit {
				t.Errorf("%s: 见证路径 %s 被前序规则 %s 匹配", rr.Rule, path, prev.Name)
			}
		}
	}
}

func joinPath(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}

func indexOfRule(t *testing.T, in *Input, name string) int {
	t.Helper()
	for i, r := range in.Rules {
		if r.Name == name {
			return i
		}
	}
	t.Fatalf("规则 %s 不存在", name)
	return -1
}

func appliesTo(r Rule, principal, action string) bool {
	return (r.Principal == "*" || r.Principal == principal) &&
		(r.Action == "*" || r.Action == action)
}

func TestExactDuplicateFullyShadowed(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "alice", Action: "read", Path: "/a/b"},
			{Name: "r2", Principal: "alice", Action: "read", Path: "/a/b"},
		},
	})
	if got := statusOf(t, rep, "r1").Status; got != StatusUnshadowed {
		t.Fatalf("r1 状态 = %s, 期望 unshadowed", got)
	}
	r2 := statusOf(t, rep, "r2")
	if r2.Status != StatusFull {
		t.Fatalf("r2 状态 = %s, 期望 fully_shadowed", r2.Status)
	}
	if !reflect.DeepEqual(r2.CoveredBy, []string{"r1"}) {
		t.Fatalf("r2 覆盖集 = %v, 期望 [r1]", r2.CoveredBy)
	}
}

// 关键用例：单条前序规则都不包含目标，但并集完全覆盖（跨主体联合遮蔽）。
func TestUnionCoverageAcrossPrincipals(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice", "bob"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "alice", Action: "*", Path: "/x"},
			{Name: "r2", Principal: "bob", Action: "read", Path: "/x"},
			{Name: "r3", Principal: "*", Action: "read", Path: "/x"},
		},
	})
	r3 := statusOf(t, rep, "r3")
	if r3.Status != StatusFull {
		t.Fatalf("r3 状态 = %s, 期望 fully_shadowed", r3.Status)
	}
	if !reflect.DeepEqual(r3.CoveredBy, []string{"r1", "r2"}) {
		t.Fatalf("r3 覆盖集 = %v, 期望 [r1 r2]", r3.CoveredBy)
	}
}

// 部分遮蔽：路径相同但主体域有缺口，见证应落在未被覆盖的主体上。
func TestPartiallyShadowedPrincipalGap(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice", "bob"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "alice", Action: "read", Path: "/a/*"},
			{Name: "r2", Principal: "*", Action: "read", Path: "/a/*"},
		},
	})
	r2 := statusOf(t, rep, "r2")
	if r2.Status != StatusPartial {
		t.Fatalf("r2 状态 = %s, 期望 partially_shadowed", r2.Status)
	}
	if r2.Witness == nil || r2.Witness.Principal != "bob" {
		t.Fatalf("r2 见证 = %+v, 期望主体 bob", r2.Witness)
	}
}

// 部分遮蔽：通配片段与字面量的差集，见证路径应使用"其它"片段名。
func TestPartiallyShadowedOtherSegment(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "*", Action: "read", Path: "/a/b"},
			{Name: "r2", Principal: "*", Action: "read", Path: "/a/*"},
		},
	})
	r2 := statusOf(t, rep, "r2")
	if r2.Status != StatusPartial {
		t.Fatalf("r2 状态 = %s, 期望 partially_shadowed", r2.Status)
	}
	if r2.Witness == nil || len(r2.Witness.Path) != 2 ||
		r2.Witness.Path[0] != "a" || r2.Witness.Path[1] == "b" {
		t.Fatalf("r2 见证路径 = %v, 期望 /a/<非 b>", r2.Witness)
	}
}

func TestDoubleStarSubsumption(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "*", Action: "read", Path: "/a/**"},
			{Name: "r2", Principal: "*", Action: "read", Path: "/a/b/c"},
			{Name: "r3", Principal: "*", Action: "read", Path: "/**"},
		},
	})
	if got := statusOf(t, rep, "r2").Status; got != StatusFull {
		t.Fatalf("r2 状态 = %s, 期望 fully_shadowed", got)
	}
	r3 := statusOf(t, rep, "r3")
	if r3.Status != StatusPartial {
		t.Fatalf("r3 状态 = %s, 期望 partially_shadowed", r3.Status)
	}
	if r3.Witness == nil {
		t.Fatal("r3 缺少见证")
	}
	if len(r3.Witness.Path) > 0 && r3.Witness.Path[0] == "a" {
		t.Fatalf("r3 见证 = %+v, 不应被 r1 覆盖", r3.Witness)
	}
}

// 最小集合：同时存在单独即可覆盖的规则与需联合覆盖的规则对，应选出前者。
func TestMinimalCoverPrefersSingleton(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice", "bob"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "alice", Action: "read", Path: "/x"},
			{Name: "r2", Principal: "bob", Action: "read", Path: "/x"},
			{Name: "r3", Principal: "*", Action: "read", Path: "/x"},
			{Name: "r4", Principal: "*", Action: "read", Path: "/x"},
		},
	})
	r4 := statusOf(t, rep, "r4")
	if r4.Status != StatusFull {
		t.Fatalf("r4 状态 = %s, 期望 fully_shadowed", r4.Status)
	}
	if !reflect.DeepEqual(r4.CoveredBy, []string{"r3"}) {
		t.Fatalf("r4 覆盖集 = %v, 期望 [r3]", r4.CoveredBy)
	}
}

func TestActionDomainGap(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read", "write"},
		Rules: []Rule{
			{Name: "r1", Principal: "*", Action: "read", Path: "/x"},
			{Name: "r2", Principal: "*", Action: "*", Path: "/x"},
		},
	})
	r2 := statusOf(t, rep, "r2")
	if r2.Status != StatusPartial {
		t.Fatalf("r2 状态 = %s, 期望 partially_shadowed", r2.Status)
	}
	if r2.Witness == nil || r2.Witness.Action != "write" {
		t.Fatalf("r2 见证 = %+v, 期望动作 write", r2.Witness)
	}
}

func TestRootAndDoubleStarOnly(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "*", Action: "read", Path: "/"},
			{Name: "r2", Principal: "*", Action: "read", Path: "/**"},
		},
	})
	r2 := statusOf(t, rep, "r2")
	if r2.Status != StatusPartial {
		t.Fatalf("r2 状态 = %s, 期望 partially_shadowed", r2.Status)
	}
	if r2.Witness == nil || len(r2.Witness.Path) == 0 {
		t.Fatalf("r2 见证 = %+v, 期望非空路径", r2.Witness)
	}
}

// 深度不同的联合遮蔽：/**/z 与 /y 联合覆盖 /** 不成立，但 /** 可被 /a/** 与其余规则联合覆盖的场景。
func TestUnionCoverageMixedDepth(t *testing.T) {
	rep := analyze(t, &Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules: []Rule{
			{Name: "r1", Principal: "*", Action: "read", Path: "/a/**"},
			{Name: "r2", Principal: "*", Action: "read", Path: "/**/b"},
			{Name: "r3", Principal: "*", Action: "read", Path: "/**"},
		},
	})
	r3 := statusOf(t, rep, "r3")
	if r3.Status != StatusPartial {
		t.Fatalf("r3 状态 = %s, 期望 partially_shadowed", r3.Status)
	}
	// 见证必须既不以 a 开头也不以 b 结尾。
	w := r3.Witness
	if w == nil || (len(w.Path) > 0 && (w.Path[0] == "a" || w.Path[len(w.Path)-1] == "b")) {
		t.Fatalf("r3 见证 = %+v, 不应被 r1/r2 覆盖", w)
	}
}

func TestValidationErrors(t *testing.T) {
	if _, err := Analyze(&Input{Principals: nil, Actions: []string{"read"}}); err == nil {
		t.Fatal("空主体域应报错")
	}
	if _, err := Analyze(&Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules:      []Rule{{Name: "r1", Principal: "mallory", Action: "read", Path: "/x"}},
	}); err == nil {
		t.Fatal("未知主体应报错")
	}
	if _, err := Analyze(&Input{
		Principals: []string{"alice"},
		Actions:    []string{"read"},
		Rules:      []Rule{{Name: "r1", Principal: "*", Action: "read", Path: "/a*b"}},
	}); err == nil {
		t.Fatal("部分通配片段应报错")
	}
}
