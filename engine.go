package stcs

import (
	"bytes"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

// Options configures an Engine.
type Options struct {
	// MemtableLimit is the number of bytes buffered in memory before a
	// flush to an SSTable (default 10 MiB).
	MemtableLimit int64
	// TierThreshold is N in STCS: how many similar-sized tables trigger a
	// merge (default 4).
	TierThreshold int
	// BucketRatio controls size similarity within a tier (default 1.5).
	BucketRatio float64
}

// Engine is a minimal LSM write path optimized for high-throughput
// writes: puts go to an in-memory table, full memtables flush to SSTables,
// and a size-tiered compactor merges them in the background of the write
// path.
type Engine struct {
	opts    Options
	mu      sync.Mutex
	mem     map[string]Entry
	memSize int64
	seq     uint64
	comp    *Compactor
	metrics *Metrics
}

// Open creates an engine storing its SSTables in dir.
func Open(dir string, opts Options) (*Engine, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if opts.MemtableLimit <= 0 {
		opts.MemtableLimit = 10 << 20
	}
	m := &Metrics{}
	return &Engine{
		opts:    opts,
		mem:     make(map[string]Entry),
		comp:    NewCompactor(dir, opts.TierThreshold, opts.BucketRatio, m),
		metrics: m,
	}, nil
}

// Compactor exposes the engine's compactor (e.g. to install OnCompact).
func (e *Engine) Compactor() *Compactor { return e.comp }

// Put inserts or updates a key, flushing the memtable once it is full.
func (e *Engine) Put(key, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	ent := Entry{
		Key:   append([]byte(nil), key...),
		Value: append([]byte(nil), value...),
		Seq:   e.seq,
	}
	if old, ok := e.mem[string(key)]; ok {
		e.memSize -= int64(len(old.Key) + len(old.Value))
	}
	e.mem[string(key)] = ent
	e.memSize += int64(len(ent.Key) + len(ent.Value))
	atomic.AddInt64(&e.metrics.UserBytes, int64(len(key)+len(value)))
	if e.memSize >= e.opts.MemtableLimit {
		return e.flushLocked()
	}
	return nil
}

// Flush forces the current memtable to disk.
func (e *Engine) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.flushLocked()
}

func (e *Engine) flushLocked() error {
	if len(e.mem) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e.mem))
	for k := range e.mem {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w, err := NewTableWriter(e.comp.NewTablePath())
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := w.Add(e.mem[k]); err != nil {
			return err
		}
	}
	meta, err := w.Finish()
	if err != nil {
		return err
	}
	atomic.AddInt64(&e.metrics.FlushBytes, meta.Size)
	atomic.AddInt64(&e.metrics.FlushCount, 1)
	e.mem = make(map[string]Entry)
	e.memSize = 0
	return e.comp.AddTable(meta)
}

// Get returns the newest visible version of key.
func (e *Engine) Get(key []byte) ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ent, ok := e.mem[string(key)]; ok {
		return append([]byte(nil), ent.Value...), true
	}
	for _, t := range e.comp.TablesNewestFirst() {
		if bytes.Compare(key, t.FirstKey) < 0 || bytes.Compare(key, t.LastKey) > 0 {
			continue
		}
		it, err := t.Iterator()
		if err != nil {
			return nil, false
		}
		for it.Next() {
			if ent := it.Entry(); bytes.Equal(ent.Key, key) {
				v := append([]byte(nil), ent.Value...)
				it.Close()
				return v, true
			}
		}
		it.Close()
	}
	return nil, false
}

// Stats is a point-in-time snapshot of engine metrics.
type Stats struct {
	UserBytes         int64
	FlushBytes        int64
	FlushCount        int64
	CompactReadBytes  int64
	CompactWriteBytes int64
	Compactions       int64
	DedupedEntries    int64
	// LiveTables is the current point-lookup read amplification.
	LiveTables int
	// MaxLiveTables is the high-water mark of the read amplification.
	MaxLiveTables      int
	WriteAmplification float64
	CompactionReadAmp  float64
}

// Stats returns a snapshot of the engine's counters and amplification
// ratios.
func (e *Engine) Stats() Stats {
	user, flush, flushN, cr, cw, comp, dedup := e.metrics.snapshot()
	return Stats{
		UserBytes:          user,
		FlushBytes:         flush,
		FlushCount:         flushN,
		CompactReadBytes:   cr,
		CompactWriteBytes:  cw,
		Compactions:        comp,
		DedupedEntries:     dedup,
		LiveTables:         e.comp.ReadAmplification(),
		MaxLiveTables:      e.comp.MaxReadAmplification(),
		WriteAmplification: e.metrics.WriteAmplification(),
		CompactionReadAmp:  e.metrics.CompactionReadAmplification(),
	}
}
