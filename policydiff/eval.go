package policydiff

import "sort"

func sortStrings(s []string) { sort.Strings(s) }

// Eval 在三值语义下评估规则：属性缺失 → Unknown；
// 授权决策中仅 True 放行，Unknown/False 均拒绝。
func Eval(e *Expr, env Env) Tri {
	switch e.Kind {
	case AndExpr:
		v := True
		for _, k := range e.Kids {
			v = and3(v, Eval(k, env))
		}
		return v
	case OrExpr:
		v := False
		for _, k := range e.Kids {
			v = or3(v, Eval(k, env))
		}
		return v
	case NotExpr:
		return not3(Eval(e.Kids[0], env))
	case IntRangeExpr:
		val, ok := env[e.Attr]
		if !ok {
			return Unknown
		}
		if val.Kind == IntKind && e.Lo <= val.Int && val.Int <= e.Hi {
			return True
		}
		return False
	case LabelInExpr:
		val, ok := env[e.Attr]
		if !ok {
			return Unknown
		}
		if val.Kind == LabelKind {
			for _, l := range e.Set {
				if val.Label == l {
					return True
				}
			}
		}
		return False
	default: // BoolExpr
		val, ok := env[e.Attr]
		if !ok {
			return Unknown
		}
		if val.Kind == BoolKind && val.Bool == e.Want {
			return True
		}
		return False
	}
}

// Grants 报告规则在该请求环境下是否放行（deny-by-default）。
func Grants(e *Expr, env Env) bool { return Eval(e, env) == True }
