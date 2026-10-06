package proof

import "fmt"

// Tree 是展开后的推导树节点。共享图中的共享子推导在树中被复制展开。
type Tree struct {
	Rule       string
	Conclusion string
	Children   []*Tree // 有序，与前提顺序一致
}

// Unroll 把共享图从 root 展开为推导树。
// 展开后丢失共享信息，可用于与独立构造的树做结构比较。
func Unroll(g *Graph, root ID) (*Tree, error) {
	node, ok := g.Node(root)
	if !ok {
		return nil, fmt.Errorf("根节点 %d 不存在", root)
	}
	t := &Tree{Rule: node.Rule, Conclusion: node.Conclusion}
	for _, p := range node.Premises {
		child, err := Unroll(g, p)
		if err != nil {
			return nil, err
		}
		t.Children = append(t.Children, child)
	}
	return t, nil
}

// Equal 比较两棵推导树是否结构相同（规则、结论、前提顺序全部一致）。
func (t *Tree) Equal(o *Tree) bool {
	if t == nil || o == nil {
		return t == o
	}
	if t.Rule != o.Rule || t.Conclusion != o.Conclusion || len(t.Children) != len(o.Children) {
		return false
	}
	for i := range t.Children {
		if !t.Children[i].Equal(o.Children[i]) {
			return false
		}
	}
	return true
}

// Size 返回树的节点总数（共享子推导被重复计数）。
func (t *Tree) Size() int {
	if t == nil {
		return 0
	}
	n := 1
	for _, c := range t.Children {
		n += c.Size()
	}
	return n
}
