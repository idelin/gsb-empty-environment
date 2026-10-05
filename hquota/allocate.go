package hquota

import "math"

const eps = 1e-9

// Allocator 在配额树上执行分配，并跨批次保留零头轮转游标。
type Allocator struct {
	tree   *Tree
	cursor int // 零头轮转游标：下一个优先获得零头的叶在 order 中的下标

	exact map[int]float64 // 最近一次分配的实数公平份额
	alloc map[int]int64   // 最近一次分配的整数额度
}

// NewAllocator 创建分配器。
func NewAllocator(t *Tree) *Allocator {
	return &Allocator{tree: t}
}

// Exact 返回最近一次 Allocate 的实数公平份额（未取整）。
func (a *Allocator) Exact() map[int]float64 {
	out := make(map[int]float64, len(a.exact))
	for k, v := range a.exact {
		out[k] = v
	}
	return out
}

// Allocation 返回最近一次 Allocate 的整数额度。
func (a *Allocator) Allocation() map[int]int64 {
	out := make(map[int]int64, len(a.alloc))
	for k, v := range a.alloc {
		out[k] = v
	}
	return out
}

// Allocate 重新计算全树分配并返回各叶整数额度（键为叶编号）。
//
// 流程：
//  1. 在实数域做加权最大最小公平注水，得到每个叶的精确份额；
//  2. 一次性向下取整（不在中间层取整，避免丢掉可用额度）；
//  3. 剩余零头按叶编号轮转分配，每叶每轮至多 1 单位，
//     受叶需求与沿途各节点上限约束；游标跨批次保留。
func (a *Allocator) Allocate() map[int]int64 {
	a.exact = waterFill(a.tree)
	base := make(map[int]int64, len(a.exact))
	for id, x := range a.exact {
		base[id] = int64(math.Floor(x + 1e-6))
	}
	a.distributeRemainder(base)
	a.alloc = base
	return a.Allocation()
}

// SetCap 调整节点额度并立即重新分配，返回新的整数额度。
// 节点额度缩小后，受影响子树按公平原则重新注水。
func (a *Allocator) SetCap(nodeID string, cap int64) (map[int]int64, error) {
	if err := a.tree.SetCap(nodeID, cap); err != nil {
		return nil, err
	}
	return a.Allocate(), nil
}

// waterFill 渐进注水：所有未冻结叶按权重同步抬升水位，
// 任一节点上限或叶需求触紧即冻结相应叶，直至全部冻结。
func waterFill(t *Tree) map[int]float64 {
	leaves := t.order
	alloc := make(map[int]float64, len(leaves))
	frozen := make(map[int]bool, len(leaves))

	maxIter := 2*(len(leaves)+len(t.nodes)) + 8
	for iter := 0; iter < maxIter; iter++ {
		nodeAlloc := make(map[*Node]float64, len(t.nodes))
		nodeWeight := make(map[*Node]float64, len(t.nodes))
		active := false
		for _, lf := range leaves {
			if !frozen[lf.ID] {
				active = true
			}
			for n := lf.Parent; n != nil; n = n.Parent {
				nodeAlloc[n] += alloc[lf.ID]
				if !frozen[lf.ID] {
					nodeWeight[n] += float64(lf.Weight)
				}
			}
		}
		if !active {
			break
		}

		dt := math.Inf(1)
		for _, lf := range leaves {
			if frozen[lf.ID] {
				continue
			}
			rem := float64(lf.Demand) - alloc[lf.ID]
			if rem <= eps {
				continue
			}
			if d := rem / float64(lf.Weight); d < dt {
				dt = d
			}
		}
		for n, w := range nodeWeight {
			if w <= 0 {
				continue
			}
			rem := float64(n.Cap) - nodeAlloc[n]
			// rem <= 0 表示节点已饱和：d <= 0 会把 dt 压到 0，
			// 本轮不前进，直接由冻结步骤冻结其下所有活跃叶。
			if d := rem / w; d < dt {
				dt = d
			}
		}
		if math.IsInf(dt, 1) {
			break
		}
		if dt < 0 {
			dt = 0
		}

		for _, lf := range leaves {
			if frozen[lf.ID] {
				continue
			}
			alloc[lf.ID] += dt * float64(lf.Weight)
			if alloc[lf.ID] > float64(lf.Demand) {
				alloc[lf.ID] = float64(lf.Demand)
			}
		}
		for _, lf := range leaves {
			if !frozen[lf.ID] && alloc[lf.ID] >= float64(lf.Demand)-eps {
				alloc[lf.ID] = math.Min(alloc[lf.ID], float64(lf.Demand))
				frozen[lf.ID] = true
			}
		}

		nodeAlloc = make(map[*Node]float64, len(t.nodes))
		for _, lf := range leaves {
			for n := lf.Parent; n != nil; n = n.Parent {
				nodeAlloc[n] += alloc[lf.ID]
			}
		}
		for n, sum := range nodeAlloc {
			if sum >= float64(n.Cap)-eps {
				for _, lf := range t.leavesUnder(n) {
					frozen[lf.ID] = true
				}
			}
		}
	}
	return alloc
}

// distributeRemainder 将取整后的剩余额度按叶编号轮转分发。
// 从游标开始循环扫描，每叶每轮至多 +1，须满足叶需求与全部祖先上限；
// 一整轮无任何分发即结束。游标保存在 Allocator 中跨批次延续。
func (a *Allocator) distributeRemainder(base map[int]int64) {
	leaves := a.tree.order
	n := len(leaves)
	if n == 0 {
		return
	}
	usage := make(map[*Node]int64, len(a.tree.nodes))
	for _, lf := range leaves {
		for nd := lf.Parent; nd != nil; nd = nd.Parent {
			usage[nd] += base[lf.ID]
		}
	}
	a.cursor %= n
	for {
		granted := false
		start := a.cursor
		last := -1
		for k := 0; k < n; k++ {
			i := (start + k) % n
			lf := leaves[i]
			if base[lf.ID] >= lf.Demand {
				continue
			}
			ok := true
			for nd := lf.Parent; nd != nil; nd = nd.Parent {
				if usage[nd]+1 > nd.Cap {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			base[lf.ID]++
			for nd := lf.Parent; nd != nil; nd = nd.Parent {
				usage[nd]++
			}
			granted = true
			last = i
		}
		if !granted {
			return
		}
		a.cursor = (last + 1) % n
	}
}
