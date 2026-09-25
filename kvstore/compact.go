package kvstore

import (
	"os"
	"sort"
)

// Compact 把除活跃段外的所有不可变段合并成一个新段，仅保留当前可见
// （未被覆盖、未被删除）的记录，然后通过 MANIFEST 原子切换。
//
// 恢复语义：
//   - rename 前崩溃：旧 MANIFEST 不变，新段是孤儿文件，重启时清理；
//   - rename 后崩溃：新 MANIFEST 已生效，旧段是孤儿文件，重启时清理。
//
// 旧段文件只有在所有 pin 住它的读者结束后才会被关闭并删除。
func (s *Store) Compact() error {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()

	// 1. 快照待压缩段并 pin 住，防止扫描期间被回收。
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrClosed
	}
	if len(s.order) < 2 {
		s.mu.RUnlock()
		return nil // 只有活跃段，无需压缩
	}
	oldIDs := append([]uint64(nil), s.order[:len(s.order)-1]...)
	oldSegs := make([]*segment, len(oldIDs))
	for i, id := range oldIDs {
		oldSegs[i] = s.segments[id]
		oldSegs[i].pin()
	}
	s.mu.RUnlock()
	defer func() {
		for _, seg := range oldSegs {
			seg.unpin() // 引用归零且已标记回收时，在此删除文件
		}
	}()

	oldSet := make(map[uint64]bool, len(oldIDs))
	segByID := make(map[uint64]*segment, len(oldIDs))
	for i, id := range oldIDs {
		oldSet[id] = true
		segByID[id] = oldSegs[i]
	}

	// 2. 收集位于旧段中的可见记录（按键排序，结果确定）。
	type entry struct {
		key    string
		oldLoc location
	}
	s.mu.RLock()
	live := make([]entry, 0, len(s.index))
	for k, loc := range s.index {
		if oldSet[loc.segID] {
			live = append(live, entry{k, loc})
		}
	}
	s.mu.RUnlock()
	sort.Slice(live, func(i, j int) bool { return live[i].key < live[j].key })

	// 3. 把可见记录原样（含 CRC）写入新段；全程不持 Store 锁，读写不阻塞。
	s.mu.Lock()
	newID := s.nextID
	s.nextID++
	s.mu.Unlock()
	newSeg, err := createSegment(s.dir, newID)
	if err != nil {
		return err
	}
	cleanupNew := func(err error) error {
		newSeg.file.Close()
		os.Remove(newSeg.path)
		return err
	}
	newLocs := make(map[string]location, len(live))
	off := int64(0)
	for _, e := range live {
		buf, err := segByID[e.oldLoc.segID].readRecordAt(e.oldLoc.offset, e.oldLoc.length)
		if err != nil {
			return cleanupNew(err)
		}
		if _, err := newSeg.file.WriteAt(buf, off); err != nil {
			return cleanupNew(err)
		}
		newLocs[e.key] = location{segID: newID, offset: off, length: int64(len(buf))}
		off += int64(len(buf))
	}
	newSeg.size = off
	if err := newSeg.file.Sync(); err != nil {
		return cleanupNew(err)
	}

	// 4. 原子切换：先持久化新清单（rename 前后均有故障注入点），再改内存状态。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return cleanupNew(ErrClosed)
	}
	newOrder := make([]uint64, 0, len(s.order)-len(oldIDs)+1)
	newOrder = append(newOrder, newID)
	for _, id := range s.order {
		if !oldSet[id] {
			newOrder = append(newOrder, id)
		}
	}
	if err := s.writeManifestLocked(newOrder); err != nil {
		return cleanupNew(err)
	}
	s.order = newOrder
	s.segments[newID] = newSeg

	// 5. 更新索引：仅当键仍指向压缩前的旧位置（期间可能被重写或删除）。
	for _, e := range live {
		if cur, ok := s.index[e.key]; ok && cur == e.oldLoc {
			s.index[e.key] = newLocs[e.key]
		}
	}

	// 6. 旧段移出段表并标记待回收；此后不再有新的 pin，
	//    等存量读者 unpin（含本函数 defer 的 unpin）后文件被删除。
	for _, seg := range oldSegs {
		delete(s.segments, seg.id)
		seg.removeWhenDone.Store(true)
	}
	return nil
}
