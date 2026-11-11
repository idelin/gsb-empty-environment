package netsec

// Decision 是策略服务据此执行的三态动作。
type Decision int8

const (
	// Reject 表示规则求值为 Fail，拒收邮件。
	Reject Decision = iota
	// Pending 表示规则求值为 Unknown，证据不足、暂待定。
	Pending
	// Allow 表示规则求值为 Pass，放行邮件。
	Allow
)

// String 返回决策的规范名称。
func (d Decision) String() string {
	switch d {
	case Reject:
		return "reject"
	case Pending:
		return "pending"
	default:
		return "allow"
	}
}

// Decide 把三值结果映射为动作：Fail 拒收、Unknown 待定、Pass 放行。
func Decide(v Value) Decision {
	switch v {
	case Fail:
		return Reject
	case Pass:
		return Allow
	default:
		return Pending
	}
}

// Eval 在给定事实赋值下求规则值。
//
// facts 中未出现的事实按 Unknown 处理（缺失不等于失败）；不属于本规则
// Compiler 的事实 ID 会被忽略。DAG 按节点记忆化求值，共享子式只计算一次。
func (r *Rule) Eval(facts map[string]Value) Value {
	memo := make(map[*Rule]Value)
	return r.eval(facts, memo)
}

// Decide 等价于 Decide(rule.Eval(facts))。
func (r *Rule) Decide(facts map[string]Value) Decision {
	return Decide(r.Eval(facts))
}

func (r *Rule) eval(facts map[string]Value, memo map[*Rule]Value) Value {
	if v, ok := memo[r]; ok {
		return v
	}
	var result Value
	switch r.op {
	case opFact:
		result = Unknown
		if v, ok := facts[r.factID]; ok {
			result = v
		}
	case opNot:
		result = Not(r.children[0].eval(facts, memo))
	case opAnd:
		result = Pass
		for _, child := range r.children {
			childResult := child.eval(facts, memo)
			if childResult == Fail {
				result = Fail
				break
			}
			if childResult == Unknown {
				result = Unknown
			}
		}
	case opOr:
		result = Fail
		for _, child := range r.children {
			childResult := child.eval(facts, memo)
			if childResult == Pass {
				result = Pass
				break
			}
			if childResult == Unknown {
				result = Unknown
			}
		}
	default:
		panic("netsec: unknown rule op " + r.op)
	}
	memo[r] = result
	return result
}
