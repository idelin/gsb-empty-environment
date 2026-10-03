// Package mvcc 实现面向高频读业务的无锁 MVCC 读路径。
//
// 核心设计：
//   - 每个键对应一条不可变版本节点组成的单向链表，表头为 atomic.Pointer。
//   - 写事务为本次提交分配全局递增序列号 seq，先把所有新版本节点发布到
//     各自键的链头，最后原子地推进全局 commit 序列号（release）。
//   - 只读事务只原子读取一次 commit 序列号作为快照点，随后沿版本链无锁
//     遍历，取第一个 seq <= 快照点的版本。版本节点发布后永不修改，
//     因此读者绝不会看到写事务提交到一半的"半脏"数据。
//   - 读者不获取任何互斥锁；写者之间用一把仅写路径使用的互斥锁串行化，
//     读路径完全不接触它。
//
// 内存回收：读者在槽位表中登记自己的快照序列号，GC 只裁剪
// "所有活跃读者都不可见"的旧版本尾部，且裁剪动作本身也是无锁的。
package mvcc

import (
	"errors"
	"sync"
	"sync/atomic"
)

// readerSlots 是读者登记快照点的槽位数量，远大于预期的并发读者数。
const readerSlots = 256

// Version 是不可变的版本节点。发布后其 value 与 seq 永不修改，
// next 仅在发布前写入一次，之后只会被 GC 将尾部截断为 nil。
type Version struct {
	value int64
	seq   uint64
	next  atomic.Pointer[Version]
}

// Value 返回该版本携带的值。
func (v *Version) Value() int64 { return v.value }

// Seq 返回提交该版本的事务序列号。
func (v *Version) Seq() uint64 { return v.seq }

// Update 描述一次写事务中对单个键的修改。
type Update struct {
	Key   int
	Value int64
}

// ErrKeyOutOfRange 在键下标越界时返回。
var ErrKeyOutOfRange = errors.New("mvcc: key out of range")

// Store 是无锁 MVCC 键值存储，键为 [0, numKeys) 的整数。
type Store struct {
	heads  []atomic.Pointer[Version]
	commit atomic.Uint64

	// wmu 只串行化写事务；读路径永远不触碰它。
	wmu sync.Mutex

	// slots 记录活跃读者的快照点（存 seq+1，0 表示空槽），供 GC 使用。
	slots    [readerSlots]atomic.Uint64
	overflow atomic.Bool // 槽位耗尽时置位，本轮 GC 直接放弃
}

// NewStore 创建包含 numKeys 个键的存储，每个键以 initValue 作为
// 序列号 0 的创世版本。
func NewStore(numKeys int, initValue int64) *Store {
	s := &Store{heads: make([]atomic.Pointer[Version], numKeys)}
	for i := range s.heads {
		s.heads[i].Store(&Version{value: initValue, seq: 0})
	}
	return s
}

// NumKeys 返回键的数量。
func (s *Store) NumKeys() int { return len(s.heads) }

// Commit 以单个原子提交点发布一批更新，返回本次事务的序列号。
// 写者之间串行；读者在任何时刻都不会被本函数阻塞。
func (s *Store) Commit(updates ...Update) (uint64, error) {
	for _, u := range updates {
		if u.Key < 0 || u.Key >= len(s.heads) {
			return 0, ErrKeyOutOfRange
		}
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()

	seq := s.commit.Load() + 1
	// 先把所有新版本节点挂到链头。节点一旦发布即不可变。
	for _, u := range updates {
		node := &Version{value: u.Value, seq: seq}
		node.next.Store(s.heads[u.Key].Load())
		s.heads[u.Key].Store(node)
	}
	// 最后推进全局提交点：此后新快照才能看到上述版本。
	// 在此之前的旧快照因为 seq 过滤，天然看不到它们。
	s.commit.Store(seq)
	return seq, nil
}

// Latest 返回键当前的已提交值（读自己的写场景使用），无锁。
func (s *Store) Latest(key int) int64 {
	return s.heads[key].Load().value
}

// CommitSeq 返回当前全局提交序列号。
func (s *Store) CommitSeq() uint64 { return s.commit.Load() }

// Snapshot 是一个只读事务。它不属于任何锁，用完后调用 Close 释放
// GC 登记槽位。Snapshot 不可跨 goroutine 共享。
type Snapshot struct {
	store *Store
	seq   uint64
	slot  int // -1 表示未登记（槽位耗尽）
}

// Begin 开启一个只读事务，立即确定快照点，全程无锁。
func (s *Store) Begin() *Snapshot {
	seq := s.commit.Load()
	for i := range s.slots {
		// 槽位存 seq+1，0 表示空闲。
		if s.slots[i].CompareAndSwap(0, seq+1) {
			// 登记后复核：若提交点已前进，改用更新的快照点并更新登记，
			// 保证登记的快照点不落后于本快照实际使用的值。
			if cur := s.commit.Load(); cur != seq {
				s.slots[i].Store(cur + 1)
				seq = cur
			}
			return &Snapshot{store: s, seq: seq, slot: i}
		}
	}
	// 槽位耗尽（实践中不会发生）：快照依然可用，只是本轮 GC 必须放弃，
	// 由 Get 的"版本缺失重试"兜底正确性。
	s.overflow.Store(true)
	return &Snapshot{store: s, seq: seq, slot: -1}
}

// Seq 返回快照点的全局序列号。
func (sn *Snapshot) Seq() uint64 { return sn.seq }

// Get 读取键在快照点的值。返回 false 表示该键在快照点的版本已被
// GC 回收（仅当读者登记与 GC 发生极端竞争时），调用方应放弃本次
// 扫描并用新快照重试——绝不会返回撕裂的数据。
func (sn *Snapshot) Get(key int) (int64, bool) {
	for v := sn.store.heads[key].Load(); v != nil; v = v.next.Load() {
		if v.seq <= sn.seq {
			return v.value, true
		}
	}
	return 0, false
}

// Close 结束只读事务并释放 GC 登记槽位。
func (sn *Snapshot) Close() {
	if sn.slot >= 0 {
		sn.store.slots[sn.slot].Store(0)
		sn.slot = -1
	}
}

// minActiveSeq 返回所有活跃读者中最小的快照点；没有读者时返回当前提交点。
func (s *Store) minActiveSeq() uint64 {
	min := s.commit.Load()
	for i := range s.slots {
		if v := s.slots[i].Load(); v != 0 && v-1 < min {
			min = v - 1
		}
	}
	return min
}

// GC 裁剪所有活跃读者都不可见的旧版本，返回回收的节点数。
// 对每条链，找到"seq <= minActive 的最新版本"作为保留边界，
// 丢弃比它更老的尾部。边界节点本身保留，因此任何活跃或未来的
// 读者都仍能找到自己快照点处的版本。本函数无锁，可与读写并发，
// 但不应并发调用自身。
func (s *Store) GC() int {
	if s.overflow.Swap(false) {
		return 0 // 有未登记读者，本轮放弃
	}
	min := s.minActiveSeq()
	removed := 0
	for i := range s.heads {
		cut := s.heads[i].Load()
		for cut != nil && cut.seq > min {
			cut = cut.next.Load()
		}
		if cut == nil {
			continue
		}
		tail := cut.next.Load()
		if tail == nil {
			continue
		}
		for n := tail; n != nil; n = n.next.Load() {
			removed++
		}
		cut.next.Store(nil)
	}
	return removed
}
