// Package skiplist 实现一个基于多层随机链表的内存有序表（MemTable）。
//
// 特性：
//   - 支持并发有序插入（Set）与点查（Get）。
//   - 层级索引通过无锁指针（CAS 原子更新）维护，插入无需任何互斥锁。
//   - 迭代器在创建时物化只读快照，遍历过程不受后续并发写入干扰。
package skiplist

import (
	"sync/atomic"
	"unsafe"
)

// maxLevel 为跳表的最大层数。按 1/4 概率晋升，20 层足以支撑千万级键。
const maxLevel = 20

// node 是跳表节点。next 数组中的指针通过 sync/atomic 原子读写，
// value 指向堆上分配的 []byte，更新时整体原子替换，保证只读快照安全。
type node struct {
	key   int64
	value unsafe.Pointer // *[]byte
	next  [maxLevel]unsafe.Pointer
}

func loadNext(n *node, level int) *node {
	return (*node)(atomic.LoadPointer(&n.next[level]))
}

func casNext(n *node, level int, old, new *node) bool {
	return atomic.CompareAndSwapPointer(&n.next[level], unsafe.Pointer(old), unsafe.Pointer(new))
}

// SkipList 是并发安全的内存有序表。
type SkipList struct {
	head *node
	seed uint64 // 用于无锁随机层数生成
}

// New 创建一个空跳表。
func New() *SkipList {
	return &SkipList{head: &node{}, seed: 0x2545F4914F6CDD1D}
}

// randomLevel 以 1/4 概率逐层晋升，使用原子计数器 + 位混合避免全局锁。
func (sl *SkipList) randomLevel() int {
	r := atomic.AddUint64(&sl.seed, 0x9E3779B97F4A7C15)
	r ^= r >> 33
	r *= 0xff51afd7ed558ccd
	r ^= r >> 33
	level := 1
	for level < maxLevel && r&3 == 3 {
		level++
		r >>= 2
	}
	return level
}

// find 自顶向下查找 key，返回第一个 key' >= key 的节点。
// 若 preds/succs 非 nil，则记录每一层的前驱与后继。
func (sl *SkipList) find(key int64, preds, succs []*node) *node {
	pred := sl.head
	var curr *node
	for level := maxLevel - 1; level >= 0; level-- {
		curr = loadNext(pred, level)
		for curr != nil && curr.key < key {
			pred = curr
			curr = loadNext(pred, level)
		}
		if preds != nil {
			preds[level] = pred
		}
		if succs != nil {
			succs[level] = curr
		}
	}
	return curr
}

// Set 插入或更新键值对，可并发调用。相同 key 的 value 原子替换。
func (sl *SkipList) Set(key int64, value []byte) {
	var preds, succs [maxLevel]*node
	for {
		found := sl.find(key, preds[:], succs[:])
		if found != nil && found.key == key {
			v := value
			atomic.StorePointer(&found.value, unsafe.Pointer(&v))
			return
		}

		level := sl.randomLevel()
		v := value
		n := &node{key: key, value: unsafe.Pointer(&v)}
		for i := 0; i < level; i++ {
			n.next[i] = unsafe.Pointer(succs[i])
		}

		// 先在第 0 层 CAS 链接，成功即对外可见；失败则重新查找。
		if !casNext(preds[0], 0, succs[0], n) {
			continue
		}
		// 自底向上链接更高层，冲突时重新定位前驱后继后重试。
		for i := 1; i < level; i++ {
			for !casNext(preds[i], i, succs[i], n) {
				sl.find(key, preds[:], succs[:])
				n.next[i] = unsafe.Pointer(succs[i])
			}
		}
		return
	}
}

// Get 点查 key，返回 value 的拷贝与是否存在。
func (sl *SkipList) Get(key int64) ([]byte, bool) {
	n := sl.find(key, nil, nil)
	if n == nil || n.key != key {
		return nil, false
	}
	v := *(*[]byte)(atomic.LoadPointer(&n.value))
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// Iterator 是只读快照迭代器。创建时物化 [start, end) 区间内的
// 全部键值对，之后的并发写入不会影响本次遍历结果。
type Iterator struct {
	keys   []int64
	values [][]byte
	idx    int
}

// NewIterator 创建区间 [start, end) 的只读快照迭代器。
// 若需要扫描到末尾，end 可传 math.MaxInt64。
func (sl *SkipList) NewIterator(start, end int64) *Iterator {
	it := &Iterator{idx: -1}
	for n := sl.find(start, nil, nil); n != nil && n.key < end; n = loadNext(n, 0) {
		v := *(*[]byte)(atomic.LoadPointer(&n.value))
		cp := make([]byte, len(v))
		copy(cp, v)
		it.keys = append(it.keys, n.key)
		it.values = append(it.values, cp)
	}
	return it
}

// Next 前进到下一个元素，返回是否还有元素。
func (it *Iterator) Next() bool {
	it.idx++
	return it.idx < len(it.keys)
}

// Valid 报告当前是否指向有效元素。
func (it *Iterator) Valid() bool {
	return it.idx >= 0 && it.idx < len(it.keys)
}

// Key 返回当前元素的键。仅在 Valid 时有效。
func (it *Iterator) Key() int64 {
	return it.keys[it.idx]
}

// Value 返回当前元素的值（快照拷贝）。仅在 Valid 时有效。
func (it *Iterator) Value() []byte {
	return it.values[it.idx]
}
