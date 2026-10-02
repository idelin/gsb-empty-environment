package leveled

import (
	"container/heap"
	"os"
	"sort"
)

// compaction 描述一次待执行的压缩任务。
type compaction struct {
	level   int          // 源层
	inputs0 []*TableMeta // 源层参与文件
	inputs1 []*TableMeta // 下一层重叠文件
}

// MaybeCompact 循环检查各层，执行所有已触发条件的压缩。
func (db *DB) MaybeCompact() error {
	for {
		c := db.pickCompaction()
		if c == nil {
			return nil
		}
		if err := db.runCompaction(c); err != nil {
			return err
		}
	}
}

// pickCompaction 按 RocksDB 风格选择一次压缩：
//  1. L0 文件数达到阈值时，选中 L0 全部文件（L0 内部相互重叠，必须整体参与）；
//  2. 某层总大小超过容量阈值时，从中选出与下一层重叠范围最小的文件。
func (db *DB) pickCompaction() *compaction {
	if len(db.levels[0]) >= db.opts.L0CompactionThreshold {
		c := &compaction{level: 0, inputs0: append([]*TableMeta(nil), db.levels[0]...)}
		minKey, maxKey := keyRange(c.inputs0)
		c.inputs1 = db.overlappingTables(1, minKey, maxKey)
		return c
	}
	for l := 1; l+1 < len(db.levels); l++ {
		var total int64
		for _, t := range db.levels[l] {
			total += t.Size
		}
		if total <= db.opts.LevelCapacities[l-1] {
			continue
		}
		best := db.pickSmallestOverlap(l)
		if best == nil {
			continue
		}
		c := &compaction{level: l, inputs0: []*TableMeta{best}}
		c.inputs1 = db.overlappingTables(l+1, best.MinKey, best.MaxKey)
		return c
	}
	return nil
}

// pickSmallestOverlap 在 level 层中选出与下一层重叠数据量最小的文件，
// 平局时选自身 Key 范围最窄者。
func (db *DB) pickSmallestOverlap(level int) *TableMeta {
	var best *TableMeta
	var bestOverlap int64
	var bestSpan string
	for _, t := range db.levels[level] {
		var overlap int64
		for _, n := range db.levels[level+1] {
			if n.overlaps(t.MinKey, t.MaxKey) {
				overlap += n.Size
			}
		}
		span := spanOf(t.MinKey, t.MaxKey)
		if best == nil || overlap < bestOverlap || (overlap == bestOverlap && span < bestSpan) {
			best, bestOverlap, bestSpan = t, overlap, span
		}
	}
	return best
}

func spanOf(minKey, maxKey string) string {
	if len(maxKey) >= len(minKey) {
		return maxKey
	}
	return minKey
}

func keyRange(tables []*TableMeta) (string, string) {
	minKey, maxKey := tables[0].MinKey, tables[0].MaxKey
	for _, t := range tables[1:] {
		if t.MinKey < minKey {
			minKey = t.MinKey
		}
		if t.MaxKey > maxKey {
			maxKey = t.MaxKey
		}
	}
	return minKey, maxKey
}

func (db *DB) overlappingTables(level int, minKey, maxKey string) []*TableMeta {
	var out []*TableMeta
	for _, t := range db.levels[level] {
		if t.overlaps(minKey, maxKey) {
			out = append(out, t)
		}
	}
	return out
}

// isBaseLevelForKey 判断 key 在 outLevel 之下是否不存在更旧的版本，
// 若是，则压缩时其 Tombstone 可以安全清理。
func (db *DB) isBaseLevelForKey(outLevel int, key string) bool {
	for l := outLevel + 1; l < len(db.levels); l++ {
		for _, t := range db.levels[l] {
			if t.contains(key) {
				return false
			}
		}
	}
	return true
}

// runCompaction 执行多路归并：同 Key 取 Seq 最大者，清理可安全删除的
// Tombstone，结果写为下一层的新 SSTable，并删除输入文件。
func (db *DB) runCompaction(c *compaction) error {
	outLevel := c.level + 1
	inputs := append(append([]*TableMeta(nil), c.inputs0...), c.inputs1...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ID < inputs[j].ID })

	iters := make([]*tableIterator, 0, len(inputs))
	mh := &mergeHeap{}
	for _, t := range inputs {
		it, err := openTable(t.Path)
		if err != nil {
			return err
		}
		iters = append(iters, it)
	}
	defer func() {
		for _, it := range iters {
			it.close()
		}
	}()
	for i, it := range iters {
		if e, err := it.next(); err == nil {
			heap.Push(mh, mergeItem{entry: e, src: i})
		}
	}

	tw, err := createTable(db.dir, db.nextID, outLevel)
	if err != nil {
		return err
	}
	db.nextID++

	lastKey := ""
	haveLast := false
	for mh.Len() > 0 {
		item := heap.Pop(mh).(mergeItem)
		if e, err := iters[item.src].next(); err == nil {
			heap.Push(mh, mergeItem{entry: e, src: item.src})
		}
		e := item.entry
		if haveLast && e.Key == lastKey {
			continue // 同 Key 的旧版本，丢弃
		}
		haveLast = true
		lastKey = e.Key
		if e.Tombstone && db.isBaseLevelForKey(outLevel, e.Key) {
			continue // 下层无更旧版本，Tombstone 连同 Key 一起清理
		}
		if err := tw.add(e); err != nil {
			return err
		}
	}
	meta, err := tw.finish()
	if err != nil {
		return err
	}

	// 更新清单：移除输入文件，加入输出文件，删除旧文件。
	db.removeTables(c.level, c.inputs0)
	db.removeTables(outLevel, c.inputs1)
	if meta != nil {
		db.addTable(meta)
	}
	for _, t := range inputs {
		os.Remove(t.Path)
	}
	return nil
}

func (db *DB) removeTables(level int, tables []*TableMeta) {
	gone := make(map[uint64]bool, len(tables))
	for _, t := range tables {
		gone[t.ID] = true
	}
	kept := db.levels[level][:0]
	for _, t := range db.levels[level] {
		if !gone[t.ID] {
			kept = append(kept, t)
		}
	}
	db.levels[level] = kept
}

// mergeItem / mergeHeap 实现按 (Key 升序, Seq 降序) 排序的最小堆，
// 保证同 Key 的最新版本先弹出。
type mergeItem struct {
	entry Entry
	src   int
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	if h[i].entry.Key != h[j].entry.Key {
		return h[i].entry.Key < h[j].entry.Key
	}
	return h[i].entry.Seq > h[j].entry.Seq
}

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(mergeItem)) }

func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
