package memberlist

import (
	"fmt"
	"strconv"
	"strings"
)

// Condition 是一条属性筛选条件，如 zone=us-east 或 weight>=50。
type Condition struct {
	Key   string // zone / role / weight / id / addr / label.<name>
	Op    string // =  !=  >=  <=  >  <
	Value string
}

// ParseSelector 解析逗号分隔的 AND 选择器，如 "zone=us-east,role=worker"。
func ParseSelector(s string) ([]Condition, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var conds []Condition
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var cond Condition
		found := false
		for _, op := range []string{"!=", ">=", "<=", "=", ">", "<"} {
			if idx := strings.Index(part, op); idx > 0 {
				cond = Condition{
					Key:   strings.TrimSpace(part[:idx]),
					Op:    op,
					Value: strings.TrimSpace(part[idx+len(op):]),
				}
				found = true
				break
			}
		}
		if !found || cond.Key == "" || cond.Value == "" {
			return nil, fmt.Errorf("invalid selector condition: %q", part)
		}
		conds = append(conds, cond)
	}
	return conds, nil
}

// Match 判断节点是否满足全部条件（AND 语义）。
func Match(n *Node, conds []Condition) bool {
	for _, c := range conds {
		if !matchOne(n, c) {
			return false
		}
	}
	return true
}

func matchOne(n *Node, c Condition) bool {
	attr, numeric, ok := nodeAttr(n, c.Key)
	if !ok {
		return false
	}
	switch c.Op {
	case "=":
		return attr == c.Value
	case "!=":
		return attr != c.Value
	}
	// 数值比较
	if !numeric {
		return false
	}
	lhs, err := strconv.ParseFloat(attr, 64)
	if err != nil {
		return false
	}
	rhs, err := strconv.ParseFloat(c.Value, 64)
	if err != nil {
		return false
	}
	switch c.Op {
	case ">":
		return lhs > rhs
	case "<":
		return lhs < rhs
	case ">=":
		return lhs >= rhs
	case "<=":
		return lhs <= rhs
	}
	return false
}

// nodeAttr 取出节点属性值，并报告该属性是否为数值类型。
func nodeAttr(n *Node, key string) (val string, numeric bool, ok bool) {
	switch key {
	case "id":
		return n.ID, false, true
	case "addr":
		return n.Addr, false, true
	case "zone":
		return n.Meta.Zone, false, true
	case "role":
		return n.Meta.Role, false, true
	case "weight":
		return strconv.Itoa(n.Meta.Weight), true, true
	}
	if strings.HasPrefix(key, "label.") {
		v, exists := n.Labels[strings.TrimPrefix(key, "label.")]
		return v, false, exists
	}
	return "", false, false
}
