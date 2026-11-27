package scim

import (
	"encoding/json"
	"strings"
)

// Resource 是 JSON 风格的 SCIM 资源表示（map 形式便于宿主直接序列化）。
type Resource map[string]any

// CloneResource 做深拷贝，保证策略校验 / PATCH 事务期间的快照隔离。
func CloneResource(r Resource) Resource {
	if r == nil {
		return nil
	}
	out, _ := cloneValue(r).(Resource)
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case Resource:
		cp := make(Resource, len(t))
		for k, val := range t {
			cp[k] = cloneValue(val)
		}
		return cp
	case map[string]any:
		cp := make(map[string]any, len(t))
		for k, val := range t {
			cp[k] = cloneValue(val)
		}
		return cp
	case []any:
		cp := make([]any, len(t))
		for i, val := range t {
			cp[i] = cloneValue(val)
		}
		return cp
	case []map[string]any:
		cp := make([]any, len(t))
		for i, val := range t {
			cp[i] = cloneValue(val)
		}
		return cp
	default:
		return v
	}
}

// Match 对单个资源求值 AST。属性名按 SCIM 惯例大小写不敏感匹配。
func Match(expr Expr, r Resource) (bool, error) {
	switch e := expr.(type) {
	case *AttrExpr:
		return matchAttr(e, r), nil
	case *NotExpr:
		v, err := Match(e.Inner, r)
		if err != nil {
			return false, err
		}
		return !v, nil
	case *LogicalExpr:
		switch e.Op {
		case LogicalAnd:
			l, err := Match(e.Left, r)
			if err != nil || !l {
				return false, err
			}
			return Match(e.Right, r)
		case LogicalOr:
			l, err := Match(e.Left, r)
			if err != nil {
				return false, err
			}
			if l {
				return true, nil
			}
			return Match(e.Right, r)
		}
	case *ValuePathExpr:
		return matchValuePath(e, r), nil
	}
	return false, nil
}

func matchValuePath(e *ValuePathExpr, r Resource) bool {
	elements := resolvePath(r, e.Path)
	for _, el := range elements {
		m, ok := asMap(el)
		if !ok {
			continue
		}
		if ok, _ := Match(e.Filter, m); ok {
			return true
		}
	}
	return false
}

func matchAttr(e *AttrExpr, r Resource) bool {
	values := resolvePath(r, e.Path)

	if e.Op == OpPr {
		for _, v := range values {
			if v != nil {
				if s, ok := v.(string); ok && s == "" {
					continue
				}
				return true
			}
		}
		return false
	}

	// ne 是 eq 的逻辑取反：存在相等值即 false，否则 true（含属性缺失）。
	if e.Op == OpNe {
		for _, actual := range values {
			if valuesEqual(actual, e.Value.Value) {
				return false
			}
		}
		return true
	}

	for _, actual := range values {
		if compareValue(e.Op, actual, e.Value) {
			return true
		}
	}
	return false
}

// resolvePath 返回属性路径命中的全部标量/复杂值，多值切片会被展开。
func resolvePath(r Resource, path AttrPath) []any {
	if r == nil {
		return nil
	}
	nodes := []any{r}

	// 完整 URN 路径先定位 schema 扩展对象，例如
	// urn:ietf:params:scim:schemas:extension:enterprise:2.0:User.department
	if path.Schema != "" {
		if v, ok := lookupKeyCI(r, path.Schema); ok {
			nodes = []any{v}
		} else if v, ok := lookupKeyCI(r, path.String()); ok {
			// 兼容把完整 URN:attr 作为扁平 JSON 键的资源。
			return []any{v}
		}
	}

	for _, part := range path.Parts {
		var next []any
		for _, node := range nodes {
			m, ok := asMap(node)
			if !ok {
				continue
			}
			v, ok := lookupKeyCI(m, part)
			if !ok {
				continue
			}
			if slice, ok := asSlice(v); ok {
				next = append(next, slice...)
			} else {
				next = append(next, v)
			}
		}
		nodes = next
	}
	return nodes
}

func asMap(v any) (Resource, bool) {
	switch m := v.(type) {
	case Resource:
		return m, true
	case map[string]any:
		return Resource(m), true
	default:
		return nil, false
	}
}

func asSlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []map[string]any:
		out := make([]any, len(s))
		for i, m := range s {
			out[i] = m
		}
		return out, true
	default:
		return nil, false
	}
}

func lookupKeyCI(m Resource, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func compareValue(op Operator, actual any, lit *Literal) bool {
	if lit == nil {
		return false
	}
	switch op {
	case OpEq:
		return valuesEqual(actual, lit.Value)
	case OpCo:
		hay, ok1 := actual.(string)
		needle, ok2 := lit.Value.(string)
		return ok1 && ok2 && strings.Contains(hay, needle)
	case OpSw:
		hay, ok1 := actual.(string)
		needle, ok2 := lit.Value.(string)
		return ok1 && ok2 && strings.HasPrefix(hay, needle)
	case OpEw:
		hay, ok1 := actual.(string)
		needle, ok2 := lit.Value.(string)
		return ok1 && ok2 && strings.HasSuffix(hay, needle)
	case OpGt, OpGe, OpLt, OpLe:
		return orderCompare(op, actual, lit.Value)
	default:
		return false
	}
}

func valuesEqual(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		return ab == bb
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func orderCompare(op Operator, actual, expected any) bool {
	af, aNum := toFloat(actual)
	bf, bNum := toFloat(expected)
	var cmp int
	if aNum && bNum {
		switch {
		case af < bf:
			cmp = -1
		case af > bf:
			cmp = 1
		default:
			cmp = 0
		}
	} else {
		as, aok := actual.(string)
		bs, bok := expected.(string)
		if !aok || !bok {
			return false
		}
		cmp = strings.Compare(as, bs)
	}
	switch op {
	case OpGt:
		return cmp > 0
	case OpGe:
		return cmp >= 0
	case OpLt:
		return cmp < 0
	case OpLe:
		return cmp <= 0
	default:
		return false
	}
}

// TenantPredicate 构造隐式租户隔离谓词，例如 tenantId eq "tenant-a"。
func TenantPredicate(tenantAttribute, tenantID string) *AttrExpr {
	path, _ := ParseAttrPath(tenantAttribute)
	return &AttrExpr{Path: path, Op: OpEq, Value: StringLiteral(tenantID)}
}

// BindTenantScope 把调用方租户谓词以 AND 绑定到用户 filter 外层。
// 用户表达式会作为左操作数整体存在，因此其内部 OR 不可能绕过租户条件：
// 最终形态恒为 (userFilter) and tenantAttr eq "callerTenant"。
func BindTenantScope(expr Expr, tenantAttribute, tenantID string) Expr {
	pred := TenantPredicate(tenantAttribute, tenantID)
	if expr == nil {
		return pred
	}
	return &LogicalExpr{Op: LogicalAnd, Left: expr, Right: pred}
}
