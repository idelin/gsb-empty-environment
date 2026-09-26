package session

import (
	"fmt"
	"sort"
	"strings"
)

// VersionVector 是因果版本向量：节点 ID -> 该节点已见的最大序号。
type VersionVector map[string]uint64

// Copy 返回深拷贝。
func (v VersionVector) Copy() VersionVector {
	out := make(VersionVector, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

// Merge 取两个版本向量的逐分量最大值。
func Merge(a, b VersionVector) VersionVector {
	out := a.Copy()
	for k, val := range b {
		if val > out[k] {
			out[k] = val
		}
	}
	return out
}

// Dominates 报告 v 是否因果覆盖 o（v 的每个分量都不小于 o）。
func (v VersionVector) Dominates(o VersionVector) bool {
	for k, val := range o {
		if v[k] < val {
			return false
		}
	}
	return true
}

func (v VersionVector) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, v[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
