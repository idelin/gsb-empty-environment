package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Tree 描述单个准标识属性的泛化层次。
// 每条边 {子节点, 父节点} 表示具体值向更一般值的一步泛化；
// 唯一没有父节点的节点即泛化树根，叶子是宿主发布数据中的具体取值。
// 树允许不平衡：泛化层级以“向上走几步”定义，因此对任意节点都无歧义。
type Tree struct {
	name   string
	parent map[string]string
	child  map[string][]string
	root   string
	height int
}

// NewTree 根据 child->parent 边构建泛化树。
// 根节点是唯一从未作为子节点出现的节点；构建时校验空节点、重复父节点、
// 多根、环与不可达节点。节点值不允许包含 NUL 字符（组键的内部分隔符）。
func NewTree(name string, edges [][2]string) (*Tree, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("netsec: 泛化树名称不能为空")
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("netsec: 树 %q 至少需要一条边", name)
	}

	t := &Tree{
		name:   name,
		parent: make(map[string]string),
		child:  make(map[string][]string),
	}
	nodes := make(map[string]struct{})
	for _, edge := range edges {
		childNode, parentNode := edge[0], edge[1]
		if childNode == "" || parentNode == "" {
			return nil, fmt.Errorf("netsec: 树 %q 存在空节点", name)
		}
		if strings.ContainsRune(childNode, 0) || strings.ContainsRune(parentNode, 0) {
			return nil, fmt.Errorf("netsec: 树 %q 节点不能包含 NUL 字符", name)
		}
		if existing, ok := t.parent[childNode]; ok {
			if existing == parentNode {
				return nil, fmt.Errorf("netsec: 树 %q 存在重复边 %q->%q", name, childNode, parentNode)
			}
			return nil, fmt.Errorf("netsec: 节点 %q 同时挂在 %q 与 %q 下", childNode, existing, parentNode)
		}
		t.parent[childNode] = parentNode
		t.child[parentNode] = append(t.child[parentNode], childNode)
		nodes[childNode] = struct{}{}
		nodes[parentNode] = struct{}{}
	}

	var roots []string
	for node := range nodes {
		if _, ok := t.parent[node]; !ok {
			roots = append(roots, node)
		}
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("netsec: 树 %q 必须恰有一个根节点，实际有 %d 个", name, len(roots))
	}
	t.root = roots[0]

	seen := make(map[string]bool)
	var walk func(node string, depth int)
	walk = func(node string, depth int) {
		seen[node] = true
		if depth > t.height {
			t.height = depth
		}
		for _, childNode := range t.child[node] {
			walk(childNode, depth+1)
		}
	}
	walk(t.root, 0)
	if len(seen) != len(nodes) {
		return nil, fmt.Errorf("netsec: 树 %q 存在环或与根不连通的节点", name)
	}
	return t, nil
}

// Name 返回准标识属性名。
func (t *Tree) Name() string { return t.name }

// Root 返回泛化树根节点。
func (t *Tree) Root() string { return t.root }

// Height 返回任意叶子到根的最大距离（可用泛化步数上界）。
func (t *Tree) Height() int { return t.height }

// Contains 报告 value 是否是树中的合法节点。
func (t *Tree) Contains(value string) bool {
	if value == t.root {
		return true
	}
	_, ok := t.parent[value]
	return ok
}

// StepsToRoot 返回 value 泛化到根所需的步数。
func (t *Tree) StepsToRoot(value string) (int, error) {
	if !t.Contains(value) {
		return 0, fmt.Errorf("netsec: 值 %q 不在树 %q 中", value, t.name)
	}
	steps := 0
	current := value
	for current != t.root {
		current = t.parent[current]
		steps++
	}
	return steps, nil
}

// Generalize 将 value 向上泛化 steps 步：steps=0 返回原值，
// steps 超过该值到根的距离时返回错误。
func (t *Tree) Generalize(value string, steps int) (string, error) {
	if steps < 0 {
		return "", fmt.Errorf("netsec: 泛化步数不能为负数，收到 %d", steps)
	}
	if !t.Contains(value) {
		return "", fmt.Errorf("netsec: 值 %q 不在树 %q 中", value, t.name)
	}
	current := value
	for i := 0; i < steps; i++ {
		if current == t.root {
			maxSteps, _ := t.StepsToRoot(value)
			return "", fmt.Errorf("netsec: 值 %q 在树 %q 中最多泛化 %d 步，收到 %d 步", value, t.name, maxSteps, steps)
		}
		current = t.parent[current]
	}
	return current, nil
}
