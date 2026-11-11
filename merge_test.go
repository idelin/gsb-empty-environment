package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func match(m ...string) map[string]string {
	rule := make(map[string]string, len(m)/2)
	for i := 0; i < len(m); i += 2 {
		rule[m[i]] = m[i+1]
	}
	return rule
}

// bruteEval 按首匹配语义独立评估一张规则表，作为测试参照实现。
func bruteEval(rules []Rule, def string, input map[string]string) string {
	for _, rule := range rules {
		hit := true
		for field, value := range rule.Match {
			if input[field] != value {
				hit = false
				break
			}
		}
		if hit {
			return rule.Decision
		}
	}
	return def
}

func allInputs(domains []FieldDomain) []map[string]string {
	var inputs []map[string]string
	var build func(i int, current map[string]string)
	build = func(i int, current map[string]string) {
		if i == len(domains) {
			inputs = append(inputs, current)
			return
		}
		for _, value := range domains[i].Values {
			next := make(map[string]string, len(current)+1)
			for k, v := range current {
				next[k] = v
			}
			next[domains[i].Name] = value
			build(i+1, next)
		}
	}
	build(0, map[string]string{})
	return inputs
}

func sampleDomains() []FieldDomain {
	return []FieldDomain{
		{Name: "src", Values: []string{"a", "b"}},
		{Name: "dst", Values: []string{"x", "y"}},
		{Name: "proto", Values: []string{"tcp", "udp"}},
	}
}

// 本地与远端在不相交的输入区域上各加一条拒绝规则，必须自动合并且不丢规则。
func TestMergeDisjointEdits(t *testing.T) {
	domains := sampleDomains()
	base := []Rule{{Match: match("src", "a"), Decision: "deny"}}
	local := []Rule{
		{Match: match("src", "a"), Decision: "deny"},
		{Match: match("proto", "udp"), Decision: "deny"},
	}
	remote := []Rule{
		{Match: match("src", "a"), Decision: "deny"},
		{Match: match("dst", "y"), Decision: "deny"},
	}

	merged, err := Merge("allow", domains, base, local, remote)
	if err != nil {
		t.Fatalf("unexpected conflict: %v", err)
	}
	for _, input := range allInputs(domains) {
		got := bruteEval(merged, "allow", input)
		want := "allow"
		if input["src"] == "a" || input["proto"] == "udp" || input["dst"] == "y" {
			want = "deny"
		}
		if got != want {
			t.Fatalf("input %v: got %q want %q", input, got, want)
		}
	}
}

// 动机场景：本地删掉基线拒绝规则，远端同时修改同一条拒绝规则。
// 文本合并且两方各自动了同一区域，结构化合并必须报冲突并给出最小反例。
func TestMergeDenyRuleDeletionConflict(t *testing.T) {
	domains := sampleDomains()
	base := []Rule{{Match: match("src", "a"), Decision: "deny"}}
	local := []Rule{}
	remote := []Rule{
		{Match: match("src", "a"), Decision: "audit"},
	}

	_, err := Merge("allow", domains, base, local, remote)
	var conflict *Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want *Conflict, got %v", err)
	}
	ce := conflict.Counterexample
	if len(ce.Input) != 1 || ce.Input["src"] != "a" {
		t.Fatalf("want minimal counterexample {src:a}, got %v", ce.Input)
	}
	if ce.Base != "deny" || ce.Local != "allow" || ce.Remote != "audit" {
		t.Fatalf("unexpected decisions: %+v", ce)
	}
}

// 单边删除（远端未改动同一区域）应被自动接受，不能误报冲突。
func TestMergeUnilateralDeletionAccepted(t *testing.T) {
	domains := sampleDomains()
	base := []Rule{{Match: match("src", "a"), Decision: "deny"}}
	local := []Rule{}
	remote := []Rule{{Match: match("src", "a"), Decision: "deny"}}

	merged, err := Merge("allow", domains, base, local, remote)
	if err != nil {
		t.Fatalf("unexpected conflict: %v", err)
	}
	for _, input := range allInputs(domains) {
		if got := bruteEval(merged, "allow", input); got != "allow" {
			t.Fatalf("input %v: unilateral deletion not honored, got %q", input, got)
		}
	}
}

// 规则重排改变了首匹配语义，且与远端的独立修改相撞，必须被穷举识别。
func TestMergeReorderSemanticConflict(t *testing.T) {
	domains := sampleDomains()
	base := []Rule{
		{Match: match("src", "a", "dst", "x"), Decision: "deny"},
		{Match: match("src", "a"), Decision: "log"},
	}
	// 本地交换两条重叠规则的顺序：(a,x) 从 deny 变成 log。
	local := []Rule{
		{Match: match("src", "a"), Decision: "log"},
		{Match: match("src", "a", "dst", "x"), Decision: "deny"},
	}
	// 远端独立把 (a,x) 的 deny 改成 audit。
	remote := []Rule{
		{Match: match("src", "a", "dst", "x"), Decision: "audit"},
		{Match: match("src", "a"), Decision: "log"},
	}

	_, err := Merge("allow", domains, base, local, remote)
	var conflict *Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want *Conflict, got %v", err)
	}
	ce := conflict.Counterexample
	if len(ce.Input) != 2 || ce.Input["src"] != "a" || ce.Input["dst"] != "x" {
		t.Fatalf("want minimal counterexample {src:a,dst:x}, got %v", ce.Input)
	}
	if ce.Base != "deny" || ce.Local != "log" || ce.Remote != "audit" {
		t.Fatalf("unexpected decisions: %+v", ce)
	}
}

// 语义等价的规则重排（互斥规则换序）不应阻碍合并。
func TestMergeEquivalentReorder(t *testing.T) {
	domains := sampleDomains()
	base := []Rule{
		{Match: match("proto", "tcp"), Decision: "deny"},
		{Match: match("proto", "udp"), Decision: "allow"},
	}
	local := []Rule{
		{Match: match("proto", "udp"), Decision: "allow"},
		{Match: match("proto", "tcp"), Decision: "deny"},
	}
	remote := []Rule{
		{Match: match("src", "b"), Decision: "deny"},
		{Match: match("proto", "tcp"), Decision: "deny"},
		{Match: match("proto", "udp"), Decision: "allow"},
	}

	merged, err := Merge("allow", domains, base, local, remote)
	if err != nil {
		t.Fatalf("unexpected conflict: %v", err)
	}
	for _, input := range allInputs(domains) {
		got := bruteEval(merged, "allow", input)
		want := "allow"
		if input["proto"] == "tcp" || input["src"] == "b" {
			want = "deny"
		}
		if got != want {
			t.Fatalf("input %v: got %q want %q", input, got, want)
		}
	}
}

// 随机性质测试：随机生成三张表，合并结果逐点与朴素三方语义对齐；
// 报冲突时验证反例真实存在且对每个被省略字段的任意取值仍成立（最小性）。
func TestMergeRandomExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	domains := []FieldDomain{
		{Name: "f0", Values: []string{"0", "1", "2"}},
		{Name: "f1", Values: []string{"0", "1", "2"}},
		{Name: "f2", Values: []string{"0", "1"}},
	}
	decisions := []string{"allow", "deny", "log"}
	fieldNames := []string{"f0", "f1", "f2"}

	randomTable := func() []Rule {
		var rules []Rule
		for range rng.Intn(7) {
			terms := map[string]string{}
			for _, name := range fieldNames {
				if rng.Intn(2) == 0 {
					continue
				}
				var domain FieldDomain
				for _, d := range domains {
					if d.Name == name {
						domain = d
					}
				}
				terms[name] = domain.Values[rng.Intn(len(domain.Values))]
			}
			rules = append(rules, Rule{Match: terms, Decision: decisions[rng.Intn(len(decisions))]})
		}
		return rules
	}

	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		base, local, remote := randomTable(), randomTable(), randomTable()
		merged, err := Merge("allow", domains, base, local, remote)
		inputs := allInputs(domains)

		if err != nil {
			var conflict *Conflict
			if !errors.As(err, &conflict) {
				t.Fatalf("trial %d: unexpected error type %v", trial, err)
			}
			ce := conflict.Counterexample
			compatibleConflict := false
			compatibleCount := 0
			for _, input := range inputs {
				b := bruteEval(base, "allow", input)
				l := bruteEval(local, "allow", input)
				r := bruteEval(remote, "allow", input)
				compatible := true
				for field, value := range ce.Input {
					if input[field] != value {
						compatible = false
					}
				}
				if !compatible {
					continue
				}
				compatibleCount++
				if l != b && r != b && l != r {
					compatibleConflict = true
				}
				// 反例覆盖的每个完整输入都必须是冲突点，且决策与反例一致。
				if !(l != b && r != b && l != r) {
					t.Fatalf("trial %d: counterexample %v covers non-conflict input %v", trial, ce.Input, input)
				}
				// 决策字段非空表示覆盖区内决策恒定，必须逐点一致；
				// 为空表示仅保证处处冲突、决策随自由字段变化。
				if ce.Local != "" && (l != ce.Local || r != ce.Remote || b != ce.Base) {
					t.Fatalf("trial %d: non-uniform counterexample decisions %+v at %v", trial, ce, input)
				}
			}
			if !compatibleConflict || compatibleCount == 0 {
				t.Fatalf("trial %d: counterexample %v does not cover any conflict", trial, ce.Input)
			}
			// 最小性：去掉任意一个受约束字段后，覆盖区内必然存在非冲突点。
			for dropped := range ce.Input {
				allConflict := true
				for _, input := range inputs {
					compatible := true
					for field, value := range ce.Input {
						if field != dropped && input[field] != value {
							compatible = false
						}
					}
					if !compatible {
						continue
					}
					b := bruteEval(base, "allow", input)
					l := bruteEval(local, "allow", input)
					r := bruteEval(remote, "allow", input)
					if !(l != b && r != b && l != r) {
						allConflict = false
						break
					}
				}
				if allConflict {
					t.Fatalf("trial %d: counterexample %v not minimal, field %q is redundant",
						trial, ce.Input, dropped)
				}
			}
			continue
		}

		for _, input := range inputs {
			b := bruteEval(base, "allow", input)
			l := bruteEval(local, "allow", input)
			r := bruteEval(remote, "allow", input)
			// 与 Merge 的逐点规则保持一致：先取本地改动，其次远端改动，否则基线。
			want := b
			localChanged := l != b
			remoteChanged := r != b
			if localChanged && remoteChanged && l != r {
				t.Fatalf("trial %d: undetected conflict at %v (b=%q l=%q r=%q)",
					trial, input, b, l, r)
			}
			if localChanged {
				want = l
			} else if remoteChanged {
				want = r
			}
			if got := bruteEval(merged, "allow", input); got != want {
				t.Fatalf("trial %d: input %v got %q want %q (b=%q l=%q r=%q)",
					trial, input, got, want, b, l, r)
			}
		}
	}
}

func TestMergeRejectsOversizedSpace(t *testing.T) {
	domains := []FieldDomain{
		{Name: "a", Values: uniqueValues(101)},
		{Name: "b", Values: uniqueValues(101)},
		{Name: "c", Values: uniqueValues(101)},
	}
	if _, err := Merge("allow", domains, nil, nil, nil); err == nil {
		t.Fatal("want error for input space above 1e6")
	}
}

func uniqueValues(n int) []string {
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("v%03d", i)
	}
	return values
}

func TestMergeRejectsInvalidRules(t *testing.T) {
	domains := sampleDomains()
	valid := []Rule{{Match: match("src", "a"), Decision: "deny"}}
	badField := []Rule{{Match: match("nope", "a"), Decision: "deny"}}
	if _, err := Merge("allow", domains, badField, valid, valid); err == nil {
		t.Fatal("want error for unknown field")
	}
	badValue := []Rule{{Match: match("src", "z"), Decision: "deny"}}
	if _, err := Merge("allow", domains, valid, badValue, valid); err == nil {
		t.Fatal("want error for value outside domain")
	}
	dupDomain := []FieldDomain{{Name: "x", Values: []string{"1", "1"}}}
	if _, err := Merge("allow", dupDomain, nil, nil, nil); err == nil {
		t.Fatal("want error for duplicate domain values")
	}
}

// 最小反例覆盖区内若决策随自由字段变化，决策字段留空但冲突处处成立。
func TestMergeCounterexampleVaryingDecisions(t *testing.T) {
	domains := []FieldDomain{
		{Name: "k", Values: []string{"0", "1"}},
		{Name: "v", Values: []string{"x", "y"}},
	}
	// 基线按 v 区分两种决策；本地整体改成 log；远端整体改成 audit。
	// 冲突覆盖整个输入空间，最小反例为空输入，且 base 决策随 v 变化而不恒定。
	base := []Rule{
		{Match: match("v", "x"), Decision: "deny"},
		{Match: match("v", "y"), Decision: "allow"},
	}
	local := []Rule{{Match: nil, Decision: "log"}}
	remote := []Rule{{Match: nil, Decision: "audit"}}

	_, err := Merge("allow", domains, base, local, remote)
	var conflict *Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want *Conflict, got %v", err)
	}
	ce := conflict.Counterexample
	if len(ce.Input) != 0 {
		t.Fatalf("want empty minimal counterexample, got %v", ce.Input)
	}
	if ce.Local != "" || ce.Remote != "" || ce.Base != "" {
		t.Fatalf("want empty decision labels for varying subcube, got %+v", ce)
	}
}

// 无字段时输入空间只有一个点（空合取），合并退化为单点三方选择。
func TestMergeZeroFields(t *testing.T) {
	base := []Rule{{Match: nil, Decision: "deny"}}
	local := []Rule{{Match: nil, Decision: "log"}}
	remote := []Rule{{Match: nil, Decision: "deny"}}

	merged, err := Merge("allow", nil, base, local, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bruteEval(merged, "allow", map[string]string{}); got != "log" {
		t.Fatalf("got %q want log", got)
	}

	remoteConflict := []Rule{{Match: nil, Decision: "audit"}}
	if _, err := Merge("allow", nil, base, local, remoteConflict); err == nil {
		t.Fatal("want conflict at the single input point")
	}
}

// 百万组合上限下的规模冒烟测试，确认内存与耗时可控。
func TestMergeAtMillionCombinations(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in short mode")
	}
	domains := []FieldDomain{
		{Name: "a", Values: uniqueValues(100)},
		{Name: "b", Values: uniqueValues(100)},
		{Name: "c", Values: uniqueValues(100)},
	}
	base := []Rule{{Match: map[string]string{"a": "v000"}, Decision: "deny"}}
	local := []Rule{
		{Match: map[string]string{"a": "v000"}, Decision: "deny"},
		{Match: map[string]string{"a": "v001"}, Decision: "log"},
	}
	remote := []Rule{
		{Match: map[string]string{"a": "v000"}, Decision: "deny"},
		{Match: map[string]string{"a": "v002"}, Decision: "deny"},
	}
	merged, err := Merge("allow", domains, base, local, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged) == 0 {
		t.Fatal("merged table should contain the independent edits")
	}
}
