package netsec

import (
	"fmt"
	"reflect"
)

// Decision 是授权决定。
type Decision string

const (
	// DecisionAllow 表示至少一条 permit 生效且没有 forbid 拦截。
	DecisionAllow Decision = "allow"
	// DecisionDeny 表示默认拒绝、forbid 拦截或 failClosed 下的出错拒绝。
	DecisionDeny Decision = "deny"
)

// PolicyStatus 是单条策略在本次请求中的求值状态。
type PolicyStatus string

const (
	// StatusMatched 表示条件求值成功且为真。
	StatusMatched PolicyStatus = "matched"
	// StatusNotMatched 表示条件求值成功且为假。
	StatusNotMatched PolicyStatus = "not_matched"
	// StatusError 表示条件求值出错（缺属性、类型不符等）。
	StatusError PolicyStatus = "error"
)

// Request 是一次授权请求的三元组。
// 属性值使用 map[string]any 嵌套表达，集合使用 []any。
type Request struct {
	Principal map[string]any
	Action    map[string]any
	Resource  map[string]any
}

// PolicyDiagnostic 记录单条策略的求值结果。
type PolicyDiagnostic struct {
	PolicyID string
	Effect   Effect
	Status   PolicyStatus
	// Error 仅在 Status == StatusError 时非空，描述缺属性或类型不符等原因。
	Error string
}

// Result 是一次授权请求的完整输出。
type Result struct {
	Decision    Decision
	FailClosed  bool
	Diagnostics []PolicyDiagnostic
}

// EvalError 描述一次条件求值中出现的错误。
type EvalError struct {
	Reason string
}

func (e *EvalError) Error() string { return e.Reason }

func attrNotFound(attr string) error {
	return &EvalError{Reason: fmt.Sprintf("attribute %q does not exist", attr)}
}

func typeMismatch(msg string) error {
	return &EvalError{Reason: "type mismatch: " + msg}
}

type evalContext struct {
	req *Request
}

// EvalCondition 对外暴露的条件求值入口，便于调用方单独调试表达式。
func EvalCondition(expr Expr, req *Request) (bool, error) {
	return (&evalContext{req: req}).evalBool(expr)
}

func (c *evalContext) evalBool(expr Expr) (bool, error) {
	value, err := c.eval(expr)
	if err != nil {
		return false, err
	}
	b, ok := value.(bool)
	if !ok {
		return false, typeMismatch(fmt.Sprintf("when condition must evaluate to bool, got %s", typeName(value)))
	}
	return b, nil
}

func (c *evalContext) eval(expr Expr) (any, error) {
	switch node := expr.(type) {
	case *VarExpr:
		return c.lookupVar(node.Name)
	case *LiteralExpr:
		return node.Value, nil
	case *SetExpr:
		values := make([]any, 0, len(node.Elements))
		for _, element := range node.Elements {
			value, err := c.eval(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case *AttrExpr:
		return c.evalAttr(node)
	case *HasExpr:
		return c.evalHas(node)
	case *InExpr:
		return c.evalIn(node)
	case *BinaryExpr:
		return c.evalBinary(node)
	default:
		return nil, typeMismatch(fmt.Sprintf("unknown expression %T", expr))
	}
}

func (c *evalContext) lookupVar(name string) (map[string]any, error) {
	switch name {
	case "principal":
		return c.req.Principal, nil
	case "action":
		return c.req.Action, nil
	case "resource":
		return c.req.Resource, nil
	default:
		return nil, &EvalError{Reason: fmt.Sprintf("unknown variable %q", name)}
	}
}

func (c *evalContext) evalAttr(node *AttrExpr) (any, error) {
	target, err := c.eval(node.Target)
	if err != nil {
		return nil, err
	}
	record, ok := target.(map[string]any)
	if !ok {
		return nil, typeMismatch(fmt.Sprintf("cannot access .%s on %s", node.Name, typeName(target)))
	}
	value, ok := record[node.Name]
	if !ok {
		return nil, attrNotFound(node.Name)
	}
	return value, nil
}

// has 受保护：仅当目标是记录且缺少该键时返回 false，不会因为属性缺失出错。
// 目标本身求值失败、目标不是记录、属性名不是字符串时仍按错误处理。
func (c *evalContext) evalHas(node *HasExpr) (bool, error) {
	target, err := c.eval(node.Target)
	if err != nil {
		return false, err
	}
	record, ok := target.(map[string]any)
	if !ok {
		return false, typeMismatch(fmt.Sprintf("has requires a record on the left, got %s", typeName(target)))
	}
	attrValue, err := c.eval(node.Attr)
	if err != nil {
		return false, err
	}
	attr, ok := attrValue.(string)
	if !ok {
		return false, typeMismatch(fmt.Sprintf("has attribute name must be a string, got %s", typeName(attrValue)))
	}
	_, ok = record[attr]
	return ok, nil
}

// in 采用集合成员语义：右侧必须是数组/切片，左侧与任一元素深度相等即真。
func (c *evalContext) evalIn(node *InExpr) (bool, error) {
	item, err := c.eval(node.Item)
	if err != nil {
		return false, err
	}
	scope, err := c.eval(node.Scope)
	if err != nil {
		return false, err
	}
	rv := reflect.ValueOf(scope)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return false, typeMismatch(fmt.Sprintf("in requires a set on the right, got %s", typeName(scope)))
	}
	for i := 0; i < rv.Len(); i++ {
		if valuesEqual(item, rv.Index(i).Interface()) {
			return true, nil
		}
	}
	return false, nil
}

func (c *evalContext) evalBinary(node *BinaryExpr) (any, error) {
	// && 与 || 短路，避免求值未被 has 保护的另一侧。
	if node.Op == "&&" {
		left, err := c.boolOperand(node.Op, node.LHS)
		if err != nil || !left {
			return false, err
		}
		return c.boolOperand(node.Op, node.RHS)
	}
	if node.Op == "||" {
		left, err := c.boolOperand(node.Op, node.LHS)
		if err != nil {
			return false, err
		}
		if left {
			return true, nil
		}
		return c.boolOperand(node.Op, node.RHS)
	}

	left, err := c.eval(node.LHS)
	if err != nil {
		return nil, err
	}
	right, err := c.eval(node.RHS)
	if err != nil {
		return nil, err
	}

	switch node.Op {
	case "==":
		return valuesEqual(left, right), nil
	case "!=":
		return !valuesEqual(left, right), nil
	case "<", ">", "<=", ">=":
		return compareOrdered(node.Op, left, right)
	default:
		return nil, typeMismatch(fmt.Sprintf("unsupported operator %q", node.Op))
	}
}

func (c *evalContext) boolOperand(op string, expr Expr) (bool, error) {
	value, err := c.eval(expr)
	if err != nil {
		return false, err
	}
	b, ok := value.(bool)
	if !ok {
		return false, typeMismatch(fmt.Sprintf("operands of %q must be bool, got %s", op, typeName(value)))
	}
	return b, nil
}

func compareOrdered(op string, left, right any) (bool, error) {
	ln, lok := toFloat(left)
	rn, rok := toFloat(right)
	if lok && rok {
		switch op {
		case "<":
			return ln < rn, nil
		case ">":
			return ln > rn, nil
		case "<=":
			return ln <= rn, nil
		case ">=":
			return ln >= rn, nil
		}
	}
	ls, lok := left.(string)
	rs, rok := right.(string)
	if lok && rok {
		switch op {
		case "<":
			return ls < rs, nil
		case ">":
			return ls > rs, nil
		case "<=":
			return ls <= rs, nil
		case ">=":
			return ls >= rs, nil
		}
	}
	return false, typeMismatch(fmt.Sprintf("operator %q requires two numbers or two strings, got %s and %s",
		op, typeName(left), typeName(right)))
}

func toFloat(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	default:
		return 0, false
	}
}

// == / != 对不同类型直接判不相等，不视为类型错误（与 Cedar 一致）。
func valuesEqual(left, right any) bool {
	if ln, lok := toFloat(left); lok {
		if rn, rok := toFloat(right); rok {
			return ln == rn
		}
		return false
	}
	if reflect.TypeOf(left) != reflect.TypeOf(right) {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func typeName(value any) string {
	if value == nil {
		return "null"
	}
	switch value.(type) {
	case map[string]any:
		return "record"
	case []any:
		return "set"
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int64, float32, float64:
		return "number"
	default:
		return reflect.TypeOf(value).String()
	}
}

// Evaluate 对请求执行全部策略并组合出最终决定。
//
// 组合规则（Cedar 子集）：
//   - 默认拒绝；
//   - 任一 forbid 条件为真，立即拒绝（forbid 优先于 permit）；
//   - 策略求值出错时标记为 error 并视为不适用；
//   - 出错的是 forbid 且 failClosed 为 true，整个请求拒绝；
//   - failClosed 为 false 时出错的 forbid 被跳过，
//     只有存在求值成功且条件为真的 permit 才放行。
func Evaluate(policies []*Policy, req *Request, failClosed bool) *Result {
	result := &Result{
		Decision:   DecisionDeny,
		FailClosed: failClosed,
	}

	permitMatched := false
	for _, policy := range policies {
		diag := PolicyDiagnostic{
			PolicyID: policy.ID,
			Effect:   policy.Effect,
		}

		matched, err := evalPolicy(policy, req)
		switch {
		case err != nil:
			diag.Status = StatusError
			diag.Error = err.Error()
			if policy.Effect == Forbid && failClosed {
				result.Diagnostics = append(result.Diagnostics, diag)
				return result
			}
		case matched:
			diag.Status = StatusMatched
			if policy.Effect == Forbid {
				result.Diagnostics = append(result.Diagnostics, diag)
				return result
			}
			permitMatched = true
		default:
			diag.Status = StatusNotMatched
		}
		result.Diagnostics = append(result.Diagnostics, diag)
	}

	if permitMatched {
		result.Decision = DecisionAllow
	}
	return result
}

func evalPolicy(policy *Policy, req *Request) (bool, error) {
	if policy.When == nil {
		return true, nil
	}
	return EvalCondition(policy.When, req)
}
