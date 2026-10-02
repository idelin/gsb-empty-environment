package swq

import "math/rand"

const (
	skipListMaxLevel = 32
	skipListP        = 0.25
)

// slNode is a node of an order-statistics skip list. span[i] is the number
// of bottom-level steps between this node and forward[i] at level i, which
// allows O(log n) rank-based lookup (Redis zskiplist style).
type slNode struct {
	value   float64
	id      uint64
	forward []*slNode
	span    []uint32
}

type skipList struct {
	header *slNode
	level  int
	length int
	rng    *rand.Rand
}

func newSkipList(rng *rand.Rand) *skipList {
	return &skipList{
		header: &slNode{
			forward: make([]*slNode, skipListMaxLevel),
			span:    make([]uint32, skipListMaxLevel),
		},
		level: 1,
		rng:   rng,
	}
}

// less orders elements by (value, id) so duplicate values stay distinct.
func less(aVal float64, aID uint64, bVal float64, bID uint64) bool {
	if aVal != bVal {
		return aVal < bVal
	}
	return aID < bID
}

func (sl *skipList) randomLevel() int {
	lvl := 1
	for lvl < skipListMaxLevel && sl.rng.Float64() < skipListP {
		lvl++
	}
	return lvl
}

func (sl *skipList) insert(value float64, id uint64) {
	var update [skipListMaxLevel]*slNode
	var rank [skipListMaxLevel]uint32

	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		if i == sl.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.forward[i] != nil && less(x.forward[i].value, x.forward[i].id, value, id) {
			rank[i] += x.span[i]
			x = x.forward[i]
		}
		update[i] = x
	}

	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			rank[i] = 0
			update[i] = sl.header
			update[i].span[i] = uint32(sl.length)
		}
		sl.level = lvl
	}

	x = &slNode{
		value:   value,
		id:      id,
		forward: make([]*slNode, lvl),
		span:    make([]uint32, lvl),
	}
	for i := 0; i < lvl; i++ {
		x.forward[i] = update[i].forward[i]
		update[i].forward[i] = x
		x.span[i] = update[i].span[i] - (rank[0] - rank[i])
		update[i].span[i] = (rank[0] - rank[i]) + 1
	}
	for i := lvl; i < sl.level; i++ {
		update[i].span[i]++
	}
	sl.length++
}

func (sl *skipList) delete(value float64, id uint64) bool {
	var update [skipListMaxLevel]*slNode

	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for x.forward[i] != nil && less(x.forward[i].value, x.forward[i].id, value, id) {
			x = x.forward[i]
		}
		update[i] = x
	}

	x = x.forward[0]
	if x == nil || x.value != value || x.id != id {
		return false
	}
	for i := 0; i < sl.level; i++ {
		if update[i].forward[i] == x {
			update[i].span[i] += x.span[i] - 1
			update[i].forward[i] = x.forward[i]
		} else {
			update[i].span[i]--
		}
	}
	for sl.level > 1 && sl.header.forward[sl.level-1] == nil {
		sl.level--
	}
	sl.length--
	return true
}

// kth returns the element at 0-based rank k in sorted order.
func (sl *skipList) kth(k int) (float64, uint64, bool) {
	if k < 0 || k >= sl.length {
		return 0, 0, false
	}
	target := uint32(k + 1)
	var traversed uint32
	x := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for x.forward[i] != nil && traversed+x.span[i] <= target {
			traversed += x.span[i]
			x = x.forward[i]
		}
		if traversed == target {
			return x.value, x.id, true
		}
	}
	return 0, 0, false
}
