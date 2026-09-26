package sessionkv

import (
	"fmt"
	"sort"
	"strings"
)

// Vector 是跨键因果版本向量：节点ID -> 该节点已应用的最大写序号。
// 会话令牌携带它，读请求必须达到（或等待达到）它所刻画的因果前沿。
type Vector map[string]uint64

// Copy 返回深拷贝。
func (v Vector) Copy() Vector {
	c := make(Vector, len(v))
	for k, val := range v {
		c[k] = val
	}
	return c
}

// Merge 按分量取最大，把 other 合并进 v。
func (v Vector) Merge(other Vector) Vector {
	for k, val := range other {
		if val > v[k] {
			v[k] = val
		}
	}
	return v
}

// Covers 报告 v 是否逐分量 >= other，即 v 因果上包含 other 标记的所有写。
func (v Vector) Covers(other Vector) bool {
	for k, val := range other {
		if v[k] < val {
			return false
		}
	}
	return true
}

// Equal 报告两个向量是否逐分量相等。
func (v Vector) Equal(other Vector) bool {
	return v.Covers(other) && other.Covers(v)
}

func (v Vector) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%s:%d", k, v[k])
	}
	sb.WriteString("}")
	return sb.String()
}
