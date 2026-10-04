// Package quota 实现层级配额树上的加权最大最小公平分配。
//
// 内部节点携带总上限 Cap，叶节点携带需求 Demand 与权重 Weight。
// 分配时沿树向下做注水（water-filling），每一层的份额用精确有理数
// (math/big.Rat) 表示，只在叶节点处向下取整，因此不会因逐层取整而
// 丢掉可用额度。取整后的剩余额度按叶编号轮转发放，游标保存在
// Allocator 中，跨批次（多次 Allocate 调用）持续轮转。
package quota

import (
	"fmt"
	"math/big"
	"sort"
)

// Node 是配额树中的一个节点。内部节点使用 Cap（子树可用总额度），
// 叶节点使用 Demand（需求）与 Weight（权重）。
type Node struct {
	ID       int
	Cap      int64
	Demand   int64
	Weight   int64
	Children []*Node
}

// IsLeaf 报告该节点是否为叶节点。
func (n *Node) IsLeaf() bool { return len(n.Children) == 0 }

func (n *Node) find(id int) *Node {
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if m := c.find(id); m != nil {
			return m
		}
	}
	return nil
}

// weightSum 返回子树的权重和（叶节点为自身权重）。
func (n *Node) weightSum() int64 {
	if n.IsLeaf() {
		return n.Weight
	}
	var s int64
	for _, c := range n.Children {
		s += c.weightSum()
	}
	return s
}

// demandSum 返回子树叶需求之和。
func (n *Node) demandSum() int64 {
	if n.IsLeaf() {
		return n.Demand
	}
	var s int64
	for _, c := range n.Children {
		s += c.demandSum()
	}
	return s
}

// effDemand 返回子树的有效需求：内部节点为 min(Cap, 叶需求之和)。
func (n *Node) effDemand() *big.Rat {
	if n.IsLeaf() {
		return rat(n.Demand)
	}
	d := rat(n.demandSum())
	c := rat(n.Cap)
	if c.Cmp(d) < 0 {
		return c
	}
	return d
}

// Allocator 对一棵固定的配额树做整数额度分配。它持有一个轮转游标，
// 跨多次 Allocate 调用保留，使余数按叶编号逐批轮转。
type Allocator struct {
	Root   *Node
	cursor int
}

// NewAllocator 校验树并创建分配器。
func NewAllocator(root *Node) (*Allocator, error) {
	if root == nil {
		return nil, fmt.Errorf("nil root")
	}
	if err := validate(root); err != nil {
		return nil, err
	}
	return &Allocator{Root: root}, nil
}

func validate(n *Node) error {
	if n.IsLeaf() {
		if n.Weight <= 0 {
			return fmt.Errorf("leaf %d: weight must be positive", n.ID)
		}
		if n.Demand < 0 {
			return fmt.Errorf("leaf %d: negative demand", n.ID)
		}
		return nil
	}
	if n.Cap < 0 {
		return fmt.Errorf("node %d: negative cap", n.ID)
	}
	for _, c := range n.Children {
		if err := validate(c); err != nil {
			return err
		}
	}
	return nil
}

// SetCap 修改内部节点的总上限。额度缩小后调用 Allocate 即按新上限
// 重新分配整棵树。
func (a *Allocator) SetCap(id int, cap int64) error {
	n := a.Root.find(id)
	if n == nil {
		return fmt.Errorf("node %d not found", id)
	}
	if n.IsLeaf() {
		return fmt.Errorf("node %d is a leaf; use SetDemand", id)
	}
	if cap < 0 {
		return fmt.Errorf("negative cap")
	}
	n.Cap = cap
	return nil
}

// SetDemand 修改叶节点的需求。
func (a *Allocator) SetDemand(id int, demand int64) error {
	n := a.Root.find(id)
	if n == nil {
		return fmt.Errorf("node %d not found", id)
	}
	if !n.IsLeaf() {
		return fmt.Errorf("node %d is internal; use SetCap", id)
	}
	if demand < 0 {
		return fmt.Errorf("negative demand")
	}
	n.Demand = demand
	return nil
}

// Allocate 重新计算整棵树的加权最大最小公平整数分配。
//
// 理想份额以精确分数沿树向下传递，仅在叶节点取整；余数从上次
// 游标位置起按叶编号轮转发放，每发一份推进一次游标。
func (a *Allocator) Allocate() map[int]int64 {
	leaves := sortedLeaves(a.Root)
	ideal := a.idealShares()

	alloc := make(map[int]int64, len(leaves))
	var floorSum, totalDemand int64
	for _, lf := range leaves {
		f := floorRat(ideal[lf.ID])
		alloc[lf.ID] = f
		floorSum += f
		totalDemand += lf.Demand
	}

	total := a.rootBudget()
	if totalDemand < total {
		total = totalDemand
	}
	leftover := total - floorSum

	for leftover > 0 && len(leaves) > 0 {
		progress := false
		for i := 0; i < len(leaves) && leftover > 0; i++ {
			lf := leaves[a.cursor%len(leaves)]
			a.cursor++
			if alloc[lf.ID] < lf.Demand {
				alloc[lf.ID]++
				leftover--
				progress = true
			}
		}
		if !progress { // 理论上不会发生：leftover <= 剩余需求之和
			break
		}
	}
	return alloc
}

// Verify 核验 alloc 是否为可行的加权最大最小公平整数分配：
//  1. 每个叶子的分配非负且不超过需求；
//  2. 每个内部节点的子树分配之和不超过其 Cap；
//  3. 每个叶子的分配与其精确公平份额相差不足 1（取整只允许 floor 或 floor+1）；
//  4. 分配总额等于 min(根可用额度, 总需求)，即不丢任何可用额度。
func (a *Allocator) Verify(alloc map[int]int64) error {
	leaves := sortedLeaves(a.Root)
	ideal := a.idealShares()

	var sum, totalDemand int64
	for _, lf := range leaves {
		v, ok := alloc[lf.ID]
		if !ok {
			return fmt.Errorf("leaf %d: missing allocation", lf.ID)
		}
		if v < 0 {
			return fmt.Errorf("leaf %d: negative allocation %d", lf.ID, v)
		}
		if v > lf.Demand {
			return fmt.Errorf("leaf %d: allocation %d exceeds demand %d", lf.ID, v, lf.Demand)
		}
		sum += v
		totalDemand += lf.Demand
	}
	if err := checkCaps(a.Root, alloc); err != nil {
		return err
	}
	for _, lf := range leaves {
		v := alloc[lf.ID]
		f := floorRat(ideal[lf.ID])
		if v != f && v != f+1 {
			return fmt.Errorf("leaf %d: allocation %d deviates from fair share %s",
				lf.ID, v, ideal[lf.ID].RatString())
		}
	}
	want := a.rootBudget()
	if totalDemand < want {
		want = totalDemand
	}
	if sum != want {
		return fmt.Errorf("total allocated %d, want %d: quota lost to rounding", sum, want)
	}
	return nil
}

func (a *Allocator) rootBudget() int64 {
	if a.Root.IsLeaf() {
		return a.Root.Demand
	}
	return a.Root.Cap
}

// idealShares 计算每个叶子的精确（有理数）公平份额。
func (a *Allocator) idealShares() map[int]*big.Rat {
	out := make(map[int]*big.Rat)
	a.Root.split(rat(a.rootBudget()), out)
	return out
}

// split 将 budget 在子树内做加权最大最小公平分配，结果写入 out。
func (n *Node) split(budget *big.Rat, out map[int]*big.Rat) {
	if n.IsLeaf() {
		d := rat(n.Demand)
		if budget.Cmp(d) > 0 {
			out[n.ID] = d
		} else {
			out[n.ID] = new(big.Rat).Set(budget)
		}
		return
	}
	shares := waterfill(budget, n.Children)
	for i, c := range n.Children {
		c.split(shares[i], out)
	}
}

// waterfill 在兄弟节点间做加权最大最小公平分配，返回每个孩子的份额。
// 分配结果之和等于 min(budget, 有效需求之和)，全程使用精确分数。
func waterfill(budget *big.Rat, children []*Node) []*big.Rat {
	type item struct {
		idx    int
		demand *big.Rat
		weight *big.Rat
	}
	items := make([]item, len(children))
	for i, c := range children {
		items[i] = item{idx: i, demand: c.effDemand(), weight: rat(c.weightSum())}
	}
	// 按 demand/weight 升序：先饱和的是阈值最低者。
	sort.Slice(items, func(x, y int) bool {
		l := new(big.Rat).Mul(items[x].demand, items[y].weight)
		r := new(big.Rat).Mul(items[y].demand, items[x].weight)
		return l.Cmp(r) < 0
	})

	res := make([]*big.Rat, len(children))
	remaining := new(big.Rat).Set(budget)
	wsum := new(big.Rat)
	for _, it := range items {
		wsum.Add(wsum, it.weight)
	}

	var pending []item
	for _, it := range items {
		// 若 demand/weight <= remaining/wsum，则该节点在当前水位下饱和。
		l := new(big.Rat).Mul(it.demand, wsum)
		r := new(big.Rat).Mul(remaining, it.weight)
		if l.Cmp(r) <= 0 {
			res[it.idx] = new(big.Rat).Set(it.demand)
			remaining.Sub(remaining, it.demand)
			wsum.Sub(wsum, it.weight)
		} else {
			pending = append(pending, it)
		}
	}
	if wsum.Sign() > 0 {
		level := new(big.Rat).Quo(remaining, wsum)
		for _, it := range pending {
			res[it.idx] = new(big.Rat).Mul(level, it.weight)
		}
	}
	return res
}

func checkCaps(n *Node, alloc map[int]int64) error {
	if n.IsLeaf() {
		return nil
	}
	var sum int64
	for _, lf := range collectLeaves(n) {
		sum += alloc[lf.ID]
	}
	if sum > n.Cap {
		return fmt.Errorf("node %d: subtree allocated %d exceeds cap %d", n.ID, sum, n.Cap)
	}
	for _, c := range n.Children {
		if err := checkCaps(c, alloc); err != nil {
			return err
		}
	}
	return nil
}

func collectLeaves(n *Node) []*Node {
	if n.IsLeaf() {
		return []*Node{n}
	}
	var out []*Node
	for _, c := range n.Children {
		out = append(out, collectLeaves(c)...)
	}
	return out
}

func sortedLeaves(root *Node) []*Node {
	leaves := collectLeaves(root)
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].ID < leaves[j].ID })
	return leaves
}

func rat(x int64) *big.Rat { return new(big.Rat).SetInt64(x) }

// floorRat 对非负有理数向下取整。
func floorRat(r *big.Rat) int64 {
	return new(big.Int).Quo(r.Num(), r.Denom()).Int64()
}
