package netsec

import (
	"errors"
	"fmt"
	"regexp"
)

// TenantLabel 是网关注入租户约束时使用的标签名。
const TenantLabel = "tenant"

var (
	// ErrTenantLabelForbidden 在查询通过聚合/运算修饰符或 label_replace、
	// label_join 操纵 tenant 标签时返回，整条查询必须被拒绝。
	ErrTenantLabelForbidden = errors.New("netsec: query manipulates the protected tenant label")
	// ErrEmptyTenant 在调用方传入空租户标识时返回。
	ErrEmptyTenant = errors.New("netsec: tenant identifier must not be empty")
)

// Rewrite 就地改写查询树，使树中每一个向量选择器（包括子查询与函数
// 参数内的）都带上 tenant="<tenant>" 精确匹配。选择器上原有的 tenant
// 匹配器会先被删除，攻击者无法通过 tenant=~".+" 之类的写法放行。
//
// 出现以下任一情况时返回 ErrTenantLabelForbidden，调用方应拒绝整条查询：
//   - label_replace 或 label_join 的目标标签参数是 tenant；
//   - 任一 by、without、on、ignoring、group_left 列表包含 tenant。
//
// 不包含任何向量选择器的查询（例如纯数字运算）原样返回。
func Rewrite(query Node, tenant string) (Node, error) {
	if tenant == "" {
		return query, ErrEmptyTenant
	}
	if query == nil {
		return query, nil
	}
	if err := rewriteNode(query, tenant); err != nil {
		return nil, err
	}
	return query, nil
}

func rewriteNode(node Node, tenant string) error {
	switch expr := node.(type) {
	case *VectorSelector:
		enforceTenant(expr, tenant)
		return nil

	case *RangeSelector:
		if expr.Vector != nil {
			enforceTenant(expr.Vector, tenant)
		}
		return nil

	case *Subquery:
		return rewriteChild(expr.Expr, tenant)

	case *NumberLiteral, *StringLiteral:
		return nil

	case *FunctionCall:
		if expr.Name == "label_replace" || expr.Name == "label_join" {
			// label_replace(v instant-vector, dst_label, ...)
			// label_join(v instant-vector, dst_label, sep, src_label...)
			// 两个函数的目标标签都是第 1 个参数。
			if targetLabel, ok := functionStringArg(expr, 1); ok && targetLabel == TenantLabel {
				return fmt.Errorf("%w: %s target label %q", ErrTenantLabelForbidden, expr.Name, TenantLabel)
			}
		}
		for _, arg := range expr.Args {
			if err := rewriteChild(arg, tenant); err != nil {
				return err
			}
		}
		return nil

	case *Aggregation:
		if containsLabel(expr.Grouping, TenantLabel) {
			clause := "by"
			if expr.Without {
				clause = "without"
			}
			return fmt.Errorf("%w: %s grouping lists protected label %q", ErrTenantLabelForbidden, clause, TenantLabel)
		}
		return rewriteChild(expr.Expr, tenant)

	case *BinaryExpr:
		if containsLabel(expr.On, TenantLabel) {
			return fmt.Errorf("%w: on clause lists protected label %q", ErrTenantLabelForbidden, TenantLabel)
		}
		if containsLabel(expr.Ignoring, TenantLabel) {
			return fmt.Errorf("%w: ignoring clause lists protected label %q", ErrTenantLabelForbidden, TenantLabel)
		}
		if containsLabel(expr.GroupLeft, TenantLabel) {
			return fmt.Errorf("%w: group_left clause lists protected label %q", ErrTenantLabelForbidden, TenantLabel)
		}
		if err := rewriteChild(expr.LHS, tenant); err != nil {
			return err
		}
		return rewriteChild(expr.RHS, tenant)

	default:
		return fmt.Errorf("netsec: unsupported query node type %T", node)
	}
}

func rewriteChild(node Node, tenant string) error {
	if node == nil {
		return nil
	}
	return rewriteNode(node, tenant)
}

// enforceTenant 删除选择器上原有的 tenant 匹配器，并追加精确匹配。
func enforceTenant(selector *VectorSelector, tenant string) {
	filtered := selector.Matchers[:0]
	for _, matcher := range selector.Matchers {
		if matcher.Label != TenantLabel {
			filtered = append(filtered, matcher)
		}
	}
	selector.Matchers = append(filtered, Matcher{
		Label: TenantLabel,
		Op:    OpEqual,
		Value: tenant,
	})
}

// functionStringArg 取出函数调用第 index 个字符串字面量参数。
func functionStringArg(call *FunctionCall, index int) (string, bool) {
	if index < 0 || index >= len(call.Args) {
		return "", false
	}
	literal, ok := call.Args[index].(*StringLiteral)
	if !ok {
		return "", false
	}
	return literal.Value, true
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

// MatchesEmpty 报告该匹配器是否会匹配没有该标签（空串标签值）的序列。
// 网关用它识别指标名缺省、且所有匹配器都能匹配空串的选择器；
// 这类选择器同样必须被强制追加 tenant 精确匹配。
func (m Matcher) MatchesEmpty() bool {
	switch m.Op {
	case OpEqual:
		return m.Value == ""
	case OpNotEqual:
		return m.Value != ""
	case OpRegex:
		re, err := regexp.Compile(m.Value)
		if err != nil {
			return false
		}
		return re.MatchString("")
	case OpNotRegex:
		re, err := regexp.Compile(m.Value)
		if err != nil {
			return true
		}
		return !re.MatchString("")
	default:
		return false
	}
}

// MatchesEmpty 报告选择器在缺省指标名时，现有匹配器是否全部能匹配空串。
func (selector *VectorSelector) MatchesEmpty() bool {
	if selector.Name != "" {
		return false
	}
	for _, matcher := range selector.Matchers {
		if !matcher.MatchesEmpty() {
			return false
		}
	}
	return true
}

// TenantMatcher 返回选择器上的 tenant 精确匹配器；不存在时 ok 为 false。
func (selector *VectorSelector) TenantMatcher() (matcher Matcher, ok bool) {
	for _, matcher := range selector.Matchers {
		if matcher.Label == TenantLabel {
			return matcher, true
		}
	}
	return Matcher{}, false
}
