package locktable

import (
	"crypto/rand"
	"encoding/hex"
)

// conflictsLocked 判断 (id, owner) 申请 (rng, typ) 是否与其他属主的锁
// 冲突。同属主的锁不构成冲突（POSIX 语义允许同属主升级/替换）；
// 活跃锁与宽限期内的锁都参与冲突判定，防止断连窗口内被他人接管。
func (t *Table) conflictsLocked(id ConnID, owner LkOwner, rng ByteRange, typ LockType) bool {
	for key, entries := range t.locks {
		if key.conn == id && key.owner == owner {
			continue
		}
		for _, entry := range entries {
			if overlaps(rng, entry.rng) && (typ == WriteLock || entry.typ == WriteLock) {
				return true
			}
		}
	}
	for key, entry := range t.grace {
		if key.conn == id && key.owner == owner {
			continue
		}
		for _, lock := range entry.locks {
			if overlaps(rng, lock.rng) && (typ == WriteLock || lock.typ == WriteLock) {
				return true
			}
		}
	}
	return false
}

// addRangeLocked 写入同属主的新区间：先从已有区间中剔除重叠部分，
// 再与相邻同类型区间合并，保持每个属主的区间列表有序且互不重叠。
func (t *Table) addRangeLocked(id ConnID, owner LkOwner, rng ByteRange, typ LockType) {
	key := ownerKey{conn: id, owner: owner}
	remaining := []lockEntry{{rng: rng, typ: typ}}
	for _, entry := range t.locks[key] {
		next := make([]lockEntry, 0, len(remaining))
		for _, cur := range remaining {
			pieces, _ := subtract(cur.rng, entry.rng)
			for _, piece := range pieces {
				next = append(next, lockEntry{rng: piece, typ: cur.typ})
			}
		}
		remaining = next
	}

	entries := t.locks[key]
	for _, entry := range remaining {
		entries = insertEntry(entries, entry)
	}
	t.locks[key] = entries
}

// insertEntry 按起始偏移有序插入，并与相邻或重叠的同类型区间合并。
func insertEntry(entries []lockEntry, in lockEntry) []lockEntry {
	out := make([]lockEntry, 0, len(entries)+1)
	inserted := false
	for _, entry := range entries {
		if !inserted && entry.rng.Start > in.rng.Start {
			out = append(out, in)
			inserted = true
		}
		out = append(out, entry)
	}
	if !inserted {
		out = append(out, in)
	}

	merged := out[:0]
	for _, entry := range out {
		if len(merged) > 0 {
			prev := &merged[len(merged)-1]
			// 重叠或首尾相接（prev.End+1 == entry.Start，避免 uint64 回绕）。
			touching := entry.rng.Start <= prev.rng.End || prev.rng.End < ^uint64(0) && entry.rng.Start == prev.rng.End+1
			if prev.typ == entry.typ && touching {
				if entry.rng.End > prev.rng.End {
					prev.rng.End = entry.rng.End
				}
				continue
			}
		}
		merged = append(merged, entry)
	}
	return merged
}

// pumpLocked 按 FIFO 顺序授予当前可满足的阻塞请求；遇到第一个仍无法
// 满足的请求即停止，保证排在前面的请求不会被后来者越过。
func (t *Table) pumpLocked() {
	if len(t.waiters) == 0 {
		return
	}
	ready := make([]*waiter, 0)
	blocked := make([]*waiter, 0, len(t.waiters))
	for _, w := range t.waiters {
		// 一旦队首出现无法满足的请求，其后的请求一律继续等待，
		// 即使它们本身可满足也不得越过队首。
		if len(blocked) == 0 && !t.conflictsLocked(w.conn, w.owner, w.rng, w.typ) {
			t.addRangeLocked(w.conn, w.owner, w.rng, w.typ)
			ready = append(ready, w)
			continue
		}
		blocked = append(blocked, w)
	}
	t.waiters = blocked
	for _, w := range ready {
		close(w.ch)
	}
}

// detachWaitersLocked 移除属于 id 的全部阻塞请求，调用方负责在解锁后
// 关闭它们的通知通道。
func (t *Table) detachWaitersLocked(id ConnID, err error) []*waiter {
	if len(t.waiters) == 0 {
		return nil
	}
	removed := make([]*waiter, 0)
	kept := make([]*waiter, 0, len(t.waiters))
	for _, w := range t.waiters {
		if w.conn == id {
			w.err = err
			removed = append(removed, w)
		} else {
			kept = append(kept, w)
		}
	}
	t.waiters = kept
	return removed
}

func (t *Table) removeWaiterLocked(target *waiter) bool {
	for i, w := range t.waiters {
		if w == target {
			t.waiters = append(t.waiters[:i], t.waiters[i+1:]...)
			return true
		}
	}
	return false
}

func validateRange(rng ByteRange) error {
	if rng.Start > rng.End {
		return ErrInvalidRange
	}
	return nil
}

func newReclaimToken() (ReclaimToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return ReclaimToken(hex.EncodeToString(raw)), nil
}

// overlaps 判断两个闭区间是否相交。
func overlaps(a, b ByteRange) bool {
	return a.Start <= b.End && b.Start <= a.End
}

// subtract 返回从 held 中扣除 cut 后的剩余闭区间（0、1 或 2 段），
// 以及两者是否重叠。剩余区间保持闭区间语义。
func subtract(held, cut ByteRange) (pieces []ByteRange, overlapped bool) {
	if !overlaps(held, cut) {
		return []ByteRange{held}, false
	}
	if cut.Start <= held.Start {
		if cut.End >= held.End {
			return nil, true
		}
		return []ByteRange{{Start: cut.End + 1, End: held.End}}, true
	}
	if cut.End >= held.End {
		return []ByteRange{{Start: held.Start, End: cut.Start - 1}}, true
	}
	return []ByteRange{
		{Start: held.Start, End: cut.Start - 1},
		{Start: cut.End + 1, End: held.End},
	}, true
}
