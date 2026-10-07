package pseq

import "sync/atomic"

// 题面给定模数与多项式基。
const (
	Mod  int64 = 998244353
	Base int64 = 91138233
)

// Evidence 是一段序列的摘要证据：多项式摘要与长度。
// 二者必须同时核对；仅凭 Digest 相等不能断定内容相等。
type Evidence struct {
	Digest int64
	Len    int
}

type node struct {
	val  int64 // 已规范到 [0, Mod)
	prio uint64
	l, r *node
	size int
	hash int64 // 子树序列的多项式摘要
	refs int32
}

var (
	allocCount int64
	freeCount  int64
	prioState  uint64 = 0x9e3779b97f4a7c15
)

// AllocatedNodes 返回累计分配的节点数（测试与审计用）。
func AllocatedNodes() int64 { return atomic.LoadInt64(&allocCount) }

// FreedNodes 返回累计回收的节点数（测试与审计用）。
func FreedNodes() int64 { return atomic.LoadInt64(&freeCount) }

// LiveNodes 返回当前仍可达（未回收）的节点数。
func LiveNodes() int64 { return AllocatedNodes() - FreedNodes() }

func nextPrio() uint64 {
	// splitmix64 伪随机优先级；摘要与树形无关，优先级只影响平衡。
	for {
		z := atomic.AddUint64(&prioState, 0x9e3779b97f4a7c15)
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z = z ^ (z >> 31)
		if z != 0 {
			return z
		}
	}
}

// Norm 把任意整数规范到 [0, Mod)，负整数先取模。
func Norm(x int64) int64 {
	x %= Mod
	if x < 0 {
		x += Mod
	}
	return x
}

func size(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

func hashOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.hash
}

func powBase(k int) int64 {
	result := int64(1)
	base := Base
	for k > 0 {
		if k&1 == 1 {
			result = result * base % Mod
		}
		base = base * base % Mod
		k >>= 1
	}
	return result
}

func (n *node) recompute() {
	ls, rs := size(n.l), size(n.r)
	n.size = ls + 1 + rs
	// 序列折叠 h = h_l * B^(1+rs) + val * B^rs + h_r (mod Mod)。
	h := hashOf(n.l) * powBase(1+rs) % Mod
	h = (h + n.val*powBase(rs)) % Mod
	h = (h + hashOf(n.r)) % Mod
	n.hash = h
}

func retain(n *node) *node {
	if n != nil {
		atomic.AddInt32(&n.refs, 1)
	}
	return n
}

func release(n *node) {
	if n == nil {
		return
	}
	if atomic.AddInt32(&n.refs, -1) == 0 {
		release(n.l)
		release(n.r)
		atomic.AddInt64(&freeCount, 1)
	}
}

// mkNode 接管 l、r 各一份引用，返回调用方持有的一份计数引用。
func mkNode(val int64, prio uint64, l, r *node) *node {
	n := &node{val: val, prio: prio, l: l, r: r, refs: 1}
	n.recompute()
	atomic.AddInt64(&allocCount, 1)
	return n
}

// merge 借用 l、r，返回调用方持有的新引用；不修改任何既有节点。
func merge(l, r *node) *node {
	if l == nil {
		return retain(r)
	}
	if r == nil {
		return retain(l)
	}
	if l.prio > r.prio {
		left := retain(l.l)
		mid := merge(l.r, r)
		return mkNode(l.val, l.prio, left, mid)
	}
	mid := merge(l, r.l)
	right := retain(r.r)
	return mkNode(r.val, r.prio, mid, right)
}

// split 借用 n，把前 k 个元素分入左部，返回两份调用方持有的引用。
func split(n *node, k int) (*node, *node) {
	if n == nil {
		return nil, nil
	}
	ls := size(n.l)
	if k <= ls {
		ll, lr := split(n.l, k)
		right := mkNode(n.val, n.prio, lr, retain(n.r))
		return ll, right
	}
	rl, rr := split(n.r, k-ls-1)
	left := mkNode(n.val, n.prio, retain(n.l), rl)
	return left, rr
}

// rangeHash 计算子树序列下标 [l, r) 的多项式摘要，不分配节点。
func rangeHash(n *node, l, r int) int64 {
	if n == nil || l >= r {
		return 0
	}
	if l == 0 && r == n.size {
		return n.hash
	}
	ls := size(n.l)
	leftEnd := r
	if leftEnd > ls {
		leftEnd = ls
	}
	hl := int64(0)
	if l < leftEnd {
		hl = rangeHash(n.l, l, leftEnd)
	}
	valIncl := l <= ls && ls < r
	rightStart := l
	if rightStart < ls+1 {
		rightStart = ls + 1
	}
	hr := int64(0)
	rightLen := 0
	if rightStart < r {
		hr = rangeHash(n.r, rightStart-ls-1, r-ls-1)
		rightLen = r - rightStart
	}
	midLen := 0
	valContrib := int64(0)
	if valIncl {
		midLen = 1
		valContrib = n.val * powBase(rightLen) % Mod
	}
	h := hl * powBase(midLen+rightLen) % Mod
	h = (h + valContrib) % Mod
	h = (h + hr) % Mod
	return h
}

// Version 是序列的一个不可变快照句柄，持有一份根节点引用。
// 不再使用时必须 Release 以回收不可达节点。
type Version struct {
	root *node
}

// New 返回空序列版本。
func New() *Version { return &Version{} }

// Snapshot 以 O(1) 复制版本句柄，与源版本共享底层结构。
func (v *Version) Snapshot() *Version { return &Version{root: retain(v.root)} }

// Release 释放句柄持有的引用，回收因此不可达的节点。
// 释放后该句柄不可再使用。
func (v *Version) Release() {
	release(v.root)
	v.root = nil
}

// Len 返回序列长度。
func (v *Version) Len() int { return size(v.root) }

// Digest 返回整段序列的多项式摘要。
func (v *Version) Digest() int64 { return hashOf(v.root) }

// Evidence 返回整段序列的摘要与长度证据。
func (v *Version) Evidence() Evidence {
	return Evidence{Digest: v.Digest(), Len: v.Len()}
}

// RangeEvidence 返回下标区间 [l, r) 的摘要与长度证据，越界自动截断。
func (v *Version) RangeEvidence(l, r int) Evidence {
	l, r = clampRange(v.Len(), l, r)
	return Evidence{Digest: rangeHash(v.root, l, r), Len: r - l}
}

func clampRange(n, l, r int) (int, int) {
	if l < 0 {
		l = 0
	}
	if r > n {
		r = n
	}
	if r < l {
		r = l
	}
	return l, r
}

// PushBack 返回在右端追加元素后的新版本，元素先规范取模。
func (v *Version) PushBack(x int64) *Version {
	leaf := mkNode(Norm(x), nextPrio(), nil, nil)
	root := merge(v.root, leaf)
	release(leaf)
	return &Version{root: root}
}

// PushFront 返回在左端追加元素后的新版本，元素先规范取模。
func (v *Version) PushFront(x int64) *Version {
	leaf := mkNode(Norm(x), nextPrio(), nil, nil)
	root := merge(leaf, v.root)
	release(leaf)
	return &Version{root: root}
}

// PopFront 返回删除左端元素后的新版本；空序列仍为空。
func (v *Version) PopFront() *Version {
	dropped, rest := split(v.root, 1)
	release(dropped)
	return &Version{root: rest}
}

// PopBack 返回删除右端元素后的新版本；空序列仍为空。
func (v *Version) PopBack() *Version {
	kept, dropped := split(v.root, size(v.root)-1)
	release(dropped)
	return &Version{root: kept}
}

// Concat 返回 a 后接 b 的新版本。
// 只新建路径上的节点，共享子树为不可变结构，
// 两个版本即使共享底层节点也不会产生可变别名。
func Concat(a, b *Version) *Version {
	return &Version{root: merge(a.root, b.root)}
}

// Slice 返回下标区间 [l, r) 剪切出的新版本，越界自动截断。
func (v *Version) Slice(l, r int) *Version {
	l, r = clampRange(v.Len(), l, r)
	before, after := split(v.root, r)
	prefix, mid := split(before, l)
	release(before)
	release(prefix)
	release(after)
	return &Version{root: mid}
}

// Elements 按序返回全部元素（已规范），供核对与调试。
func (v *Version) Elements() []int64 {
	out := make([]int64, 0, v.Len())
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.l)
		out = append(out, n.val)
		walk(n.r)
	}
	walk(v.root)
	return out
}

// EqualContent 逐元素比较两个版本的内容。
// 摘要不要用于判等：模碰撞下摘要相同而内容不同。
func EqualContent(a, b *Version) bool {
	if a.Len() != b.Len() {
		return false
	}
	ea, eb := a.Elements(), b.Elements()
	for i := range ea {
		if ea[i] != eb[i] {
			return false
		}
	}
	return true
}
