package main

import (
	"sort"
	"sync"
)

// entry 是索引中每个键的版本化记录；tombstone 表示已删除。
// 保留版本号是为了让迟到的旧版本事件无法复活已删除的键。
type entry struct {
	value     string
	version   uint64
	tombstone bool
}

// Index 是一个版本化索引。它满足两条核心不变式：
//  1. 每键消重：只有版本号严格更大的事件才会改变该键的数据；
//  2. 水位连续：watermark 是已应用版本的最大连续前缀，重发/乱序不影响正确性。
type Index struct {
	name      string
	mu        sync.RWMutex
	entries   map[string]entry
	applied   map[uint64]struct{} // 已应用的事件版本（用于乱序去重与水位推进）
	base      uint64              // 快照版本：<= base 的事件视为已被快照覆盖
	watermark uint64              // 已连续应用到的最大版本
}

func NewIndex(name string, base uint64) *Index {
	return &Index{
		name:      name,
		entries:   make(map[string]entry),
		applied:   make(map[uint64]struct{}),
		base:      base,
		watermark: base,
	}
}

func (ix *Index) Name() string { return ix.name }

// LoadSnapshot 用一致性快照初始化索引，快照中每个键带其版本号。
func (ix *Index) LoadSnapshot(snap map[string]VersionedValue) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for k, v := range snap {
		ix.entries[k] = entry{value: v.Value, version: v.Version}
	}
}

// Apply 应用一条事件，返回是否真正改变了数据。
// 重复事件（版本已应用）与过期事件（版本 <= 快照基线或 <= 该键当前版本）都被安全忽略。
func (ix *Index) Apply(e Event) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if e.Version <= ix.base {
		return false // 已被快照覆盖
	}
	if _, dup := ix.applied[e.Version]; dup {
		return false // 重发事件，全局去重
	}
	ix.applied[e.Version] = struct{}{}

	// 推进连续水位：乱序到达的事件先落袋，缺口补齐后水位跳升。
	for {
		if _, ok := ix.applied[ix.watermark+1]; ok {
			ix.watermark++
		} else {
			break
		}
	}

	// 每键版本消重：旧版本事件不得覆盖新版本数据（含墓碑）。
	cur, ok := ix.entries[e.Key]
	if ok && cur.version >= e.Version {
		return false
	}
	if e.Op == OpDelete {
		ix.entries[e.Key] = entry{version: e.Version, tombstone: true}
	} else {
		ix.entries[e.Key] = entry{value: e.Value, version: e.Version}
	}
	return true
}

// Get 是读路径。已删除（墓碑）的键对读者不可见。
func (ix *Index) Get(key string) (string, uint64, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	e, ok := ix.entries[key]
	if !ok || e.tombstone {
		return "", 0, false
	}
	return e.value, e.version, true
}

// Watermark 返回已连续应用到的最大事件版本。
func (ix *Index) Watermark() uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.watermark
}

// MissingUpto 返回 (base, upto] 范围内尚未应用的版本（水位缺口的证据）。
func (ix *Index) MissingUpto(upto uint64) []uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var missing []uint64
	for v := ix.base + 1; v <= upto; v++ {
		if _, ok := ix.applied[v]; !ok {
			missing = append(missing, v)
		}
	}
	return missing
}

// LiveEntries 返回所有对读者可见的键（排除墓碑），按键名排序。
func (ix *Index) LiveEntries() map[string]VersionedValue {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make(map[string]VersionedValue, len(ix.entries))
	for k, e := range ix.entries {
		if !e.tombstone {
			out[k] = VersionedValue{Value: e.value, Version: e.version}
		}
	}
	return out
}

// SortedKeys 返回可见键的有序列表，便于稳定输出。
func (ix *Index) SortedKeys() []string {
	live := ix.LiveEntries()
	keys := make([]string, 0, len(live))
	for k := range live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
