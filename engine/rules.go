package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RuleKind 规则类型：重命名、删除、合成。
type RuleKind string

const (
	RuleRename RuleKind = "rename"
	RuleDelete RuleKind = "delete"
	RuleSynth  RuleKind = "synth"
)

// Rule 一条标签重写规则。Order 显式指定应用顺序，必须全局唯一。
type Rule struct {
	ID    string   `json:"id"`
	Order int      `json:"order"`
	Kind  RuleKind `json:"kind"`
	// rename
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// delete
	Label string `json:"label,omitempty"`
	// synth：Template 中 {name} 引用当前标签值
	Target   string `json:"target,omitempty"`
	Template string `json:"template,omitempty"`
}

// RuleSet 一批原子发布的规则。Merge 为 true 表示显式聚合模式，
// 只有此时多个输入系列映射到同一输出才允许合并。
type RuleSet struct {
	Version  uint64   `json:"version"`
	Merge    bool     `json:"merge"`
	Reserved []string `json:"reserved,omitempty"`
	Rules    []Rule   `json:"rules"`
}

// Compile 校验并整理一批规则：顺序唯一、字段完整、保留标签不可被改写。
// 任何一条非法都会导致整批失败，调用方据此实现原子发布。
func Compile(version uint64, merge bool, reserved []string, rules []Rule) (*RuleSet, error) {
	if version == 0 {
		return nil, errors.New("ruleset version must be >= 1")
	}
	res := make(map[string]bool, len(reserved))
	for _, r := range reserved {
		if r == "" {
			return nil, errors.New("empty reserved label name")
		}
		res[r] = true
	}
	ids := make(map[string]bool, len(rules))
	orders := make(map[int]bool, len(rules))
	for _, r := range rules {
		if r.ID == "" {
			return nil, errors.New("rule with empty id")
		}
		if ids[r.ID] {
			return nil, fmt.Errorf("duplicate rule id %q", r.ID)
		}
		ids[r.ID] = true
		if orders[r.Order] {
			return nil, fmt.Errorf("rule %q: duplicate order %d", r.ID, r.Order)
		}
		orders[r.Order] = true
		switch r.Kind {
		case RuleRename:
			if r.From == "" || r.To == "" {
				return nil, fmt.Errorf("rule %q: rename requires from and to", r.ID)
			}
			if res[r.From] || res[r.To] {
				return nil, fmt.Errorf("rule %q: reserved label cannot be rewritten", r.ID)
			}
		case RuleDelete:
			if r.Label == "" {
				return nil, fmt.Errorf("rule %q: delete requires label", r.ID)
			}
			if res[r.Label] {
				return nil, fmt.Errorf("rule %q: reserved label cannot be rewritten", r.ID)
			}
		case RuleSynth:
			if r.Target == "" || r.Template == "" {
				return nil, fmt.Errorf("rule %q: synth requires target and template", r.ID)
			}
			if res[r.Target] {
				return nil, fmt.Errorf("rule %q: reserved label cannot be rewritten", r.ID)
			}
		default:
			return nil, fmt.Errorf("rule %q: unknown kind %q", r.ID, r.Kind)
		}
	}
	sorted := append([]Rule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })
	return &RuleSet{
		Version:  version,
		Merge:    merge,
		Reserved: append([]string(nil), reserved...),
		Rules:    sorted,
	}, nil
}

// Apply 按显式顺序对一组标签应用全部规则。
func (rs *RuleSet) Apply(in LabelSet) LabelSet {
	out := in.Clone()
	for _, r := range rs.Rules {
		switch r.Kind {
		case RuleRename:
			if v, ok := out[r.From]; ok {
				delete(out, r.From)
				out[r.To] = v
			}
		case RuleDelete:
			delete(out, r.Label)
		case RuleSynth:
			if v, ok := renderTemplate(r.Template, out); ok {
				out[r.Target] = v
			}
		}
	}
	return out
}

// renderTemplate 展开 {name} 占位符；任一引用标签缺失则整条合成规则不生效。
func renderTemplate(tmpl string, ls LabelSet) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		if tmpl[i] == '{' {
			j := strings.IndexByte(tmpl[i:], '}')
			if j < 0 {
				return "", false
			}
			v, ok := ls[tmpl[i+1:i+j]]
			if !ok {
				return "", false
			}
			b.WriteString(v)
			i += j + 1
		} else {
			b.WriteByte(tmpl[i])
			i++
		}
	}
	return b.String(), true
}
