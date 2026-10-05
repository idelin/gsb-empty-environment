package plan

import (
	"fmt"
	"strings"
)

type comparator struct {
	op string
	v  Version
}

// Constraint 是一组比较子句的合取；空子句集表示匹配任意版本。
type Constraint struct {
	raw   string
	comps []comparator
}

// ParseConstraint 支持："*"、精确版本、"=1.2.3"、">=1.2 <2.0"（逗号或空格分隔）、
// "^1.2.3"（兼容到下一主版本）、"~1.2.3"（兼容到次版本）。
func ParseConstraint(s string) (Constraint, error) {
	c := Constraint{raw: strings.TrimSpace(s)}
	if c.raw == "" || c.raw == "*" {
		return c, nil
	}
	parts := strings.FieldsFunc(c.raw, func(r rune) bool {
		return r == ',' || r == ' '
	})
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "^"):
			base, err := ParseVersion(p[1:])
			if err != nil {
				return c, err
			}
			upper := base
			if upper.Parts[0] > 0 {
				upper = Version{Parts: [3]int{upper.Parts[0] + 1, 0, 0}}
			} else if upper.Parts[1] > 0 {
				upper = Version{Parts: [3]int{0, upper.Parts[1] + 1, 0}}
			} else {
				upper = Version{Parts: [3]int{0, 0, upper.Parts[2] + 1}}
			}
			c.comps = append(c.comps, comparator{">=", base}, comparator{"<", upper})
		case strings.HasPrefix(p, "~"):
			base, err := ParseVersion(p[1:])
			if err != nil {
				return c, err
			}
			upper := Version{Parts: [3]int{base.Parts[0], base.Parts[1] + 1, 0}}
			c.comps = append(c.comps, comparator{">=", base}, comparator{"<", upper})
		default:
			op := "="
			rest := p
			for _, cand := range []string{">=", "<=", "!=", "=", ">", "<"} {
				if strings.HasPrefix(p, cand) {
					op = cand
					rest = p[len(cand):]
					break
				}
			}
			v, err := ParseVersion(rest)
			if err != nil {
				return c, fmt.Errorf("约束 %q 无法解析: %v", s, err)
			}
			c.comps = append(c.comps, comparator{op, v})
		}
	}
	return c, nil
}

// Match 判断版本是否满足约束。
func (c Constraint) Match(v Version) bool {
	for _, cmp := range c.comps {
		r := v.Compare(cmp.v)
		switch cmp.op {
		case "=":
			if r != 0 {
				return false
			}
		case "!=":
			if r == 0 {
				return false
			}
		case ">":
			if r <= 0 {
				return false
			}
		case ">=":
			if r < 0 {
				return false
			}
		case "<":
			if r >= 0 {
				return false
			}
		case "<=":
			if r > 0 {
				return false
			}
		}
	}
	return true
}

func (c Constraint) String() string {
	if c.raw == "" {
		return "*"
	}
	return c.raw
}
