package policydiff

import (
	"fmt"
	"sort"
)

// Domain 描述一个属性的有限取值域。
type Domain struct {
	Type   string   // "int" | "label" | "bool"
	Min    int      // int 域下界（含）
	Max    int      // int 域上界（含）
	Values []string // label 域的候选标签
}

// Enumerate 枚举域中的全部取值。
func (d Domain) Enumerate() ([]Value, error) {
	switch d.Type {
	case "int":
		if d.Min > d.Max {
			return nil, fmt.Errorf("整数域下界 %d 大于上界 %d", d.Min, d.Max)
		}
		vs := make([]Value, 0, d.Max-d.Min+1)
		for i := d.Min; i <= d.Max; i++ {
			vs = append(vs, Int(i))
		}
		return vs, nil
	case "label":
		if len(d.Values) == 0 {
			return nil, fmt.Errorf("标签域不能为空")
		}
		vs := make([]Value, 0, len(d.Values))
		for _, s := range d.Values {
			vs = append(vs, Label(s))
		}
		return vs, nil
	case "bool":
		return []Value{Bool(false), Bool(true)}, nil
	default:
		return nil, fmt.Errorf("未知域类型 %q", d.Type)
	}
}

// Schema 是属性名到有限域的映射。
type Schema map[string]Domain

// Result 是差异检查的结果。
type Result struct {
	// Expands 为 true 表示新规则扩大了授权。
	Expands bool
	// Counterexample 是最小的主体/资源属性组合：
	// 新规则放行而旧规则拒绝。未列出的属性即缺失（Unknown → 拒绝）。
	Counterexample Env
	// NewTree / OldTree 分别是新、旧规则在反例下的解释树。
	NewTree *ExplainNode
	OldTree *ExplainNode
}

// CheckExpansion 判断 newRule 是否相对 oldRule 扩大了授权。
// 若扩大，返回属性个数最少的反例（同数量时按属性名字典序、
// 取值域顺序取最先者）。域均为有限域，枚举配合三值单调性剪枝：
// 部分赋值下若新规则已为 False 或旧规则已为 True，则其任何
// 超集都不可能成为反例，可直接剪枝。
func CheckExpansion(schema Schema, oldRule, newRule *Expr) (*Result, error) {
	attrs, values, err := prepare(schema, oldRule, newRule)
	if err != nil {
		return nil, err
	}
	// 迭代加深：按已赋值属性个数 k = 0..n 逐层搜索，保证最小性。
	for k := 0; k <= len(attrs); k++ {
		if env, ok := searchK(attrs, values, k, oldRule, newRule); ok {
			return &Result{
				Expands:        true,
				Counterexample: env,
				NewTree:        Explain(newRule, env),
				OldTree:        Explain(oldRule, env),
			}, nil
		}
	}
	return &Result{Expands: false}, nil
}

// prepare 汇总两条规则引用的属性并解析各自的有限域。
func prepare(schema Schema, rules ...*Expr) ([]string, [][]Value, error) {
	seen := map[string]bool{}
	var attrs []string
	for _, r := range rules {
		for _, a := range r.Attrs() {
			if !seen[a] {
				seen[a] = true
				attrs = append(attrs, a)
			}
		}
	}
	sort.Strings(attrs)
	values := make([][]Value, len(attrs))
	for i, a := range attrs {
		d, ok := schema[a]
		if !ok {
			return nil, nil, fmt.Errorf("属性 %s 缺少域定义", a)
		}
		vs, err := d.Enumerate()
		if err != nil {
			return nil, nil, fmt.Errorf("属性 %s：%w", a, err)
		}
		values[i] = vs
	}
	return attrs, values, nil
}

// searchK 枚举恰好包含 k 个已赋值属性的环境，返回首个反例。
func searchK(attrs []string, values [][]Value, k int, oldRule, newRule *Expr) (Env, bool) {
	env := Env{}
	var found Env

	var dfs func(pos, left int) bool
	dfs = func(pos, left int) bool {
		if left == 0 {
			// 恰好 k 个属性已赋值：判定反例条件。
			if Eval(newRule, env) == True && Eval(oldRule, env) != True {
				found = copyEnv(env)
				return true
			}
			return false
		}
		if len(attrs)-pos < left {
			return false
		}
		// 选择不赋值 attrs[pos]（保持缺失）。
		if dfs(pos+1, left) {
			return true
		}
		// 选择为 attrs[pos] 赋每个可能值。
		for _, v := range values[pos] {
			env[attrs[pos]] = v
			// 三值单调性剪枝：新规则已确定为 False（无法再变 True），
			// 或旧规则已确定为 True（无法再被拒绝），则停止扩展。
			if Eval(newRule, env) != False && Eval(oldRule, env) != True {
				if dfs(pos+1, left-1) {
					return true
				}
			}
		}
		delete(env, attrs[pos])
		return false
	}

	if dfs(0, k) {
		return found, true
	}
	return nil, false
}

func copyEnv(env Env) Env {
	out := make(Env, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// SortedPairs 将环境按键名升序展开为 "属性 = 值" 列表，便于展示。
func SortedPairs(env Env) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s = %s", k, env[k]))
	}
	return out
}
