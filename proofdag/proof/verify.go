package proof

import "fmt"

// Verify 按拓扑序检查图中每个节点的结论都由公开有限规则集合法推出。
// 检查项：
//  1. 无环（Kahn 拓扑排序，排不完则有环）；
//  2. 前提引用有效（无悬挂 ID）；
//  3. 每个节点的规则在公开规则集中登记，且前提个数匹配；
//  4. 结论由规则从有序前提的结论合法推出。
func Verify(g *Graph, rules *RuleSet) error {
	n := g.Len()
	indeg := make([]int, n)
	children := make([][]int, n)
	for i := 0; i < n; i++ {
		node := g.nodes[i]
		if node.ID != ID(i) {
			return fmt.Errorf("节点 %d 的 ID 字段 (%d) 与位置不一致", i, node.ID)
		}
		for _, p := range node.Premises {
			if p >= ID(n) {
				return fmt.Errorf("节点 %d 引用了不存在的前提 %d", i, p)
			}
			indeg[i]++
			children[p] = append(children[p], i)
		}
	}

	// Kahn 拓扑排序：按拓扑序逐个用规则校验；排不完则有环。
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++

		node := g.nodes[cur]
		rule, ok := rules.Lookup(node.Rule)
		if !ok {
			return fmt.Errorf("节点 %d 使用了未登记的规则 %q", cur, node.Rule)
		}
		if len(node.Premises) != rule.Arity() {
			return fmt.Errorf("节点 %d 规则 %q 需要 %d 个前提，实际 %d 个",
				cur, node.Rule, rule.Arity(), len(node.Premises))
		}
		premises := make([]string, len(node.Premises))
		for j, p := range node.Premises {
			premises[j] = g.nodes[p].Conclusion
		}
		if err := rule.Check(premises, node.Conclusion); err != nil {
			return fmt.Errorf("节点 %d (%s): %v", cur, node.Rule, err)
		}

		for _, ch := range children[cur] {
			indeg[ch]--
			if indeg[ch] == 0 {
				queue = append(queue, ch)
			}
		}
	}
	if visited != n {
		return fmt.Errorf("图中存在环：%d 个节点中只有 %d 个可拓扑排序", n, visited)
	}
	return nil
}
