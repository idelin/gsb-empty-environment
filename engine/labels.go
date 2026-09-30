package engine

import (
	"sort"
	"strings"
)

// LabelSet 标识一个输入或输出系列。冲突判定基于完整标签集合的同一性，
// 而不是某次重写产生的文本片段。
type LabelSet map[string]string

// Key 返回规范化标识（排序后的 k=v 序列），用于输出标签同一性比较。
func (ls LabelSet) Key() string {
	keys := make([]string, 0, len(ls))
	for k := range ls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(ls[k])
		b.WriteByte(0)
	}
	return b.String()
}

// Clone 返回副本，避免规则改写污染输入。
func (ls LabelSet) Clone() LabelSet {
	out := make(LabelSet, len(ls))
	for k, v := range ls {
		out[k] = v
	}
	return out
}
