// Package entitymerge 提供面向数据管线的增量记录合并能力：
// 多来源记录按分块键缩小候选范围，由可配置规则判定匹配，
// 匹配置信边经并查集传递合并为实体；撤销某条匹配证据时
// 只重算受影响的连通分量。实体 ID 取分量内最早到达的记录 ID，
// 拆分时各新分量各自取最早记录，保证稳定分配。
package entitymerge

import "strings"

// Record 是一条来自某个数据源的记录。Seq 由 Engine 在 AddRecord 时
// 按到达顺序赋值，调用方无需设置。
type Record struct {
	ID     string
	Source string
	Fields map[string]string

	seq int
}

// Rule 描述一类匹配证据：BlockFields 决定分块键（仅同块记录会被比较），
// MatchFields 决定匹配条件（归一化后全部相等才匹配）。Matcher 非 nil 时
// 优先使用自定义判定，忽略 MatchFields。
type Rule struct {
	Name        string
	BlockFields []string
	MatchFields []string
	Normalize   func(string) string
	Matcher     func(a, b Record) bool
}

// DefaultNormalize 转小写并折叠连续空白。
func DefaultNormalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func (r Rule) normalize(s string) string {
	if r.Normalize != nil {
		return r.Normalize(s)
	}
	return DefaultNormalize(s)
}

// blockKey 返回记录在该规则下的分块键；任一分块字段为空时返回 false，
// 该记录在此规则下不参与比较。
func (r Rule) blockKey(rec Record) (string, bool) {
	parts := make([]string, len(r.BlockFields))
	for i, f := range r.BlockFields {
		v := r.normalize(rec.Fields[f])
		if v == "" {
			return "", false
		}
		parts[i] = v
	}
	return strings.Join(parts, "\x1f"), true
}

func (r Rule) matches(a, b Record) bool {
	if r.Matcher != nil {
		return r.Matcher(a, b)
	}
	if len(r.MatchFields) == 0 {
		return false
	}
	for _, f := range r.MatchFields {
		av, bv := r.normalize(a.Fields[f]), r.normalize(b.Fields[f])
		if av == "" || bv == "" || av != bv {
			return false
		}
	}
	return true
}

// Evidence 是一条匹配证据：某条规则判定两条记录匹配。
type Evidence struct {
	Rule string
	A, B string
}
