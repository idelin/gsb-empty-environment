package rebuild

import "sync"

// entry 是索引内部条目；deleted 为墓碑，防止乱序的旧事件在删除后复活该键。
type entry struct {
	rec     Record
	deleted bool
}

// Index 是源数据的服务副本。按每键版本应用事件：只有更高版本才会覆盖，
// 因此重发或乱序的事件会被消重，最终结果不变。
type Index struct {
	mu        sync.RWMutex
	name      string
	data      map[string]entry
	watermark uint64
	applied   uint64
	skipped   uint64
}

func NewIndex(name string) *Index {
	return &Index{name: name, data: make(map[string]entry)}
}

func (idx *Index) Name() string { return idx.name }

// Apply 应用一条事件；事件是重发或乱序（版本不高于该键当前版本）时返回 false。
func (idx *Index) Apply(ev Event) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if cur, ok := idx.data[ev.Key]; ok && cur.rec.Version >= ev.Seq {
		idx.skipped++
		return false
	}
	if ev.Deleted {
		idx.data[ev.Key] = entry{rec: Record{Key: ev.Key, Version: ev.Seq}, deleted: true}
	} else {
		idx.data[ev.Key] = entry{rec: Record{Key: ev.Key, Value: ev.Value, Version: ev.Seq}}
	}
	if ev.Seq > idx.watermark {
		idx.watermark = ev.Seq
	}
	idx.applied++
	return true
}

// LoadSnapshot 批量装载一致性快照，并把水位设为快照（冻结）版本。
func (idx *Index) LoadSnapshot(data map[string]Record, version uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.data = make(map[string]entry, len(data))
	for k, v := range data {
		idx.data[k] = entry{rec: v}
	}
	idx.watermark = version
}

func (idx *Index) Get(key string) (Record, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	e, ok := idx.data[key]
	if !ok || e.deleted {
		return Record{}, false
	}
	return e.rec, true
}

// Scan 返回全部存活记录。
func (idx *Index) Scan() map[string]Record {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make(map[string]Record, len(idx.data))
	for k, e := range idx.data {
		if !e.deleted {
			out[k] = e.rec
		}
	}
	return out
}

// keyVersion 返回某键已存版本（含墓碑），用于确认变更是否已被处理。
func (idx *Index) keyVersion(key string) uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if e, ok := idx.data[key]; ok {
		return e.rec.Version
	}
	return 0
}

// Watermark 是索引见过的最高事件版本。
func (idx *Index) Watermark() uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.watermark
}

// Stats 返回实际应用与因消重跳过的事件数。
func (idx *Index) Stats() (applied, skipped uint64) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.applied, idx.skipped
}
