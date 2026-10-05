package main

import (
	"fmt"
	"strings"
)

// Rule 描述一条权限规则。Principal 和 Action 取自有限域，"*" 表示匹配域中任意值。
// Path 是有限深度的片段模式：字面片段、"*"（恰好一个片段）、"**"（零个或多个片段）。
type Rule struct {
	Name      string `json:"name"`
	Principal string `json:"principal"`
	Action    string `json:"action"`
	Path      string `json:"path"`
}

// Input 是分析器的完整输入。
type Input struct {
	Principals []string `json:"principals"`
	Actions    []string `json:"actions"`
	Rules      []Rule   `json:"rules"`
}

type segmentKind int

const (
	segLiteral segmentKind = iota
	segSingle
	segMulti
)

type segment struct {
	kind segmentKind
	lit  string
}

// parsePattern 把 "/a/b/*" 形式的路径解析为片段序列。根路径 "/" 解析为空序列。
func parsePattern(path string) ([]segment, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, "/")
	segs := make([]segment, 0, len(parts))
	for _, p := range parts {
		switch p {
		case "*":
			segs = append(segs, segment{kind: segSingle})
		case "**":
			segs = append(segs, segment{kind: segMulti})
		case "":
			return nil, fmt.Errorf("路径 %q 含空片段", path)
		default:
			if strings.Contains(p, "*") {
				return nil, fmt.Errorf("不支持部分通配片段 %q", p)
			}
			segs = append(segs, segment{kind: segLiteral, lit: p})
		}
	}
	return segs, nil
}

// compiledRule 携带解析后的模式与该规则适用的 (主体, 动作) 下标对。
type compiledRule struct {
	rule    Rule
	segs    []segment
	applies [][2]int
}

func validateAndCompile(in *Input) ([]compiledRule, []string, error) {
	if len(in.Principals) == 0 {
		return nil, nil, fmt.Errorf("主体域为空")
	}
	if len(in.Actions) == 0 {
		return nil, nil, fmt.Errorf("动作域为空")
	}
	pIndex := map[string]int{}
	for i, p := range in.Principals {
		if _, dup := pIndex[p]; dup {
			return nil, nil, fmt.Errorf("主体 %q 重复", p)
		}
		pIndex[p] = i
	}
	aIndex := map[string]int{}
	for i, a := range in.Actions {
		if _, dup := aIndex[a]; dup {
			return nil, nil, fmt.Errorf("动作 %q 重复", a)
		}
		aIndex[a] = i
	}

	literalSet := map[string]bool{}
	compiled := make([]compiledRule, len(in.Rules))
	names := map[string]bool{}
	for i, r := range in.Rules {
		if r.Name == "" {
			r.Name = fmt.Sprintf("rule-%d", i+1)
		}
		if names[r.Name] {
			return nil, nil, fmt.Errorf("规则名 %q 重复", r.Name)
		}
		names[r.Name] = true

		var pairs [][2]int
		pIdxs, err := domainIndices(r.Principal, in.Principals, pIndex, "主体")
		if err != nil {
			return nil, nil, fmt.Errorf("规则 %q: %v", r.Name, err)
		}
		aIdxs, err := domainIndices(r.Action, in.Actions, aIndex, "动作")
		if err != nil {
			return nil, nil, fmt.Errorf("规则 %q: %v", r.Name, err)
		}
		for _, pi := range pIdxs {
			for _, ai := range aIdxs {
				pairs = append(pairs, [2]int{pi, ai})
			}
		}

		segs, err := parsePattern(r.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("规则 %q: %v", r.Name, err)
		}
		for _, s := range segs {
			if s.kind == segLiteral {
				literalSet[s.lit] = true
			}
		}
		compiled[i] = compiledRule{rule: r, segs: segs, applies: pairs}
	}

	// 字母表 = 所有规则中出现过的字面片段 + 一个代表"任意其它片段"的符号。
	// 模式只测试与字面量的相等性，因此所有非字面量片段行为一致，单个代表元即可精确建模。
	alphabet := make([]string, 0, len(literalSet)+1)
	for lit := range literalSet {
		alphabet = append(alphabet, lit)
	}
	alphabet = append(alphabet, otherSymbol)
	return compiled, alphabet, nil
}

func domainIndices(value string, domain []string, index map[string]int, what string) ([]int, error) {
	if value == "*" {
		all := make([]int, len(domain))
		for i := range domain {
			all[i] = i
		}
		return all, nil
	}
	i, ok := index[value]
	if !ok {
		return nil, fmt.Errorf("%s %q 不在有限域内", what, value)
	}
	return []int{i}, nil
}

// MatchPath 报告解析后的模式是否匹配具体路径片段序列（"*" 匹配恰好一个片段，
// "**" 匹配零个或多个片段）。用于校验分析器给出的见证。
func MatchPath(pattern, path string) (bool, error) {
	segs, err := parsePattern(pattern)
	if err != nil {
		return false, err
	}
	var parts []string
	if trimmed := strings.Trim(path, "/"); trimmed != "" {
		parts = strings.Split(trimmed, "/")
	}
	return matchSegs(segs, parts), nil
}

func matchSegs(segs []segment, parts []string) bool {
	if len(segs) == 0 {
		return len(parts) == 0
	}
	switch segs[0].kind {
	case segLiteral:
		return len(parts) > 0 && parts[0] == segs[0].lit && matchSegs(segs[1:], parts[1:])
	case segSingle:
		return len(parts) > 0 && matchSegs(segs[1:], parts[1:])
	default: // segMulti
		for k := 0; k <= len(parts); k++ {
			if matchSegs(segs[1:], parts[k:]) {
				return true
			}
		}
		return false
	}
}
