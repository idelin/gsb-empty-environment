package policycheck

import (
	"fmt"
	"sort"
)

// Rule 是有序参考规则链中的一条规则。
type Rule struct {
	ID      string   `json:"id,omitempty"`
	When    *Pred    `json:"when"`
	Action  Action   `json:"action"`
	Payload Payload  `json:"payload,omitempty"`
	Audit   []string `json:"audit,omitempty"`
	Notify  []string `json:"notify,omitempty"`
}

// Policy 是双架构有序参考规则链。
type Policy struct {
	Rules          []Rule      `json:"rules"`
	DefaultAction  Action      `json:"default_action,omitempty"`
	DefaultPayload Payload     `json:"default_payload,omitempty"`
	Aliases        *AliasTable `json:"aliases,omitempty"`
}

// AliasTable 将规范调用名映射到各架构的独立调用号。
//
// 同名调用在 x86_64 与 ia32 下通常具有不同号码；号码相同也不代表
// 语义相同。所有校验区域始终以 (Arch, Nr) 为键。
type AliasTable struct {
	NameToNumbers map[string]map[Arch]uint64 `json:"name_to_numbers"`
}

// NewAliasTable 创建别名表。
func NewAliasTable() *AliasTable {
	return &AliasTable{NameToNumbers: make(map[string]map[Arch]uint64)}
}

// Bind 绑定一个规范调用名在两个架构下的号码。
func (t *AliasTable) Bind(name string, x86_64NR, ia32NR uint64) *AliasTable {
	if t.NameToNumbers == nil {
		t.NameToNumbers = make(map[string]map[Arch]uint64)
	}
	t.NameToNumbers[name] = map[Arch]uint64{
		ArchX86_64: x86_64NR,
		ArchIA32:   ia32NR,
	}
	return t
}

// Predicate 返回“指定规范调用名在当前架构下号码”的谓词。
func (t *AliasTable) Predicate(name string) (*Pred, error) {
	numbers, ok := t.NameToNumbers[name]
	if !ok {
		return nil, fmt.Errorf("policycheck: unknown syscall alias %q", name)
	}
	var archPreds []*Pred
	for arch, nr := range numbers {
		archPreds = append(archPreds, And(ArchIn(arch), NrIs(nr)))
	}
	sort.Slice(archPreds, func(i, j int) bool {
		return archPreds[i].Expression() < archPreds[j].Expression()
	})
	return Or(archPreds...), nil
}

// DefaultSyscallAliases 返回少量常用双架构调用号，供测试和宿主参考。
//
// 该表不试图覆盖全部内核版本；宿主可传入自己的权威表。
func DefaultSyscallAliases() *AliasTable {
	return NewAliasTable().
		Bind("read", 0, 3).
		Bind("write", 1, 4).
		Bind("open", 2, 5).
		Bind("close", 3, 6).
		Bind("execve", 59, 11).
		Bind("exit", 60, 1).
		Bind("openat", 257, 295)
}

func (p *Policy) defaultDecision() Decision {
	action := p.DefaultAction
	if action == "" {
		action = ActionAllow
	}
	return Decision{Action: action, Payload: p.DefaultPayload.ClonePayload()}
}

// ClonePayload 是 Payload.Clone 的便捷方法。
func (payload Payload) ClonePayload() Payload {
	out := payload
	if payload.Data != nil {
		out.Data = append([]byte(nil), payload.Data...)
	}
	if payload.PassthroughNR != nil {
		nr := *payload.PassthroughNR
		out.PassthroughNR = &nr
	}
	return out
}

// Validate 校验策略。
func (p *Policy) Validate() error {
	if p == nil {
		return fmt.Errorf("policycheck: nil policy")
	}
	if p.DefaultAction != "" && !p.DefaultAction.Valid() {
		return fmt.Errorf("policycheck: invalid default action %q", p.DefaultAction)
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.When == nil {
			return fmt.Errorf("policycheck: rule %d has nil predicate", i)
		}
		if err := r.When.Validate(); err != nil {
			return fmt.Errorf("policycheck: rule %d (%s): %w", i, r.ID, err)
		}
		if !r.Action.Valid() {
			return fmt.Errorf("policycheck: rule %d (%s): invalid action %q", i, r.ID, r.Action)
		}
	}
	return nil
}

func (p *Policy) ruleID(i int) string {
	if id := p.Rules[i].ID; id != "" {
		return id
	}
	return fmt.Sprintf("rule-%03d", i)
}

// Evaluate 按有序参考链语义求值。
//
// 所有匹配规则的 Audit/Notify 均按规则顺序保留；动作取最高优先级；
// 相同动作优先级时，链中更靠后（更近）的匹配规则载荷覆盖先前载荷。
func (p *Policy) Evaluate(in Input, reg Registry) (Decision, error) {
	if err := p.Validate(); err != nil {
		return Decision{}, err
	}
	if _, ok := NormalizeArch(string(in.Arch)); !ok {
		return Decision{}, fmt.Errorf("policycheck: unsupported input architecture %q", in.Arch)
	}

	result := p.defaultDecision()
	bestRank := result.Action.Rank()
	var audit []string
	var notify []string

	for i := range p.Rules {
		rule := &p.Rules[i]
		matched, err := rule.When.Eval(in, reg)
		if err != nil {
			return Decision{}, fmt.Errorf("policycheck: rule %s: %w", p.ruleID(i), err)
		}
		if !matched {
			continue
		}

		audit = append(audit, rule.Audit...)
		notify = append(notify, rule.Notify...)

		rank := rule.Action.Rank()
		if rank >= bestRank {
			bestRank = rank
			result.Action = rule.Action
			result.Payload = rule.Payload.ClonePayload()
		}
	}

	result.Audit = audit
	result.Notify = notify
	return result, nil
}

// EvaluateSymbolic 使用每个原子谓词的真值赋值求参考链。
func (p *Policy) EvaluateSymbolic(values map[string]bool) (Decision, error) {
	if err := p.Validate(); err != nil {
		return Decision{}, err
	}
	result := p.defaultDecision()
	bestRank := result.Action.Rank()
	var audit []string
	var notify []string

	for i := range p.Rules {
		rule := &p.Rules[i]
		matched, err := evalSymbolic(rule.When, values)
		if err != nil {
			return Decision{}, fmt.Errorf("policycheck: rule %s: %w", p.ruleID(i), err)
		}
		if !matched {
			continue
		}
		audit = append(audit, rule.Audit...)
		notify = append(notify, rule.Notify...)
		if rank := rule.Action.Rank(); rank >= bestRank {
			bestRank = rank
			result.Action = rule.Action
			result.Payload = rule.Payload.ClonePayload()
		}
	}
	result.Audit = audit
	result.Notify = notify
	return result, nil
}

func evalSymbolic(p *Pred, values map[string]bool) (bool, error) {
	if p == nil {
		return false, fmt.Errorf("policycheck: nil predicate in symbolic evaluation")
	}
	switch p.Kind {
	case KindAnd:
		for _, c := range p.Children {
			v, err := evalSymbolic(c, values)
			if err != nil {
				return false, err
			}
			if !v {
				return false, nil
			}
		}
		return true, nil
	case KindOr:
		for _, c := range p.Children {
			v, err := evalSymbolic(c, values)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	case KindNot:
		v, err := evalSymbolic(p.Children[0], values)
		return !v, err
	default:
		key := p.AtomKey()
		v, ok := values[key]
		if !ok {
			return false, fmt.Errorf("policycheck: missing symbolic assignment for %s", p.Expression())
		}
		return v, nil
	}
}
