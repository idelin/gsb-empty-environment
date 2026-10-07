package overlay

// Merge 按给定顺序从低到高叠加各层，返回合并后的树。
// 每个结果节点的 Layer 字段记录其来源层序号。
//
// 规则：
//   - 白化项删除下层同名节点，自身不进入结果；
//   - 不透明目录丢弃下层同名目录的全部子项，仅保留本层子项；
//   - 同路径类型不同（如文件 vs 目录）时上层整体替换下层；
//   - 两边都是目录时递归合并子项；
//   - 硬链接按层内 inode 共享，合并后共享关系保留。
func Merge(layers ...*Layer) *Node {
	var merged *Node
	for idx, l := range layers {
		merged = mergeLayer(merged, l.Root, idx, map[uint64]*Node{})
	}
	if merged == nil {
		return &Node{Name: "/", Kind: KindDir, Children: map[string]*Node{}, Layer: -1}
	}
	merged.Name = "/"
	return merged
}

// mergeLayer 将单层叠加到已合并的下层树上。memo 按本层 inode
// 去重，保证同层硬链接在结果中仍指向同一节点。
func mergeLayer(lower, upper *Node, idx int, memo map[uint64]*Node) *Node {
	if upper == nil {
		return lower
	}
	if lower == nil || upper.Kind != KindDir || lower.Kind != KindDir {
		// 新节点或类型替换：上层整体胜出。
		return cloneWithLayer(upper, idx, memo)
	}
	// 双方均为目录：递归合并。
	merged := &Node{
		Name:     upper.Name,
		Kind:     KindDir,
		Inode:    upper.Inode,
		Opaque:   upper.Opaque,
		Layer:    idx,
		Children: make(map[string]*Node, len(upper.Children)),
	}
	memo[upper.Inode] = merged
	if !upper.Opaque {
		// 透明目录继承下层子项；不透明目录跳过（屏蔽下层）。
		for name, child := range lower.Children {
			merged.Children[name] = child
		}
	}
	for _, name := range sortedNames(upper.Children) {
		child := upper.Children[name]
		if child.Whiteout {
			// 白化项：删除下层同名节点，自身不出现。
			delete(merged.Children, name)
			continue
		}
		merged.Children[name] = mergeLayer(merged.Children[name], child, idx, memo)
	}
	return merged
}

// cloneWithLayer 深拷贝子树并标注来源层；memo 保证同层硬链接
// 共享同一克隆节点。
func cloneWithLayer(n *Node, idx int, memo map[uint64]*Node) *Node {
	if prev, ok := memo[n.Inode]; ok {
		return prev
	}
	c := &Node{
		Name:     n.Name,
		Kind:     n.Kind,
		Data:     n.Data,
		Inode:    n.Inode,
		Opaque:   n.Opaque,
		Whiteout: n.Whiteout,
		Layer:    idx,
	}
	memo[n.Inode] = c
	if n.Kind == KindDir {
		c.Children = make(map[string]*Node, len(n.Children))
		for name, child := range n.Children {
			c.Children[name] = cloneWithLayer(child, idx, memo)
		}
	}
	return c
}
