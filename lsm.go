// Package lsm 实现一个类似 RocksDB 的分层压缩（Leveled Compaction）调度合并器。
//
// 核心规则：
//   - Level 0 的文件之间 Key 范围可以重叠，文件数达到阈值即触发压实；
//   - Level N (N>=1) 按层容量（BaseLevelSize * 倍数^(N-1)）触发压实；
//   - 触发时在源层挑选与下一层重叠 Key 范围最小的 SSTable（L0 则选全部），
//     与下一层所有有交集的 SSTable 做多路归并，写出新文件到下一层；
//   - 归并时每个 Key 只保留最新版本；当目标层之下已无任何数据（最底层）时，
//     清理 Tombstone，物理删除废弃 Key。
package lsm

import (
	"fmt"
	"os"
	"sort"
)

// Options 是 LSM 的配置项。
type Options struct {
	Dir string // 数据目录

	L0CompactionTrigger int   // L0 文件数达到该值触发压实，默认 4
	BaseLevelSize       int64 // L1 的容量阈值（字节），默认 1MB
	SizeMultiplier      int   // 每往下一层容量放大的倍数，默认 10
	MaxLevels           int   // 最大层数，默认 7
}

func (o *Options) setDefaults() {
	if o.L0CompactionTrigger <= 0 {
		o.L0CompactionTrigger = 4
	}
	if o.BaseLevelSize <= 0 {
		o.BaseLevelSize = 1 << 20
	}
	if o.SizeMultiplier <= 0 {
		o.SizeMultiplier = 10
	}
	if o.MaxLevels <= 0 {
		o.MaxLevels = 7
	}
}

// LSM 是分层存储引擎：内存表 + 若干层 SSTable。
type LSM struct {
	opt    Options
	levels [][]*SSTable
	mem    []Entry
	nextID int
	seq    uint64
}

// Open 打开（或创建）一个 LSM 存储。
func Open(opt Options) (*LSM, error) {
	opt.setDefaults()
	if opt.Dir == "" {
		return nil, fmt.Errorf("lsm: Dir is required")
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}
	return &LSM{
		opt:    opt,
		levels: make([][]*SSTable, opt.MaxLevels),
		nextID: 1,
	}, nil
}

// Put 写入一个 Key。
func (l *LSM) Put(key, value string) {
	l.seq++
	l.mem = append(l.mem, Entry{Seq: l.seq, Op: OpPut, Key: key, Value: []byte(value)})
}

// Del 删除一个 Key（写入 Tombstone）。
func (l *LSM) Del(key string) {
	l.seq++
	l.mem = append(l.mem, Entry{Seq: l.seq, Op: OpDelete, Key: key})
}

// Flush 将内存表落盘为一个 L0 SSTable，并检查是否触发压实。
func (l *LSM) Flush() error {
	if len(l.mem) == 0 {
		return nil
	}
	entries := dedupeSort(l.mem)
	l.mem = nil
	if _, err := l.addTable(0, entries); err != nil {
		return err
	}
	return l.MaybeCompact()
}

// Get 按 内存表 -> L0(新到旧) -> L1..Ln 的顺序查找 Key。
func (l *LSM) Get(key string) ([]byte, bool) {
	for i := len(l.mem) - 1; i >= 0; i-- {
		if l.mem[i].Key == key {
			if l.mem[i].Op == OpDelete {
				return nil, false
			}
			return l.mem[i].Value, true
		}
	}
	for level := 0; level < len(l.levels); level++ {
		files := l.levels[level]
		// L0 文件互相重叠，新的在前优先；其他层不重叠，顺序无关。
		for i := len(files) - 1; i >= 0; i-- {
			t := files[i]
			if !t.Contains(key) {
				continue
			}
			e, ok, err := lookupInTable(t, key)
			if err != nil {
				return nil, false
			}
			if !ok {
				continue
			}
			if e.Op == OpDelete {
				return nil, false
			}
			return e.Value, true
		}
	}
	return nil, false
}

// AddTable 直接向指定层注入一个已排序的 SSTable（用于初始化/测试铺底数据）。
func (l *LSM) AddTable(level int, entries []Entry) (*SSTable, error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, e := range sorted {
		if e.Seq > l.seq {
			l.seq = e.Seq // 保证全局序列号单调，后续写入的版本一定更新
		}
	}
	return l.addTable(level, sorted)
}

func (l *LSM) addTable(level int, entries []Entry) (*SSTable, error) {
	t, err := writeSSTable(l.opt.Dir, l.nextID, level, entries)
	if err != nil {
		return nil, err
	}
	l.nextID++
	l.levels[level] = append(l.levels[level], t)
	if level > 0 {
		sort.Slice(l.levels[level], func(i, j int) bool {
			return l.levels[level][i].MinKey < l.levels[level][j].MinKey
		})
	}
	return t, nil
}

// Levels 返回当前各层文件的快照（用于观测/测试）。
func (l *LSM) Levels() [][]*SSTable {
	out := make([][]*SSTable, len(l.levels))
	for i, files := range l.levels {
		out[i] = append([]*SSTable(nil), files...)
	}
	return out
}

// DumpTable 读取某个 SSTable 的全部记录（用于观测/测试）。
func DumpTable(t *SSTable) ([]Entry, error) {
	return readSSTable(t.Path)
}

func (l *LSM) levelSize(level int) int64 {
	var size int64
	for _, t := range l.levels[level] {
		size += t.Size
	}
	return size
}

func (l *LSM) levelCapacity(level int) int64 {
	cap_ := l.opt.BaseLevelSize
	for i := 1; i < level; i++ {
		cap_ *= int64(l.opt.SizeMultiplier)
	}
	return cap_
}

// MaybeCompact 循环检查各层，只要某层超过阈值就执行一次压实，直到全部达标。
func (l *LSM) MaybeCompact() error {
	for {
		level := l.pickLevel()
		if level < 0 {
			return nil
		}
		if err := l.compactLevel(level); err != nil {
			return err
		}
	}
}

// pickLevel 返回需要压实的层：L0 看文件个数，其余层看总字节数。
func (l *LSM) pickLevel() int {
	if len(l.levels[0]) >= l.opt.L0CompactionTrigger {
		return 0
	}
	for lv := 1; lv < len(l.levels)-1; lv++ {
		if l.levelSize(lv) > l.levelCapacity(lv) {
			return lv
		}
	}
	return -1
}

// pickInputs 在源层挑选参与压实的文件。
//
//   - L0：文件之间互相重叠，必须全部参与；
//   - 其他层：挑选与下一层重叠 Key 范围最小的 SSTable
//     （以下一层中与本文件有交集的文件数、交集总字节数衡量）。
func (l *LSM) pickInputs(level int) []*SSTable {
	if level == 0 {
		return append([]*SSTable(nil), l.levels[0]...)
	}
	next := l.levels[level+1]
	var best *SSTable
	bestCount, bestBytes := int(^uint(0)>>1), int64(^uint64(0)>>1)
	for _, f := range l.levels[level] {
		count, bytes := overlapScore(f, next)
		if count < bestCount || (count == bestCount && bytes < bestBytes) {
			best, bestCount, bestBytes = f, count, bytes
		}
	}
	if best == nil {
		return nil
	}
	return []*SSTable{best}
}

// overlapScore 统计 f 在下一层中重叠的文件数与重叠文件的总字节数。
func overlapScore(f *SSTable, next []*SSTable) (int, int64) {
	count := 0
	var bytes int64
	for _, n := range next {
		if n.Overlaps(f.MinKey, f.MaxKey) {
			count++
			bytes += n.Size
		}
	}
	return count, bytes
}

// compactLevel 执行一次压实：源层输入 + 下一层有交集的文件 -> 多路归并 -> 写入下一层。
func (l *LSM) compactLevel(level int) error {
	inputs := l.pickInputs(level)
	if len(inputs) == 0 {
		return nil
	}
	target := level + 1

	lo, hi := keyRangeOf(inputs)
	var victims []*SSTable
	for _, f := range l.levels[target] {
		if f.Overlaps(lo, hi) {
			victims = append(victims, f)
		}
	}

	all := append(append([]*SSTable(nil), inputs...), victims...)
	merged, err := kwayMerge(all)
	if err != nil {
		return err
	}
	if l.isBottommost(target) {
		merged = dropTombstones(merged)
	}

	l.removeFiles(level, inputs)
	l.removeFiles(target, victims)

	if len(merged) > 0 {
		if _, err := l.addTable(target, merged); err != nil {
			return err
		}
	}
	for _, f := range all {
		os.Remove(f.Path)
	}
	return nil
}

// isBottommost 判断目标层之下是否已无任何数据（是则可以清理 Tombstone）。
func (l *LSM) isBottommost(level int) bool {
	for lv := level + 1; lv < len(l.levels); lv++ {
		if len(l.levels[lv]) > 0 {
			return false
		}
	}
	return true
}

func (l *LSM) removeFiles(level int, files []*SSTable) {
	gone := make(map[int]bool, len(files))
	for _, f := range files {
		gone[f.ID] = true
	}
	kept := l.levels[level][:0]
	for _, f := range l.levels[level] {
		if !gone[f.ID] {
			kept = append(kept, f)
		}
	}
	l.levels[level] = kept
}

func keyRangeOf(files []*SSTable) (string, string) {
	lo, hi := files[0].MinKey, files[0].MaxKey
	for _, f := range files[1:] {
		if f.MinKey < lo {
			lo = f.MinKey
		}
		if f.MaxKey > hi {
			hi = f.MaxKey
		}
	}
	return lo, hi
}

// dedupeSort 将内存表按 Key 排序，每个 Key 只保留最新版本。
func dedupeSort(mem []Entry) []Entry {
	sort.Slice(mem, func(i, j int) bool {
		if mem[i].Key != mem[j].Key {
			return mem[i].Key < mem[j].Key
		}
		return mem[i].Seq > mem[j].Seq
	})
	out := mem[:0]
	for i, e := range mem {
		if i > 0 && e.Key == mem[i-1].Key {
			continue
		}
		out = append(out, e)
	}
	return out
}

func lookupInTable(t *SSTable, key string) (Entry, bool, error) {
	entries, err := readSSTable(t.Path)
	if err != nil {
		return Entry{}, false, err
	}
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Key >= key })
	if i < len(entries) && entries[i].Key == key {
		return entries[i], true, nil
	}
	return Entry{}, false, nil
}
