// Package broadphase 实现物理引擎粗测阶段的碰撞筛选：
// 基于增量动态 AABB 树维护所有运动刚体的外包盒，
// 通过树内自相交遍历快速产出可能碰撞的候选实体对（Pairs），
// 避免 O(N^2) 的两两暴力比对。
package broadphase

// EntityID 是场景中刚体/实体的标识。
type EntityID int

// Pair 是一对包围盒发生重叠、需要移交窄测阶段做精细几何相交检测的候选实体对。
// 保证 A < B，且同一对不会重复出现。
type Pair struct {
	A, B EntityID
}

// Broadphase 是粗测阶段碰撞筛选器。
// 内部为每个实体维护一个向外扩张 margin 的“胖”包围盒：
// 实体小幅移动时只要未逃出胖包围盒，就无需触碰树结构；
// 只有逃出时才执行一次删除 + 重插（增量更新）。
type Broadphase struct {
	tree    *Tree
	margin  float64
	proxies map[EntityID]int32 // 实体 -> 树叶子节点
	tight   map[EntityID]AABB  // 实体 -> 当前精确包围盒
}

// NewBroadphase 创建筛选器。margin 为胖包围盒外扩距离，
// 取值建议略大于刚体单帧最大位移，以最大化增量更新收益。
func NewBroadphase(margin float64) *Broadphase {
	return &Broadphase{
		tree:    NewTree(),
		margin:  margin,
		proxies: make(map[EntityID]int32),
		tight:   make(map[EntityID]AABB),
	}
}

// Add 将实体及其包围盒加入粗测。
func (bp *Broadphase) Add(id EntityID, box AABB) {
	if _, ok := bp.proxies[id]; ok {
		bp.Move(id, box)
		return
	}
	node := bp.tree.Insert(box.Fattened(bp.margin), int(id))
	bp.proxies[id] = node
	bp.tight[id] = box
}

// Remove 将实体移出粗测。
func (bp *Broadphase) Remove(id EntityID) {
	node, ok := bp.proxies[id]
	if !ok {
		return
	}
	bp.tree.Remove(node)
	delete(bp.proxies, id)
	delete(bp.tight, id)
}

// Move 更新实体的精确包围盒。
// 若新包围盒仍被旧的胖包围盒包含，则只记录精确盒，树结构保持不变；
// 否则以新的胖包围盒增量重挂树节点。返回是否发生了树结构更新。
func (bp *Broadphase) Move(id EntityID, box AABB) bool {
	node, ok := bp.proxies[id]
	if !ok {
		return false
	}
	bp.tight[id] = box
	if bp.tree.nodes[node].aabb.Contains(box) {
		return false
	}
	bp.tree.Move(node, box.Fattened(bp.margin))
	return true
}

// Pairs 执行树内自相交遍历，返回当前所有包围盒重叠的候选实体对。
// 树内按胖包围盒粗筛，输出前再用精确包围盒复核，
// 因此返回结果与暴力两两检测精确包围盒完全一致，但复杂度远低于 O(N^2)。
func (bp *Broadphase) Pairs() []Pair {
	var pairs []Pair
	bp.tree.Pairs(func(a, b int) {
		ea := EntityID(a)
		eb := EntityID(b)
		if bp.tight[ea].Overlaps(bp.tight[eb]) {
			pairs = append(pairs, Pair{A: ea, B: eb})
		}
	})
	return pairs
}

// Count 返回当前受管的实体数量。
func (bp *Broadphase) Count() int { return bp.tree.Count() }
