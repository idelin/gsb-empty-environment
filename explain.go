package permdiff

import (
	"fmt"
	"strings"
)

// Explain 是求值解释树的节点：记录每个子表达式的三值结果，
// 用于说明授权为何通过、拒绝或处于未知。
type Explain struct {
	Node     string     `json:"node"`
	Value    Tri        `json:"value"`
	Children []*Explain `json:"children,omitempty"`
}

// String 以缩进树形式渲染解释。
func (e *Explain) String() string {
	var sb strings.Builder
	e.write(&sb, 0)
	return sb.String()
}

func (e *Explain) write(sb *strings.Builder, depth int) {
	fmt.Fprintf(sb, "%s%s => %s\n", strings.Repeat("  ", depth), e.Node, e.Value)
	for _, c := range e.Children {
		c.write(sb, depth+1)
	}
}
