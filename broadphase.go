// Package broadphase 实现物理引擎粗测阶段（Broadphase）碰撞检测器。
//
// 核心是一棵增量动态 AABB 树：所有移动物体的外包盒以"肥胖"AABB
// （外加碰撞预测的位移扩张）存入树中；物体移动时增量更新包围盒，
// 随后通过树内自相交遍历快速产出可能碰撞的潜在实体对（Pairs），
// 移交窄测（Narrowphase）阶段做精细几何相交。
package broadphase

import "sort"

// Pair 是一对可能碰撞的实体 ID，保证 A < B。
type Pair struct {
	A, B int
}

// Broadphase 粗测碰撞筛选器。
type Broadphase struct {
	tree *Tree

	margin  float64 // AABB 肥胖边距：位移未超出边距时不更新树
	predict float64 // 碰撞预测系数：按 displacement*predict 沿运动方向扩张

	dirty       bool   // 树状态自上次 UpdatePairs 后是否变化
	cachedPairs []Pair // 缓存的配对结果
}

// New 创建粗测筛选器。
// margin 为肥胖边距，predict 为碰撞预测系数（0 表示不做位移预测）。
func New(margin, predict float64) *Broadphase {
	return &Broadphase{tree: NewTree(), margin: margin, predict: predict}
}

// ProxyCount 返回当前代理数量。
func (bp *Broadphase) ProxyCount() int { return bp.tree.Len() }

// TreeHeight 返回动态树高度，可用于观察平衡质量。
func (bp *Broadphase) TreeHeight() int { return bp.tree.Height() }

// FatAABB 返回指定实体当前存入树中的肥胖包围盒。
func (bp *Broadphase) FatAABB(id int) AABB { return bp.tree.AABB(id) }

// Add 添加实体及其紧密包围盒。id 重复会 panic。
func (bp *Broadphase) Add(id int, box AABB) {
	bp.tree.Insert(id, box.Fatten(bp.margin))
	bp.dirty = true
}

// Remove 移除实体。id 不存在会 panic。
func (bp *Broadphase) Remove(id int) {
	bp.tree.Remove(id)
	bp.dirty = true
}

// Move 更新实体的紧密包围盒。
// 若新包围盒仍被树中肥胖包围盒包含，则不做任何树操作（O(1)）；
// 否则以肥胖边距 + 位移预测扩张后增量重插（O(log N)）。
func (bp *Broadphase) Move(id int, box AABB, displacement Vec2) {
	if bp.tree.AABB(id).Contains(box) {
		return
	}
	grown := box.Fatten(bp.margin).Extend(displacement.Scale(bp.predict))
	bp.tree.Remove(id)
	bp.tree.Insert(id, grown)
	bp.dirty = true
}

// UpdatePairs 执行树内自相交遍历，返回当前所有包围盒重叠的潜在碰撞对。
// 结果按 (A, B) 升序排列；树未变化时直接返回缓存，调用方不应修改返回切片。
func (bp *Broadphase) UpdatePairs() []Pair {
	if !bp.dirty {
		return bp.cachedPairs
	}
	var pairs []Pair
	bp.tree.CollidePairs(func(a, b int) {
		if a > b {
			a, b = b, a
		}
		pairs = append(pairs, Pair{A: a, B: b})
	})
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	bp.cachedPairs = pairs
	bp.dirty = false
	return pairs
}
