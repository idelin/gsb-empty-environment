package proof

import (
	"fmt"
	"hash/fnv"
	"strconv"
)

// ID 是节点在图中的唯一标识。
type ID uint64

// Node 是共享图中的一个推导节点。
// 节点身份由 (规则名, 结论, 有序前提列表) 共同决定：
// 仅结论相同但前提不同的节点是不同的节点，不能被合并。
type Node struct {
	ID         ID
	Rule       string
	Conclusion string
	Premises   []ID // 有序，顺序参与身份
}

// key 是节点的结构身份。
type key struct {
	rule       string
	conclusion string
	premises   string // 编码后的有序前提列表
}

func makeKey(rule, conclusion string, premises []ID) key {
	p := ""
	for i, id := range premises {
		if i > 0 {
			p += ","
		}
		p += strconv.FormatUint(uint64(id), 36)
	}
	return key{rule: rule, conclusion: conclusion, premises: p}
}

// Builder 以哈希consing方式构建共享图：结构相同的节点自动合并。
// 前提必须先于结论节点加入，因此 Builder 构造出的图天然无环。
type Builder struct {
	nodes []Node
	cons  map[key]ID
}

func NewBuilder() *Builder {
	return &Builder{cons: make(map[key]ID)}
}

// Add 加入一个推导节点。若结构相同的节点已存在，返回已有 ID（共享）。
// 前提 ID 必须已经存在，否则报错。
func (b *Builder) Add(rule, conclusion string, premises ...ID) (ID, error) {
	for _, p := range premises {
		if p >= ID(len(b.nodes)) {
			return 0, fmt.Errorf("前提节点 %d 不存在", p)
		}
	}
	k := makeKey(rule, conclusion, premises)
	if id, ok := b.cons[k]; ok {
		return id, nil
	}
	id := ID(len(b.nodes))
	cp := make([]ID, len(premises))
	copy(cp, premises)
	b.nodes = append(b.nodes, Node{ID: id, Rule: rule, Conclusion: conclusion, Premises: cp})
	b.cons[k] = id
	return id, nil
}

// Build 冻结为只读图。节点按下标存储，下标序即拓扑序（前提先于结论）。
func (b *Builder) Build() *Graph {
	nodes := make([]Node, len(b.nodes))
	copy(nodes, b.nodes)
	return &Graph{nodes: nodes}
}

// Graph 是推导树的共享压缩图（DAG）。
type Graph struct {
	nodes []Node
}

// NewGraph 从原始节点直接构造图，不做任何一致性检查。
// 用于测试或反序列化场景；正确性由 Verify 保证（可检测环、悬挂前提等）。
func NewGraph(nodes []Node) *Graph {
	cp := make([]Node, len(nodes))
	for i, n := range nodes {
		n.ID = ID(i)
		p := make([]ID, len(n.Premises))
		copy(p, n.Premises)
		n.Premises = p
		cp[i] = n
	}
	return &Graph{nodes: cp}
}

func (g *Graph) Len() int { return len(g.nodes) }

// Node 返回指定节点，不存在时返回 false。
func (g *Graph) Node(id ID) (Node, bool) {
	if id >= ID(len(g.nodes)) {
		return Node{}, false
	}
	return g.nodes[id], true
}

// Nodes 返回全部节点（按下标序）。
func (g *Graph) Nodes() []Node {
	out := make([]Node, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Digest 返回节点内容的摘要，可用于外部身份比对。
func (n Node) Digest() uint64 {
	h := fnv.New64a()
	h.Write([]byte(n.Rule))
	h.Write([]byte{0})
	h.Write([]byte(n.Conclusion))
	for _, p := range n.Premises {
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatUint(uint64(p), 36)))
	}
	return h.Sum64()
}
