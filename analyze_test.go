package netsec

import (
	"reflect"
	"testing"
)

// basePolicy 构造 user_t -> web_t -> db_t 的两跳域转换策略，
// 每条边都配齐 transition、entrypoint、execute 三条权限。
func basePolicy() *Policy {
	return &Policy{
		Allows: []AllowRule{
			{Source: "user_t", Target: "web_t", Class: "process", Perms: []string{"transition"}},
			{Source: "web_t", Target: "web_exec_t", Class: "file", Perms: []string{"entrypoint"}},
			{Source: "user_t", Target: "web_exec_t", Class: "file", Perms: []string{"execute", "read"}},
			{Source: "web_t", Target: "db_t", Class: "process", Perms: []string{"transition"}},
			{Source: "db_t", Target: "db_exec_t", Class: "file", Perms: []string{"entrypoint"}},
			{Source: "web_t", Target: "db_exec_t", Class: "file", Perms: []string{"execute"}},
		},
		Transitions: []TypeTransitionRule{
			{Source: "user_t", Target: "web_exec_t", Class: "process", Default: "web_t"},
			{Source: "web_t", Target: "db_exec_t", Class: "process", Default: "db_t"},
		},
	}
}

func TestMultiHopReachable(t *testing.T) {
	rep := Analyze(basePolicy(), "user_t")

	want := []string{"db_t", "web_t"}
	if !reflect.DeepEqual(rep.Reachable, want) {
		t.Fatalf("Reachable = %v, want %v", rep.Reachable, want)
	}

	chain := rep.Chains["db_t"]
	wantChain := []Step{
		{From: "user_t", To: "web_t", FileType: "web_exec_t"},
		{From: "web_t", To: "db_t", FileType: "db_exec_t"},
	}
	if !reflect.DeepEqual(chain, wantChain) {
		t.Fatalf("Chains[db_t] = %v, want %v", chain, wantChain)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("Violations = %v, want none", rep.Violations)
	}
}

func TestMissingEntrypointBreaksChain(t *testing.T) {
	p := basePolicy()
	// 移除 db_t 对 db_exec_t 的 entrypoint 权限，第二跳断链。
	filtered := p.Allows[:0]
	for _, a := range p.Allows {
		if a.Source == "db_t" && a.Target == "db_exec_t" {
			continue
		}
		filtered = append(filtered, a)
	}
	p.Allows = filtered

	rep := Analyze(p, "user_t")
	want := []string{"web_t"}
	if !reflect.DeepEqual(rep.Reachable, want) {
		t.Fatalf("Reachable = %v, want %v", rep.Reachable, want)
	}
	if _, ok := rep.Chains["db_t"]; ok {
		t.Fatalf("db_t should be unreachable without entrypoint permission")
	}
}

func TestAttributeExpandedNeverallow(t *testing.T) {
	p := basePolicy()
	// allow 侧也使用属性，验证双向展开。
	p.Attributes = map[string][]string{
		"priv_domain": {"db_t"},
		"low_domain":  {"user_t", "web_t"},
	}
	for i, a := range p.Allows {
		if a.Source == "web_t" && a.Target == "db_t" {
			p.Allows[i].Target = "priv_domain"
		}
	}
	p.Neverallows = []NeverallowRule{
		{Source: "low_domain", Target: "priv_domain", Class: "process", Perms: []string{"transition"}},
	}

	rep := Analyze(p, "user_t")

	if len(rep.Violations) != 1 {
		t.Fatalf("Violations = %v, want exactly 1", rep.Violations)
	}
	v := rep.Violations[0]
	if v.Source != "web_t" || v.Target != "db_t" || v.Perm != "transition" {
		t.Fatalf("Violation = %+v, want web_t -> db_t transition", v)
	}
	wantChain := []Step{
		{From: "user_t", To: "web_t", FileType: "web_exec_t"},
		{From: "web_t", To: "db_t", FileType: "db_exec_t"},
	}
	if !reflect.DeepEqual(v.Chain, wantChain) {
		t.Fatalf("Violation.Chain = %v, want %v", v.Chain, wantChain)
	}
}

func TestLegalChainWithDontaudit(t *testing.T) {
	p := basePolicy()
	// dontaudit 命中转换权限，但不应影响放行。
	p.Dontaudits = []AllowRule{
		{Source: "user_t", Target: "web_t", Class: "process", Perms: []string{"transition"}},
		{Source: "web_t", Target: "db_t", Class: "process", Perms: []string{"transition"}},
	}
	// neverallow 针对无关属性，不应命中。
	p.Attributes = map[string][]string{"unrelated": {"null_t"}}
	p.Neverallows = []NeverallowRule{
		{Source: "user_t", Target: "unrelated", Class: "process", Perms: []string{"transition"}},
	}

	rep := Analyze(p, "user_t")
	want := []string{"db_t", "web_t"}
	if !reflect.DeepEqual(rep.Reachable, want) {
		t.Fatalf("Reachable = %v, want %v (dontaudit must not block)", rep.Reachable, want)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("Violations = %v, want none", rep.Violations)
	}
}
