package netsec

// rebuildSummaries 在加入新谓词后处理节点级摘要。
//
// 受影响区域（从 pivot 前向可达的节点）的摘要被丢弃，下一轮抽象可达时
// 重算；区域外的节点不可能经过冲突边，其摘要是精确的，只需按新谓词
// 无损重编码（重新枚举立方成员并抽象化），从而避免全量重建。
func (e *engine) rebuildSummaries(pivot int) {
	affected := e.forwardRegion(pivot)
	newSummaries := make(map[int]map[abstractState]*summaryEntry, len(e.summaries))

	for nodeIdx, nodeSum := range e.summaries {
		if affected[nodeIdx] {
			e.rebuilt += len(nodeSum)
			continue
		}
		reencoded := make(map[abstractState]*summaryEntry, len(nodeSum))
		for oldCube := range nodeSum {
			for _, cube := range e.reencodeCube(oldCube) {
				if _, ok := reencoded[cube]; ok {
					continue
				}
				reencoded[cube] = e.transformer(nodeIdx, cube)
				e.reencoded++
			}
		}
		if len(reencoded) > 0 {
			newSummaries[nodeIdx] = reencoded
		}
	}
	e.summaries = newSummaries
}

// forwardRegion 返回从 start 出发沿控制流前向可达的全部节点。
func (e *engine) forwardRegion(start int) map[int]bool {
	region := map[int]bool{start: true}
	stack := []int{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, t := range e.prog.Nodes[cur].Next {
			if !region[t] {
				region[t] = true
				stack = append(stack, t)
			}
		}
	}
	return region
}

// reencodeCube 把旧的入口立方按新谓词集合重新抽象。旧立方的具体成员
// 在新谓词下可能分裂为多个更细立方，因此返回的是一个立方集合。
func (e *engine) reencodeCube(oldCube abstractState) []abstractState {
	members := e.cubeMembers(oldCube)
	e.charge(len(members))
	buckets := make(map[abstractState]valSet)
	for _, s := range members {
		single := make(valSet)
		single.add(s)
		cube := e.abstractOf(single)
		if buckets[cube] == nil {
			buckets[cube] = make(valSet)
		}
		buckets[cube].add(s)
	}
	out := make([]abstractState, 0, len(buckets))
	for cube := range buckets {
		out = append(out, cube)
	}
	return out
}
