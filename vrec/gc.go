package vrec

import (
	"os"
	"path/filepath"
	"sort"
)

// GCStats 报告一次回收的结果。
type GCStats struct {
	Watermark    int64 // 回收水位：最老活跃读版本；无活跃读者时为 HeadVersion
	NodesKept    int   // 压缩后保留的版本节点数
	NodesRemoved int   // 释放的节点数（链前缀）
	OldWALBytes  int64 // 回收前 WAL 大小
	NewWALBytes  int64 // 回收后 WAL 大小
	DidCompact   bool  // 是否真正重写了 WAL
}

// oldestReaderVersion 返回当前所有活跃读事务/写事务中最老的快照版本。
// 无活跃读者时第二个返回值为 false。调用方须持有 wmu。
func (db *DB) oldestReaderVersion() (int64, bool) {
	if len(db.readers) == 0 {
		return 0, false
	}
	oldest := db.head + 1
	for _, r := range db.readers {
		if r.ver < oldest {
			oldest = r.ver
		}
	}
	return oldest, true
}

// collectRetained 在给定水位下挑选每个键需要保留的节点：
// 保留所有版本 > watermark 的节点，以及（若存在）版本 <= watermark 的最新节点
// 作为边界。没有任何节点满足条件的键整条丢弃。
// 调用方须持有 wmu。
func collectRetained(chains map[string][]*node, watermark int64, hasReader bool) map[string][]*node {
	kept := make(map[string][]*node, len(chains))
	for k, chain := range chains {
		if !hasReader {
			// 没有任何活跃读者：只需保留链顶，且链顶是墓碑时整条丢弃。
			top := chain[len(chain)-1]
			if !top.tomb {
				kept[k] = []*node{top}
			}
			continue
		}
		firstAbove := sort.Search(len(chain), func(i int) bool {
			return chain[i].ver > watermark
		})
		var out []*node
		if firstAbove > 0 {
			out = append(out, chain[firstAbove-1]) // 水位边界节点
		}
		out = append(out, chain[firstAbove:]...)
		if len(out) > 0 {
			kept[k] = out
		}
	}
	return kept
}

// RunGC 以最老活跃读版本为界执行回收。
//
// 语义：
//   - 长读只提供一个版本号，不持锁；GC 与其 Get 的内存安全由 RWMutex 保证。
//   - 只要存在固定在旧版本 v 的读者，所有它需要的链前缀（<= v 的边界节点）
//     都不会释放；必须先关闭该读者，下一次 GC 才能回收。
//   - 写事务也以其起始快照登记，因此它尚未提交期间，GC 不会越过其快照回收。
//
// 持久化采用“写临时文件并 fsync → 原子 rename → fsync 目录”的标准顺序，
// 任何注入点/崩溃都不会让目录里出现无法重放的日志。
func (db *DB) RunGC() (GCStats, error) {
	db.wmu.Lock()
	defer db.wmu.Unlock()

	var st GCStats
	if err := db.healthy(); err != nil {
		return st, err
	}

	oldest, hasReader := db.oldestReaderVersion()
	watermark := db.head
	if hasReader {
		watermark = oldest
	}
	st.Watermark = watermark
	st.OldWALBytes = db.walLen

	liveNodes := 0
	for _, chain := range db.chains {
		liveNodes += len(chain)
	}
	kept := collectRetained(db.chains, watermark, hasReader)
	keptCount := 0
	for _, chain := range kept {
		keptCount += len(chain)
	}
	st.NodesRemoved = liveNodes - keptCount
	st.NodesKept = keptCount

	// 注入点：写新日志之前。原 WAL 完好，中止无副作用。
	keysAll := make([]string, 0, len(db.chains))
	for k := range db.chains {
		keysAll = append(keysAll, k)
	}
	sort.Strings(keysAll)
	if err := db.fail(StageGCWrite, watermark, keysAll, true); err != nil {
		return st, err
	}

	// 内存上没有可回收节点时，重写只会得到等价内容，直接结束。
	if st.NodesRemoved == 0 {
		return st, nil
	}

	tmpPath := filepath.Join(db.dir, tmpName)
	tf, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return st, err
	}

	// compact 边界帧：版本上界为 watermark。重放时清空此前状态并把下一版本
	// 置为 watermark+1；随后写入的保留节点包含一个 <= watermark 的边界节点，
	// 其余节点版本 > watermark，重放按版本号取 max 即恢复正确 nextV。
	if err := appendFrame(tf, frame{ver: watermark, compact: true, key: "", value: nil}, false); err != nil {
		tf.Close()
		os.Remove(tmpPath)
		return st, err
	}

	keys := make([]string, 0, len(kept))
	for k := range kept {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, n := range kept[k] {
			if err := appendFrame(tf, frame{key: k, ver: n.ver, tombstone: n.tomb, value: n.val}, false); err != nil {
				tf.Close()
				os.Remove(tmpPath)
				return st, err
			}
		}
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmpPath)
		return st, err
	}

	// 注入点：临时文件已 fsync、改名之前。中止仅遗留（会被下次 Open 清理的）临时文件。
	if err := db.fail(StageGCRename, watermark, keys, true); err != nil {
		os.Remove(tmpPath)
		return st, err
	}

	walPath := filepath.Join(db.dir, walName)
	if err := os.Rename(tmpPath, walPath); err != nil {
		os.Remove(tmpPath)
		return st, err
	}

	// 注入点：rename 已完成、目录尚未 fsync。此处“进程消失”后重启，
	// 崩溃恢复只依赖日志本身：旧日志或新日志都能重放出一致状态。
	if err := db.fail(StageGCDirSync, watermark, keys, false); err != nil {
		// rename 已在内核中挂起/完成，继续把文件句柄切到新日志，使本进程
		// 也保持一致；随后 faulted 强制使用者重开验证。
		db.switchWALAfterGC(kept)
		st.NewWALBytes = db.walLen
		st.DidCompact = true
		return st, err
	}

	if err := syncDir(db.dir); err != nil {
		// rename 效果存在但目录项耐久性未确认：标记 faulted，重开后按实际日志恢复。
		db.switchWALAfterGC(kept)
		st.NewWALBytes = db.walLen
		st.DidCompact = true
		db.faulted.Store(true)
		return st, err
	}

	db.switchWALAfterGC(kept)
	st.NewWALBytes = db.walLen
	st.DidCompact = true
	return st, nil
}

// switchWALAfterGC 用压缩后的内存视图替换旧视图，并重新打开追加句柄。
func (db *DB) switchWALAfterGC(kept map[string][]*node) {
	if db.walFile != nil {
		db.walFile.Close()
	}
	f, err := os.OpenFile(filepath.Join(db.dir, walName), os.O_RDWR|os.O_APPEND, filePerm)
	if err != nil {
		db.faulted.Store(true)
		return
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		db.faulted.Store(true)
		return
	}
	db.walFile = f
	db.walLen = st.Size()
	db.chains = kept
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
