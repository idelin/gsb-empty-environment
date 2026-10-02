package leveled

import (
	"os"
	"sort"
)

// Options 控制分层压缩的行为。
type Options struct {
	// L0CompactionThreshold: L0 文件数达到该值即触发 L0->L1 压缩。
	L0CompactionThreshold int
	// LevelCapacities[i] 是 Level (i+1) 的容量阈值（字节），
	// 超过即触发该层向下一层的压缩。层数 = len(LevelCapacities) + 1。
	LevelCapacities []int64
}

// DefaultOptions 返回类似 RocksDB 默认行为的配置。
func DefaultOptions() Options {
	return Options{
		L0CompactionThreshold: 4,
		LevelCapacities:       []int64{64 << 20, 64 << 20, 64 << 20},
	}
}

// DB 维护各层的 SSTable 清单，并调度分层压缩。
type DB struct {
	dir    string
	opts   Options
	levels [][]*TableMeta
	seq    uint64
	nextID uint64
}

// Open 打开（必要时创建）一个位于 dir 的 DB。
func Open(dir string, opts Options) (*DB, error) {
	if opts.L0CompactionThreshold <= 0 {
		opts.L0CompactionThreshold = 4
	}
	if len(opts.LevelCapacities) == 0 {
		opts.LevelCapacities = DefaultOptions().LevelCapacities
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &DB{
		dir:    dir,
		opts:   opts,
		levels: make([][]*TableMeta, len(opts.LevelCapacities)+1),
		nextID: 1,
	}, nil
}

// NumLevels 返回总层数。
func (db *DB) NumLevels() int { return len(db.levels) }

// Levels 返回各层文件清单的快照（按 Key 范围排序）。
func (db *DB) Levels() [][]TableMeta {
	out := make([][]TableMeta, len(db.levels))
	for l, tables := range db.levels {
		for _, t := range tables {
			out[l] = append(out[l], *t)
		}
	}
	return out
}

// Flush 将一批记录写为一个新的 L0 SSTable，并在需要时触发压缩。
func (db *DB) Flush(entries []Entry) error {
	if err := db.ingest(0, entries); err != nil {
		return err
	}
	return db.MaybeCompact()
}

// Ingest 将一批记录直接写为指定层的 SSTable（用于构建初始层级）。
func (db *DB) Ingest(level int, entries []Entry) error {
	if level <= 0 {
		return db.Flush(entries)
	}
	if err := db.ingest(level, entries); err != nil {
		return err
	}
	return db.MaybeCompact()
}

func (db *DB) ingest(level int, entries []Entry) error {
	if level >= len(db.levels) {
		level = len(db.levels) - 1
	}
	tw, err := createTable(db.dir, db.nextID, level)
	if err != nil {
		return err
	}
	db.nextID++
	db.seq++
	seq := db.seq
	for _, e := range entries {
		e.Seq = seq
		if err := tw.add(e); err != nil {
			return err
		}
	}
	meta, err := tw.finish()
	if err != nil {
		return err
	}
	if meta == nil {
		return nil
	}
	db.addTable(meta)
	return nil
}

func (db *DB) addTable(meta *TableMeta) {
	lv := db.levels[meta.Level]
	i := sort.Search(len(lv), func(i int) bool { return lv[i].MinKey > meta.MinKey })
	lv = append(lv, nil)
	copy(lv[i+1:], lv[i:])
	lv[i] = meta
	db.levels[meta.Level] = lv
}

// Get 查找 key 的最新可见值；第二个返回值表示是否存在（被删除返回 false）。
func (db *DB) Get(key string) ([]byte, bool, error) {
	// L0 文件相互重叠，按 ID 倒序（新文件优先）逐个找。
	l0 := db.levels[0]
	for i := len(l0) - 1; i >= 0; i-- {
		if !l0[i].contains(key) {
			continue
		}
		e, found, err := tableGet(l0[i].Path, key)
		if err != nil {
			return nil, false, err
		}
		if found {
			if e.Tombstone {
				return nil, false, nil
			}
			return e.Value, true, nil
		}
	}
	// L1+ 层内不重叠，至多一个文件命中。
	for l := 1; l < len(db.levels); l++ {
		for _, t := range db.levels[l] {
			if !t.contains(key) {
				continue
			}
			e, found, err := tableGet(t.Path, key)
			if err != nil {
				return nil, false, err
			}
			if found {
				if e.Tombstone {
					return nil, false, nil
				}
				return e.Value, true, nil
			}
		}
	}
	return nil, false, nil
}
