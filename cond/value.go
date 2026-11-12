package cond

import (
	"sort"
	"strings"
)

// Value 是请求中某个标签的值，区分缺失、空集合与非空集合。
// 缺失不再是空字符串，从源头避免误放行。
type Value struct {
	present bool
	set     map[string]struct{}
}

// Missing 返回缺失值。
func Missing() Value { return Value{} }

// Set 由若干元素构造非缺失值；零个元素即空集合。
func Set(elems ...string) Value {
	v := Value{present: true, set: make(map[string]struct{}, len(elems))}
	for _, e := range elems {
		v.set[e] = struct{}{}
	}
	return v
}

// Present 报告标签是否出现（空集合也算出现）。
func (v Value) Present() bool { return v.present }

// Contains 报告元素是否在非空集合中。
func (v Value) Contains(elem string) bool {
	_, ok := v.set[elem]
	return ok
}

// All 报告非空集合的每个元素是否都满足 pred。
// 空集合返回真（空真），缺失返回未知。
func (v Value) All(pred func(string) bool) Tri {
	if !v.present {
		return Unknown
	}
	for e := range v.set {
		if !pred(e) {
			return False
		}
	}
	return True
}

// Any 报告非空集合是否存在元素满足 pred。
// 空集合返回假，缺失返回未知。
func (v Value) Any(pred func(string) bool) Tri {
	if !v.present {
		return Unknown
	}
	for e := range v.set {
		if pred(e) {
			return True
		}
	}
	return False
}

func (v Value) String() string {
	if !v.present {
		return "<missing>"
	}
	if len(v.set) == 0 {
		return "{}"
	}
	elems := make([]string, 0, len(v.set))
	for e := range v.set {
		elems = append(elems, e)
	}
	sort.Strings(elems)
	return "{" + strings.Join(elems, ",") + "}"
}

// Labels 是请求标签表：标签名到值的映射，未出现的标签即缺失。
type Labels map[string]Value

// Get 取出标签值，未配置时返回缺失。
func (l Labels) Get(name string) Value {
	if v, ok := l[name]; ok {
		return v
	}
	return Missing()
}
