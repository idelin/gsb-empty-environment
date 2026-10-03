// Package bitmap 提供压缩位图（Roaring 风格）实现：
// ArrayContainer / BitmapContainer / RunContainer(RLE) 自动转换，
// 批量 AND/OR/XOR 运算，以及基于 Copy-on-Write 的轻量级
// 增量快照与检查点（Checkpoint）归档。
package bitmap

import (
	"sort"
	"sync"
)

// tomb 记录被整体删除的容器（高 16 位键）及其删除代际，
// 供增量快照传播删除事件。
type tomb struct {
	key uint16
	gen uint64
}

// Bitmap 是 32 位无符号整数集合。高 16 位索引容器，低 16 位存于容器内。
// 并发安全：读写由 RWMutex 保护；存在活跃快照时对共享容器执行 COW。
type Bitmap struct {
	mu      sync.RWMutex
	keys    []uint16
	ctrs    []container
	gens    []uint64 // 与 keys 对齐：容器最后修改代际
	gen     uint64   // 全局代际，单调递增
	tombs   []tomb   // 自早期检查点以来被删除的容器键
	snaps   map[uint64]int
	maxSnap uint64 // 活跃快照的最大代际，0 表示无快照
}

// New 创建空位图。
func New() *Bitmap { return &Bitmap{} }

// find 返回键下标；未找到返回 -(插入位置)-1。
func (b *Bitmap) find(k uint16) int {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= k })
	if i < len(b.keys) && b.keys[i] == k {
		return i
	}
	return -i - 1
}

func (b *Bitmap) insertAt(pos int, k uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[pos+1:], b.keys[pos:])
	b.keys[pos] = k
	b.ctrs = append(b.ctrs, nil)
	copy(b.ctrs[pos+1:], b.ctrs[pos:])
	b.ctrs[pos] = c
	b.gens = append(b.gens, 0)
	copy(b.gens[pos+1:], b.gens[pos:])
	b.gens[pos] = b.gen
}

func (b *Bitmap) removeAt(pos int, recordTomb bool) {
	if recordTomb {
		b.tombs = append(b.tombs, tomb{key: b.keys[pos], gen: b.gen})
	}
	b.keys = append(b.keys[:pos], b.keys[pos+1:]...)
	b.ctrs = append(b.ctrs[:pos], b.ctrs[pos+1:]...)
	b.gens = append(b.gens[:pos], b.gens[pos+1:]...)
}

// pinView 在同一临界区内注册内部快照代际并拷贝索引视图，
// 保证视图中所有容器代际 <= pinGen：序列化期间写线程对这些容器
// 执行 COW，归档器无需持锁即可安全读取。必须与 unpin 配对。
func (b *Bitmap) pinView() (pinGen uint64, keys []uint16, ctrs []container) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pinGen = b.pinLocked()
	keys = make([]uint16, len(b.keys))
	copy(keys, b.keys)
	ctrs = make([]container, len(b.ctrs))
	copy(ctrs, b.ctrs)
	return pinGen, keys, ctrs
}

// pinDelta 与 pinView 相同，但仅收集代际晚于 base 的变更容器与墓碑。
func (b *Bitmap) pinDelta(base uint64) (pinGen uint64, tombs []uint16, keys []uint16, ctrs []container) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pinGen = b.pinLocked()
	for _, t := range b.tombs {
		if t.gen > base {
			tombs = append(tombs, t.key)
		}
	}
	for i := range b.keys {
		if b.gens[i] > base {
			keys = append(keys, b.keys[i])
			ctrs = append(ctrs, b.ctrs[i])
		}
	}
	return pinGen, tombs, keys, ctrs
}

// pinLocked 注册一个内部快照代际（调用方须持有写锁）。
func (b *Bitmap) pinLocked() uint64 {
	b.gen++
	if b.snaps == nil {
		b.snaps = make(map[uint64]int)
	}
	b.snaps[b.gen]++
	b.maxSnap = b.gen // gen 单调递增，新 pin 即最大
	return b.gen
}

func (b *Bitmap) unpin(gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := b.snaps[gen]; n > 1 {
		b.snaps[gen] = n - 1
	} else {
		delete(b.snaps, gen)
	}
	b.maxSnap = 0
	for g := range b.snaps {
		if g > b.maxSnap {
			b.maxSnap = g
		}
	}
}

// cowLocked 在容器仍被活跃快照共享时先克隆（调用方须持有写锁）。
// 容器最后修改代际 <= 最大活跃快照代际，说明该实例被快照持有。
func (b *Bitmap) cowLocked(i int) {
	if b.maxSnap != 0 && b.gens[i] <= b.maxSnap {
		b.ctrs[i] = b.ctrs[i].clone()
	}
}

// Add 插入一个值。
func (b *Bitmap) Add(x uint32) {
	k, v := uint16(x>>16), uint16(x)
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.find(k)
	b.gen++
	if i < 0 {
		b.insertAt(-i-1, k, &arrayContainer{data: []uint16{v}})
		return
	}
	b.cowLocked(i)
	b.ctrs[i] = b.ctrs[i].add(v)
	b.gens[i] = b.gen
}

// AddBatch 批量插入。
func (b *Bitmap) AddBatch(vals ...uint32) {
	for _, v := range vals {
		b.Add(v)
	}
}

// AddRange 插入连续区间 [lo, hi)，自动使用 RLE 容器。
func (b *Bitmap) AddRange(lo, hi uint64) {
	if hi > 1<<32 {
		hi = 1 << 32
	}
	if lo >= hi {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	k0, k1 := lo>>16, (hi-1)>>16
	for k := k0; k <= k1; k++ {
		var loV, hiV uint16 = 0, 0xFFFF
		if k == k0 {
			loV = uint16(lo & 0xFFFF)
		}
		if k == k1 {
			hiV = uint16((hi - 1) & 0xFFFF)
		}
		b.addRangeLocked(uint16(k), loV, hiV)
	}
}

func (b *Bitmap) addRangeLocked(k uint16, loV, hiV uint16) {
	rc := &runContainer{ivs: []interval{{loV, hiV}}}
	i := b.find(k)
	b.gen++
	if i < 0 {
		b.insertAt(-i-1, k, rc.maybeConvert())
		return
	}
	b.cowLocked(i)
	b.ctrs[i] = orContainers(b.ctrs[i], rc)
	b.gens[i] = b.gen
}

// Remove 删除一个值；容器变空时移除容器并记录墓碑。
func (b *Bitmap) Remove(x uint32) {
	k, v := uint16(x>>16), uint16(x)
	b.mu.Lock()
	defer b.mu.Unlock()
	i := b.find(k)
	if i < 0 {
		return
	}
	b.cowLocked(i)
	nc := b.ctrs[i].remove(v)
	b.gen++
	if nc.cardinality() == 0 {
		b.removeAt(i, true)
		return
	}
	b.ctrs[i] = nc
	b.gens[i] = b.gen
}

// Contains 判断值是否存在。
func (b *Bitmap) Contains(x uint32) bool {
	k, v := uint16(x>>16), uint16(x)
	b.mu.RLock()
	defer b.mu.RUnlock()
	i := b.find(k)
	return i >= 0 && b.ctrs[i].contains(v)
}

// Cardinality 返回元素总数。
func (b *Bitmap) Cardinality() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var n uint64
	for _, c := range b.ctrs {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty 报告位图是否为空。
func (b *Bitmap) IsEmpty() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.keys) == 0
}

// Stats 统计各类型容器数量。
type Stats struct {
	Containers int
	Array      int
	Bitmap     int
	Run        int
}

// Stats 返回当前容器构成。
func (b *Bitmap) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var s Stats
	s.Containers = len(b.ctrs)
	for _, c := range b.ctrs {
		switch c.ctype() {
		case typeArray:
			s.Array++
		case typeBitmap:
			s.Bitmap++
		case typeRun:
			s.Run++
		}
	}
	return s
}

// Gen 返回当前代际（每次变更单调递增）。
func (b *Bitmap) Gen() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.gen
}

// ToSlice 导出全部元素（升序）。大位图请改用 Iterator。
func (b *Bitmap) ToSlice() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	it := b.Iterator()
	for v, ok := it.Next(); ok; v, ok = it.Next() {
		out = append(out, v)
	}
	return out
}

// Iterator 按升序遍历位图。
// 注意：Iterator 持有的是创建时刻的容器索引快照；
// 若遍历期间存在并发写入，请使用 Snapshot.Iterator()。
type Iterator struct {
	keys []uint16
	ctrs []container
	ki   int
	vals []uint16
	vi   int
}

// Iterator 创建迭代器。
func (b *Bitmap) Iterator() *Iterator {
	b.mu.RLock()
	defer b.mu.RUnlock()
	keys := make([]uint16, len(b.keys))
	copy(keys, b.keys)
	ctrs := make([]container, len(b.ctrs))
	copy(ctrs, b.ctrs)
	return &Iterator{keys: keys, ctrs: ctrs, ki: -1}
}

// Next 返回下一个元素；ok 为 false 表示遍历结束。
func (it *Iterator) Next() (v uint32, ok bool) {
	for {
		if it.vi < len(it.vals) {
			v = uint32(it.keys[it.ki])<<16 | uint32(it.vals[it.vi])
			it.vi++
			return v, true
		}
		it.ki++
		if it.ki >= len(it.keys) {
			return 0, false
		}
		it.vals = it.ctrs[it.ki].toSlice()
		it.vi = 0
	}
}
