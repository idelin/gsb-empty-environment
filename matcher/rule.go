package matcher

import (
	"errors"
	"fmt"
)

// Validate 检查规则定义是否可用于引擎求值。
//
// 校验内容包括：规则 ID 非空、至少两个段、段别名唯一且非空、事件类型非空、
// 时间窗左闭右开（Low >= 0 且 High > Low）、窗口引用的别名存在且位于前序
// 正段、键相等约束引用的别名存在且属性键非空、以及否定段不能作为零号段。
func (r Rule) Validate() error {
	if r.ID == "" {
		return errors.New("规则 ID 不能为空")
	}
	if len(r.Steps) < 2 {
		return fmt.Errorf("规则 %q 至少需要两个段，当前为 %d 个", r.ID, len(r.Steps))
	}

	aliases := make(map[string]int, len(r.Steps))
	for i, step := range r.Steps {
		if step.Alias == "" {
			return fmt.Errorf("规则 %q 的第 %d 段缺少别名", r.ID, i)
		}
		if _, dup := aliases[step.Alias]; dup {
			return fmt.Errorf("规则 %q 的段别名 %q 重复", r.ID, step.Alias)
		}
		aliases[step.Alias] = i
		if step.EventType == "" {
			return fmt.Errorf("规则 %q 的段 %q 缺少事件类型", r.ID, step.Alias)
		}
		if i == 0 {
			if step.Negate {
				return fmt.Errorf("规则 %q 的零号段 %q 不能是否定段", r.ID, step.Alias)
			}
			continue
		}
		if step.Window.Low < 0 {
			return fmt.Errorf("规则 %q 的段 %q 时间窗下界不能为负", r.ID, step.Alias)
		}
		if step.Window.High <= step.Window.Low {
			return fmt.Errorf("规则 %q 的段 %q 时间窗必须左闭右开且 High > Low", r.ID, step.Alias)
		}
		if step.Window.BaseAlias != "" {
			base, ok := aliases[step.Window.BaseAlias]
			if !ok {
				return fmt.Errorf("规则 %q 的段 %q 引用了不存在的窗口锚点 %q", r.ID, step.Alias, step.Window.BaseAlias)
			}
			if base >= i {
				return fmt.Errorf("规则 %q 的段 %q 的窗口锚点 %q 必须是前序段", r.ID, step.Alias, step.Window.BaseAlias)
			}
			if r.Steps[base].Negate {
				return fmt.Errorf("规则 %q 的段 %q 的窗口锚点 %q 不能是否定段", r.ID, step.Alias, step.Window.BaseAlias)
			}
		}
	}

	for _, group := range r.EqualKeys {
		if group.Key == "" {
			return fmt.Errorf("规则 %q 存在属性键为空的键相等约束", r.ID)
		}
		if len(group.Aliases) < 2 {
			return fmt.Errorf("规则 %q 的键相等约束 %q 至少需要两个段别名", r.ID, group.Key)
		}
		seen := make(map[string]bool, len(group.Aliases))
		for _, alias := range group.Aliases {
			if _, ok := aliases[alias]; !ok {
				return fmt.Errorf("规则 %q 的键相等约束 %q 引用了不存在的段 %q", r.ID, group.Key, alias)
			}
			if seen[alias] {
				return fmt.Errorf("规则 %q 的键相等约束 %q 中段 %q 重复出现", r.ID, group.Key, alias)
			}
			seen[alias] = true
		}
	}
	return nil
}

// positiveSteps 返回规则中全部正段的下标，保持声明顺序。
func (r Rule) positiveSteps() []int {
	out := make([]int, 0, len(r.Steps))
	for i, step := range r.Steps {
		if !step.Negate {
			out = append(out, i)
		}
	}
	return out
}

// keysSatisfied 检查一条绑定路径上的全部键相等约束是否成立。
//
// bound 以段别名记录已绑定事件（正段绑定候选事件，否定段绑定触发违反的事件）。
// 若约束组引用的别名尚未全部绑定，则跳过该组；全部已绑定时要求属性取值一致。
func (r Rule) keysSatisfied(bound map[string]Event) bool {
	for _, group := range r.EqualKeys {
		events := make([]Event, 0, len(group.Aliases))
		for _, alias := range group.Aliases {
			event, ok := bound[alias]
			if !ok {
				continue
			}
			events = append(events, event)
		}
		if len(events) < 2 {
			// 已绑定段不足两个，尚无可比较对象，约束暂不适用。
			continue
		}
		want, ok := events[0].Attrs[group.Key]
		if !ok {
			return false
		}
		for _, event := range events[1:] {
			v, ok := event.Attrs[group.Key]
			if !ok {
				return false
			}
			if v != want {
				return false
			}
		}
	}
	return true
}

// negationKeysSatisfied 专门判定否定段候选事件是否满足键相等约束。
//
// 否定事件是“试探性”放入绑定表的，不能要求它与后续尚未绑定的正段同键，
// 因此只考虑别名全部已绑定的约束组，且跳过包含该否定段自身的组——
// 该组改由 negationGroupMatches 按同组已绑定正段的取值单独判定。
func (r Rule) negationKeysSatisfied(step Step, candidate Event, bound map[string]Event) bool {
	for _, group := range r.EqualKeys {
		includesSelf := false
		for _, alias := range group.Aliases {
			if alias == step.Alias {
				includesSelf = true
				break
			}
		}
		if includesSelf {
			if !r.negationGroupMatches(group, step, candidate, bound) {
				return false
			}
			continue
		}
		events := make([]Event, 0, len(group.Aliases))
		for _, alias := range group.Aliases {
			event, ok := bound[alias]
			if !ok {
				events = nil
				break
			}
			events = append(events, event)
		}
		if len(events) == 0 {
			continue
		}
		want, ok := events[0].Attrs[group.Key]
		if !ok {
			return false
		}
		for _, event := range events[1:] {
			if event.Attrs[group.Key] != want {
				return false
			}
		}
	}
	return true
}

// negationGroupMatches 判定否定候选与同一约束组内所有已绑定正段是否同键。
// 组内尚无任何已绑定段时不施加限制（返回 true）。
func (r Rule) negationGroupMatches(group KeyEquality, step Step, candidate Event, bound map[string]Event) bool {
	candidateVal, ok := candidate.Attrs[group.Key]
	if !ok {
		return false
	}
	compared := false
	for _, alias := range group.Aliases {
		if alias == step.Alias {
			continue
		}
		event, ok := bound[alias]
		if !ok {
			continue
		}
		v, ok := event.Attrs[group.Key]
		if !ok || v != candidateVal {
			return false
		}
		compared = true
	}
	return compared
}
