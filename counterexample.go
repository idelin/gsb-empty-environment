package netsec

import (
	"fmt"
	"reflect"
)

// NodeKind 分类来源图中的对象节点。它只描述节点在程序结构中的角色，
// 不参与任何基于名称或文本相似度的自动匹配。
type NodeKind string

const (
	NodeFunction  NodeKind = "function"
	NodeObject    NodeKind = "object"
	NodeBlock     NodeKind = "block"
	NodeAssertion NodeKind = "assertion"
	NodeOther     NodeKind = "other"
)

// Node 是对象来源图中的一个稳定节点。ID 在单个图内唯一；同一节点在不同
// 版本里可以拥有不同的 ID，关联时只通过调用方显式提供的迁移映射衔接。
type Node struct {
	ID   string
	Kind NodeKind
	// Name 仅用于人类阅读和错误诊断，关联逻辑从不比较或匹配 Name。
	Name string
}

// Edge 是来源图中 From 指向 To 的依赖边。Relation 描述依赖关系（例如
// "produced-by"、"calls"）。空 Relation 作为通配符，可匹配任意关系。
type Edge struct {
	From     string
	To       string
	Relation string
}

// Graph 是反例触发点的对象来源图。图必须在内部闭合：每条边的端点都必须
// 指向图内存在的节点。
type Graph struct {
	Nodes []Node
	Edges []Edge
}

// Validate 检查图的内部一致性：节点 ID 非空且唯一，边端点均存在。
func (g Graph) Validate() error {
	seen := make(map[string]struct{}, len(g.Nodes))
	for _, node := range g.Nodes {
		if node.ID == "" {
			return fmt.Errorf("graph contains node with empty id")
		}
		if _, dup := seen[node.ID]; dup {
			return fmt.Errorf("graph contains duplicate node id %q", node.ID)
		}
		seen[node.ID] = struct{}{}
	}
	for _, edge := range g.Edges {
		if _, ok := seen[edge.From]; !ok {
			return fmt.Errorf("edge references unknown source node %q", edge.From)
		}
		if _, ok := seen[edge.To]; !ok {
			return fmt.Errorf("edge references unknown target node %q", edge.To)
		}
	}
	return nil
}

func (g Graph) hasNode(id string) bool {
	for _, node := range g.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

// Input 是反例的结构化输入。输入相等采用递归结构相等：Map 不受字段顺序
// 影响，Sequence 保留顺序，叶子值按 Go 的深度相等比较。
// 这里刻意不保留原始字符串，避免关联退化为文本相似度匹配。
type Input struct {
	// Kind 区分输入承载形式，例如 "http-request"、"cli-args"。
	Kind   string
	Fields map[string]Input
	Seq    []Input
	Value  any
}

// Equal 报告两个输入在结构上是否相等。
func (in Input) Equal(other Input) bool {
	if in.Kind != other.Kind {
		return false
	}
	if !reflect.DeepEqual(in.Value, other.Value) {
		return false
	}
	if len(in.Fields) != len(other.Fields) {
		return false
	}
	for key, value := range in.Fields {
		otherValue, ok := other.Fields[key]
		if !ok || !value.Equal(otherValue) {
			return false
		}
	}
	if len(in.Seq) != len(other.Seq) {
		return false
	}
	for i := range in.Seq {
		if !in.Seq[i].Equal(other.Seq[i]) {
			return false
		}
	}
	return true
}

// Counterexample 是一个安全反例在某个版本中的快照。
type Counterexample struct {
	// AssertionID 是反例绑定的稳定断言标识，跨版本不变，是关联的主键。
	AssertionID string
	// Version 是反例所属的被测版本标签。
	Version string
	// Origin 是对象来源图，记录触发断言的对象及其依赖链。
	Origin Graph
	// Input 是复现反例所需的结构化输入。
	Input Input
}

// Validate 检查反例自身的完整性。
func (c Counterexample) Validate() error {
	if c.AssertionID == "" {
		return fmt.Errorf("counterexample is missing assertion id")
	}
	if c.Version == "" {
		return fmt.Errorf("counterexample %q is missing version", c.AssertionID)
	}
	if err := c.Origin.Validate(); err != nil {
		return fmt.Errorf("counterexample %q origin graph invalid: %w", c.AssertionID, err)
	}
	return nil
}
