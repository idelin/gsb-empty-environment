// Package mvcc 实现多版本并发控制（MVCC）的版本链管理与垃圾回收（GC）。
//
// 核心设计：
//   - 每个数据项维护一条版本链表，按时间戳倒序（最新版本在表头）。
//   - 读操作携带 ReadView（读时间戳），沿链表找到第一个 ts <= readView 的版本。
//   - Store 追踪全局最小活跃 ReadView，GC 据此安全修剪被覆盖且不再可见的废弃版本。
package mvcc

import (
	"fmt"
	"sync"
)

// Version 表示数据项的一个版本，通过 Next 指针串成按时间戳倒序的单链表。
type Version struct {
	TS    uint64   // 版本时间戳（提交时间）
	Value string   // 版本数据
	Next  *Version // 指向下一个更旧的版本
}

// VersionChain 是单个数据项的版本链表，表头永远是最新版本。
type VersionChain struct {
	head *Version
}

// insert 在表头插入新版本（要求新版本时间戳大于现有所有版本）。
func (c *VersionChain) insert(ts uint64, value string) {
	c.head = &Version{TS: ts, Value: value, Next: c.head}
}

// visible 沿链表查找对给定 ReadView 可见的版本：
// 第一个满足 ts <= readTS 的版本。
func (c *VersionChain) visible(readTS uint64) *Version {
	for v := c.head; v != nil; v = v.Next {
		if v.TS <= readTS {
			return v
		}
	}
	return nil
}

// len 返回链表中版本数量（用于观察 GC 效果）。
func (c *VersionChain) len() int {
	n := 0
	for v := c.head; v != nil; v = v.Next {
		n++
	}
	return n
}

// timestamps 返回链表中所有版本的时间戳（新到旧），用于测试与调试。
func (c *VersionChain) timestamps() []uint64 {
	var out []uint64
	for v := c.head; v != nil; v = v.Next {
		out = append(out, v.TS)
	}
	return out
}

// ReadView 表示一个活跃的只读事务视图。
type ReadView struct {
	id     uint64
	readTS uint64
}

// TS 返回该读视图的时间戳。
func (rv *ReadView) TS() uint64 { return rv.readTS }

// Store 是 MVCC 存储：管理所有数据项的版本链，追踪活跃 ReadView，并执行 GC。
type Store struct {
	mu        sync.RWMutex
	chains    map[string]*VersionChain
	readViews map[uint64]*ReadView // 活跃读事务：id -> ReadView
	nextRVid  uint64
}

// NewStore 创建一个空的 MVCC 存储。
func NewStore() *Store {
	return &Store{
		chains:    make(map[string]*VersionChain),
		readViews: make(map[uint64]*ReadView),
	}
}

// Write 以时间戳 ts 向数据项 key 写入一个新版本（追加到版本链表头）。
func (s *Store) Write(key string, ts uint64, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chain, ok := s.chains[key]
	if !ok {
		chain = &VersionChain{}
		s.chains[key] = chain
	}
	if chain.head != nil && ts <= chain.head.TS {
		panic(fmt.Sprintf("mvcc: 写入时间戳 %d 必须大于当前最新版本时间戳 %d", ts, chain.head.TS))
	}
	chain.insert(ts, value)
}

// BeginRead 开启一个只读事务，返回其 ReadView。
// 该 ReadView 会被记录为活跃视图，直到调用 EndRead 释放。
func (s *Store) BeginRead(readTS uint64) *ReadView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRVid++
	rv := &ReadView{id: s.nextRVid, readTS: readTS}
	s.readViews[rv.id] = rv
	return rv
}

// EndRead 结束只读事务，释放其 ReadView，使其不再阻塞 GC。
func (s *Store) EndRead(rv *ReadView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.readViews, rv.id)
}

// Read 在 ReadView rv 下读取 key 的可见版本。
// 返回 (value, versionTS, found)。
func (s *Store) Read(rv *ReadView, key string) (string, uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain, ok := s.chains[key]
	if !ok {
		return "", 0, false
	}
	v := chain.visible(rv.readTS)
	if v == nil {
		return "", 0, false
	}
	return v.Value, v.TS, true
}

// MinActiveReadTS 返回全局最小活跃 ReadView 时间戳。
// 若没有活跃读事务，返回 (0, false)。
func (s *Store) MinActiveReadTS() (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var min uint64
	found := false
	for _, rv := range s.readViews {
		if !found || rv.readTS < min {
			min = rv.readTS
			found = true
		}
	}
	return min, found
}

// GC 对 key 的版本链执行垃圾回收，返回被修剪掉的版本数量。
//
// 修剪规则：设 minRead 为全局最小活跃 ReadView（无活跃读者时视为 +∞，
// 即只保留最新版本）。沿链表找到"对最老读者仍可见的最旧版本"——
// 即最后一个满足 ts <= minRead 的版本——它及其之前的链表中，
// 只有它本身需要保留；比它更旧的版本已被它覆盖且对任何活跃读者不可见，
// 可以安全释放。
func (s *Store) GC(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	chain, ok := s.chains[key]
	if !ok || chain.head == nil {
		return 0
	}

	minRead := ^uint64(0) // 无活跃读者时视为无穷大：仅保留最新版本
	for _, rv := range s.readViews {
		if rv.readTS < minRead {
			minRead = rv.readTS
		}
	}

	// 找到第一个满足 v.TS <= minRead 的版本 v：
	// v 是最老活跃读者能看到的版本，v.Next 及之后的版本全部废弃。
	pruned := 0
	for v := chain.head; v != nil; v = v.Next {
		if v.TS <= minRead {
			for p := v.Next; p != nil; p = p.Next {
				pruned++
			}
			v.Next = nil // 截断链表，废弃版本不再被引用，交由 Go GC 释放内存
			break
		}
	}
	return pruned
}

// VersionCount 返回 key 当前版本链中的版本数量（用于观察 GC 效果）。
func (s *Store) VersionCount(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain, ok := s.chains[key]
	if !ok {
		return 0
	}
	return chain.len()
}

// VersionTimestamps 返回 key 版本链中所有版本时间戳（新到旧）。
func (s *Store) VersionTimestamps(key string) []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain, ok := s.chains[key]
	if !ok {
		return nil
	}
	return chain.timestamps()
}
