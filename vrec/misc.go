package vrec

import "sort"

// Close 关闭数据库与底层 WAL。重复关闭返回 ErrTxnClosed 之外的 ErrClosed：
// 这里返回 ErrClosed 表示数据库层面重复关闭；进行中的读者不会被强制失效，
// 但其后续 Get 会因数据库已关闭而失败。
func (db *DB) Close() error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if db.closed {
		return ErrClosed
	}
	db.closed = true
	if db.walFile != nil {
		err := db.walFile.Close()
		db.walFile = nil
		return err
	}
	return nil
}

// Stats 是只读的运行时统计快照。
type Stats struct {
	HeadVersion   int64
	ActiveReaders int // 普通读事务数
	ActiveWriters int // 活跃写事务数（0 或 1）
	LiveKeys      int
	LiveNodes     int // 所有版本链上的节点总数
	WALBytes      int64
}

// Stats 返回当前统计，供演示与测试观察链前缀是否被回收。
func (db *DB) Stats() Stats {
	db.wmu.RLock()
	defer db.wmu.RUnlock()
	var s Stats
	s.HeadVersion = db.head
	for _, r := range db.readers {
		if r.writer {
			s.ActiveWriters++
		} else {
			s.ActiveReaders++
		}
	}
	s.LiveKeys = len(db.chains)
	nodes := 0
	for _, c := range db.chains {
		nodes += len(c)
	}
	s.LiveNodes = nodes
	s.WALBytes = db.walLen
	return s
}

// HistoryPoint 是某键版本历史上的一个节点。
type HistoryPoint struct {
	Version   int64
	Value     []byte // 墓碑节点为 nil
	Tombstone bool
}

// History 返回某键当前内存中保留的全部版本节点（按版本升序）。
// 主要用于演示与测试：可直接观察 GC 前后链前缀的释放。
func (db *DB) History(key string) []HistoryPoint {
	db.wmu.RLock()
	defer db.wmu.RUnlock()
	chain := db.chains[key]
	out := make([]HistoryPoint, len(chain))
	for i, n := range chain {
		out[i] = HistoryPoint{Version: n.ver, Tombstone: n.tomb}
		if !n.tomb {
			out[i].Value = append([]byte(nil), n.val...)
		}
	}
	return out
}

// View 在固定快照上执行只读操作；fn 返回后读事务自动关闭。
func (db *DB) View(fn func(*Reader) error) error {
	r, err := db.BeginReader()
	if err != nil {
		return err
	}
	defer r.Close()
	return fn(r)
}

// SortedKeys 返回当前最新版本上存在（非墓碑）的键，按字典序排列。
func (db *DB) SortedKeys() []string {
	db.wmu.RLock()
	defer db.wmu.RUnlock()
	var keys []string
	for k, chain := range db.chains {
		if len(chain) == 0 {
			continue
		}
		if n := chain[len(chain)-1]; !n.tomb {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
