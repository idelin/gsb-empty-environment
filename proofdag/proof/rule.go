package proof

import (
	"fmt"
	"strconv"
)

// Rule 是一条公开的推理规则：判定“结论是否由有序前提合法推出”。
// 规则集有限且公开，验证时只允许使用登记在 RuleSet 中的规则。
type Rule interface {
	Name() string
	// Arity 返回规则要求的前提个数。
	Arity() int
	// Check 校验 conclusion 是否由有序前提的结论合法推出。
	Check(premises []string, conclusion string) error
}

// RuleSet 是有限公开规则的登记表。
type RuleSet struct {
	rules map[string]Rule
}

func NewRuleSet(rules ...Rule) *RuleSet {
	rs := &RuleSet{rules: make(map[string]Rule, len(rules))}
	for _, r := range rules {
		rs.rules[r.Name()] = r
	}
	return rs
}

func (rs *RuleSet) Lookup(name string) (Rule, bool) {
	r, ok := rs.rules[name]
	return r, ok
}

func (rs *RuleSet) Len() int { return len(rs.rules) }

// --- 内置的有限公开规则：一个极小的整数算术演算 ---

// LitRule 零前提公理规则：结论必须是非负整数字面量。
type LitRule struct{}

func (LitRule) Name() string { return "lit" }
func (LitRule) Arity() int   { return 0 }
func (LitRule) Check(premises []string, conclusion string) error {
	if len(premises) != 0 {
		return fmt.Errorf("lit 是公理，不接受前提，得到 %d 个", len(premises))
	}
	if !isLit(conclusion) {
		return fmt.Errorf("lit 的结论须为非负整数字面量，得到 %q", conclusion)
	}
	return nil
}

func isLit(c string) bool {
	if c == "" {
		return false
	}
	n, err := strconv.ParseInt(c, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == c
}

// AddRule 二前提规则：结论为两前提（须为数字）之和。
type AddRule struct{}

func (AddRule) Name() string { return "add" }
func (AddRule) Arity() int   { return 2 }
func (AddRule) Check(premises []string, conclusion string) error {
	a, b, err := twoNums(premises)
	if err != nil {
		return err
	}
	return expect(conclusion, strconv.FormatInt(a+b, 10))
}

// MulRule 二前提规则：结论为两前提之积。
type MulRule struct{}

func (MulRule) Name() string { return "mul" }
func (MulRule) Arity() int   { return 2 }
func (MulRule) Check(premises []string, conclusion string) error {
	a, b, err := twoNums(premises)
	if err != nil {
		return err
	}
	return expect(conclusion, strconv.FormatInt(a*b, 10))
}

// NegRule 单前提规则：结论为前提的相反数。
type NegRule struct{}

func (NegRule) Name() string { return "neg" }
func (NegRule) Arity() int   { return 1 }
func (NegRule) Check(premises []string, conclusion string) error {
	if len(premises) != 1 {
		return fmt.Errorf("neg 需要 1 个前提，得到 %d", len(premises))
	}
	n, err := strconv.ParseInt(premises[0], 10, 64)
	if err != nil {
		return fmt.Errorf("neg 的前提须为数字: %q", premises[0])
	}
	return expect(conclusion, strconv.FormatInt(-n, 10))
}

func twoNums(premises []string) (int64, int64, error) {
	if len(premises) != 2 {
		return 0, 0, fmt.Errorf("需要 2 个前提，得到 %d", len(premises))
	}
	a, err := strconv.ParseInt(premises[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("前提须为数字: %q", premises[0])
	}
	b, err := strconv.ParseInt(premises[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("前提须为数字: %q", premises[1])
	}
	return a, b, nil
}

func expect(got, want string) error {
	if got != want {
		return fmt.Errorf("结论应为 %q，实际为 %q", want, got)
	}
	return nil
}

// DefaultRules 返回默认的有限公开规则集。
func DefaultRules() *RuleSet {
	return NewRuleSet(LitRule{}, AddRule{}, MulRule{}, NegRule{})
}
