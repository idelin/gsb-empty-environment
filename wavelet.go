package wavelet

import (
	"errors"
	"sort"
)

// ErrEmptyRange 在查询区间为空或 k 越界时返回。
var ErrEmptyRange = errors.New("wavelet: empty range or k out of bounds")

// node 是 wavelet tree 的一个节点，覆盖秩区间 [lo, hi)。
// 每个节点持有一个位向量 bv：bv[i]=0 表示该位置（按原相对顺序）
// 的秩落入左半区间，1 表示落入右半区间。
type node struct {
	lo, hi      int // 本节点覆盖的秩区间 [lo, hi)
	mid         int // 划分点：左子树 [lo, mid)，右子树 [mid, hi)
	bv          *bitVector
	left, right *node
}

// buildNode 用 ranks（值域压缩后的秩，保持原数组顺序）递归构建子树。
// 传入的 ranks 必须是本节点对应的子序列（稳定划分自父序列）。
func buildNode(ranks []int, lo, hi int) *node {
	nd := &node{lo: lo, hi: hi, mid: (lo + hi) / 2}
	if hi-lo <= 1 || len(ranks) == 0 {
		return nd
	}
	nd.bv = newBitVector(len(ranks))
	var leftRanks, rightRanks []int
	for i, r := range ranks {
		if r < nd.mid {
			leftRanks = append(leftRanks, r)
		} else {
			nd.bv.set(i)
			rightRanks = append(rightRanks, r)
		}
	}
	nd.bv.build()
	nd.left = buildNode(leftRanks, lo, nd.mid)
	nd.right = buildNode(rightRanks, nd.mid, hi)
	return nd
}

// access 返回原序列第 i 个位置的秩。
func (nd *node) access(i int) int {
	for nd.bv != nil {
		if nd.bv.words[i/64]&(1<<(uint(i)%64)) == 0 {
			i = nd.bv.rank0(i)
			nd = nd.left
		} else {
			i = nd.bv.rank1(i)
			nd = nd.right
		}
	}
	return nd.lo
}

// quantile 返回 ranks[l..r) 中第 k 小（k 从 0 计）的秩。
// 重复值按重数计入。调用方保证 0 <= k < r-l。
func (nd *node) quantile(l, r, k int) int {
	for nd.bv != nil {
		l0, r0 := nd.bv.rank0(l), nd.bv.rank0(r)
		if zeros := r0 - l0; k < zeros {
			l, r = l0, r0
			nd = nd.left
		} else {
			k -= zeros
			l, r = nd.bv.rank1(l), nd.bv.rank1(r)
			nd = nd.right
		}
	}
	return nd.lo
}

// lte 返回 ranks[l..r) 中秩 < x 的元素个数。
func (nd *node) lte(l, r, x int) int {
	if x <= nd.lo || l >= r {
		return 0
	}
	if nd.hi <= x {
		return r - l
	}
	return nd.left.lte(nd.bv.rank0(l), nd.bv.rank0(r), x) +
		nd.right.lte(nd.bv.rank1(l), nd.bv.rank1(r), x)
}

// bytes 返回以该节点为根的子树占用的字节数。
func (nd *node) bytes() int {
	total := 0
	if nd.bv != nil {
		total += nd.bv.bytes()
	}
	if nd.left != nil {
		total += nd.left.bytes()
	}
	if nd.right != nil {
		total += nd.right.bytes()
	}
	return total
}

// Tree 是整数 wavelet tree，键为任意可比较整数（int）。
// 构建时做坐标压缩：排序去重得到 vals，原序列被替换为秩序列，
// 但始终保持原始顺序（原序映射），查询无需对子数组排序。
type Tree struct {
	root  *node
	vals  []int // 升序去重后的原值，秩 -> 原值
	ranks []int // 原序的秩序列（原序映射）
	n     int
}

// New 用给定整数切片构建 wavelet tree。空切片是合法的。
func New(data []int) *Tree {
	t := &Tree{n: len(data)}
	if len(data) == 0 {
		return t
	}
	t.vals = sortedUnique(data)
	t.ranks = make([]int, len(data))
	for i, v := range data {
		t.ranks[i] = lowerBound(t.vals, v)
	}
	t.root = buildNode(t.ranks, 0, len(t.vals))
	return t
}

// Len 返回序列长度。
func (t *Tree) Len() int { return t.n }

// Access 返回原序列第 i 个位置的值（坐标解压后还原）。
func (t *Tree) Access(i int) int {
	if i < 0 || i >= t.n {
		panic("wavelet: Access index out of range")
	}
	return t.vals[t.root.access(i)]
}

// KthSmallest 返回 data[l..r)（0 起、半开区间）中第 k 小的值，k 从 0 计。
// 重复值按重数计；空区间或 k 越界返回 ErrEmptyRange。
func (t *Tree) KthSmallest(l, r, k int) (int, error) {
	if l < 0 || r > t.n || l >= r || k < 0 || k >= r-l {
		return 0, ErrEmptyRange
	}
	return t.vals[t.root.quantile(l, r, k)], nil
}

// RangeFreq 返回 data[l..r) 中值落在 [loVal, hiVal] 闭区间内的元素个数。
func (t *Tree) RangeFreq(l, r, loVal, hiVal int) int {
	if l < 0 {
		l = 0
	}
	if r > t.n {
		r = t.n
	}
	if l >= r || loVal > hiVal || t.root == nil {
		return 0
	}
	a := lowerBound(t.vals, loVal) // 第一个 >= loVal 的秩
	b := upperBound(t.vals, hiVal) // 第一个 > hiVal 的秩
	return t.root.lte(l, r, b) - t.root.lte(l, r, a)
}

// Freq 返回 data[l..r) 中值等于 v 的元素个数。
func (t *Tree) Freq(l, r, v int) int {
	return t.RangeFreq(l, r, v, v)
}

// Bytes 返回索引实际占用的字节数：
// 各层位向量（含 rank 索引）+ 坐标映射表 + 原序秩序列。
func (t *Tree) Bytes() int {
	total := 0
	if t.root != nil {
		total += t.root.bytes()
	}
	total += 8 * len(t.vals)  // int 映射表（64 位平台）
	total += 8 * len(t.ranks) // 原序秩序列
	return total
}

// Layout 返回编码布局的人类可读描述。
func (t *Tree) Layout() string {
	return "每层一个 bitVector：比特打包进 []uint64（64 bit/字），" +
		"每 512 bit 一个 uint64 超块前缀 popcount；" +
		"秩->原值用升序去重 []int 映射，原序秩序列 []int 保留原始顺序"
}

func sortedUnique(data []int) []int {
	cp := make([]int, len(data))
	copy(cp, data)
	sort.Ints(cp)
	out := cp[:0]
	for i, v := range cp {
		if i == 0 || v != cp[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// lowerBound 返回 a 中第一个 >= v 的下标。
func lowerBound(a []int, v int) int {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := (lo + hi) / 2
		if a[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// upperBound 返回 a 中第一个 > v 的下标。
func upperBound(a []int, v int) int {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := (lo + hi) / 2
		if a[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
