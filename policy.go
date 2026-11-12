package netsec

// AllowRule 表示一条 allow 规则：允许 Source 对 Target 的 Class 拥有 Perms 权限。
// Source 与 Target 可以是具体类型，也可以是 typeattribute 属性名。
type AllowRule struct {
	Source string
	Target string
	Class  string
	Perms  []string
}

// TypeTransitionRule 表示一条 type_transition 规则：Source 域执行 Target
// 类型的文件后，新进程默认进入 Default 类型。
type TypeTransitionRule struct {
	Source  string
	Target  string
	Class   string
	Default string
}

// NeverallowRule 表示一条 neverallow 规则，字段语义与 AllowRule 相同，
// 但表示禁止：任何展开后与之匹配的 allow 都构成违规。
type NeverallowRule struct {
	Source string
	Target string
	Class  string
	Perms  []string
}

// Policy 是供发布前评审的策略快照。Dontaudit 仅抑制审计日志，
// 不参与放行判定，分析时会被忽略。
type Policy struct {
	Allows      []AllowRule
	Transitions []TypeTransitionRule
	Neverallows []NeverallowRule
	Dontaudits  []AllowRule
	Attributes  map[string][]string
}

// expand 将类型或属性名展开为具体类型集合，属性可嵌套引用属性。
func (p *Policy) expand(name string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n string)
	walk = func(n string) {
		members, ok := p.Attributes[n]
		if !ok {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
			return
		}
		for _, m := range members {
			walk(m)
		}
	}
	walk(name)
	return out
}
