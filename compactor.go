package stcs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

// CompactEvent describes one finished compaction and is reported through
// the Compactor.OnCompact hook.
type CompactEvent struct {
	Inputs      []string // input table file names
	InputBytes  int64    // bytes read during the merge
	Output      string   // output table file name
	OutputBytes int64    // bytes written by the merge
	Deduped     uint64   // stale versions dropped
	LiveBefore  int      // live tables before the merge
	LiveAfter   int      // live tables after the merge
}

// ReadAmplification returns InputBytes/OutputBytes for this merge.
func (e CompactEvent) ReadAmplification() float64 {
	if e.OutputBytes == 0 {
		return 0
	}
	return float64(e.InputBytes) / float64(e.OutputBytes)
}

// WriteAmplification returns OutputBytes/InputBytes for this merge, i.e.
// how many bytes were rewritten per byte read (<= 1.0 for a full merge).
func (e CompactEvent) WriteAmplification() float64 {
	if e.InputBytes == 0 {
		return 0
	}
	return float64(e.OutputBytes) / float64(e.InputBytes)
}

// Compactor implements size-tiered compaction: live SSTables are grouped
// into buckets of similar size, and once a bucket accumulates Threshold
// tables they are all merged into a single larger table. The larger output
// naturally lands in a higher tier, so tiers emerge by size.
type Compactor struct {
	dir       string
	threshold int
	ratio     float64

	mu            sync.Mutex
	tables        []*Table
	maxLiveTables int
	nextID        uint64

	// Metrics receives the compaction I/O counters.
	Metrics *Metrics
	// OnCompact, if set, is called after every finished compaction.
	OnCompact func(CompactEvent)
}

// NewCompactor creates a compactor storing tables in dir. threshold is the
// number of similar-sized tables that triggers a merge (N in STCS); ratio
// controls how similar sizes must be to share a tier (e.g. 1.5).
func NewCompactor(dir string, threshold int, ratio float64, metrics *Metrics) *Compactor {
	if threshold < 2 {
		threshold = 2
	}
	if ratio <= 1 {
		ratio = 1.5
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &Compactor{dir: dir, threshold: threshold, ratio: ratio, Metrics: metrics}
}

// NewTablePath reserves a fresh file name for a new SSTable.
func (c *Compactor) NewTablePath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.newTablePathLocked()
}

func (c *Compactor) newTablePathLocked() string {
	p := filepath.Join(c.dir, fmt.Sprintf("table-%06d.sst", c.nextID))
	c.nextID++
	return p
}

// AddTable registers a newly flushed table and runs compactions until no
// size tier holds Threshold or more tables.
func (c *Compactor) AddTable(t *Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tables = append(c.tables, t)
	if len(c.tables) > c.maxLiveTables {
		c.maxLiveTables = len(c.tables)
	}
	for {
		bucket := c.pickBucketLocked()
		if bucket == nil {
			return nil
		}
		if err := c.compactLocked(bucket); err != nil {
			return err
		}
	}
}

// pickBucketLocked returns the first size tier holding >= threshold tables,
// or nil if no tier is ready to compact.
func (c *Compactor) pickBucketLocked() []*Table {
	for _, b := range bucketTables(c.tables, c.ratio) {
		if len(b) >= c.threshold {
			return b
		}
	}
	return nil
}

func (c *Compactor) compactLocked(inputs []*Table) error {
	outPath := c.newTablePathLocked()
	meta, deduped, err := MergeTables(inputs, outPath)
	if err != nil {
		return err
	}
	var inBytes int64
	inNames := make([]string, 0, len(inputs))
	inSet := make(map[*Table]struct{}, len(inputs))
	for _, t := range inputs {
		inBytes += t.Size
		inNames = append(inNames, filepath.Base(t.Path))
		inSet[t] = struct{}{}
	}
	live := c.tables[:0]
	for _, t := range c.tables {
		if _, ok := inSet[t]; !ok {
			live = append(live, t)
		}
	}
	c.tables = append(live, meta)
	for _, t := range inputs {
		os.Remove(t.Path)
	}
	atomic.AddInt64(&c.Metrics.CompactReadBytes, inBytes)
	atomic.AddInt64(&c.Metrics.CompactWriteBytes, meta.Size)
	atomic.AddInt64(&c.Metrics.Compactions, 1)
	atomic.AddInt64(&c.Metrics.DedupedEntries, int64(deduped))
	if c.OnCompact != nil {
		c.OnCompact(CompactEvent{
			Inputs:      inNames,
			InputBytes:  inBytes,
			Output:      filepath.Base(meta.Path),
			OutputBytes: meta.Size,
			Deduped:     deduped,
			LiveBefore:  len(live) + len(inputs),
			LiveAfter:   len(c.tables),
		})
	}
	return nil
}

// Tables returns a snapshot of the live tables.
func (c *Compactor) Tables() []*Table {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Table, len(c.tables))
	copy(out, c.tables)
	return out
}

// TablesNewestFirst returns live tables ordered by MaxSeq descending, so
// the table holding the newest versions comes first.
func (c *Compactor) TablesNewestFirst() []*Table {
	out := c.Tables()
	sort.Slice(out, func(i, j int) bool { return out[i].MaxSeq > out[j].MaxSeq })
	return out
}

// ReadAmplification is the worst-case number of SSTable files a point
// lookup must consult right now (one per live table).
func (c *Compactor) ReadAmplification() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tables)
}

// MaxReadAmplification is the high-water mark of live tables observed.
func (c *Compactor) MaxReadAmplification() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxLiveTables
}

// bucketTables groups tables whose sizes are within ratio of the bucket's
// average size (Cassandra-style size tiers).
func bucketTables(tables []*Table, ratio float64) [][]*Table {
	sorted := make([]*Table, len(tables))
	copy(sorted, tables)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size < sorted[j].Size })
	var buckets [][]*Table
	for _, t := range sorted {
		placed := false
		for bi := range buckets {
			var sum int64
			for _, m := range buckets[bi] {
				sum += m.Size
			}
			avg := float64(sum) / float64(len(buckets[bi]))
			sz := float64(t.Size)
			if sz > avg/ratio && sz < avg*ratio {
				buckets[bi] = append(buckets[bi], t)
				placed = true
				break
			}
		}
		if !placed {
			buckets = append(buckets, []*Table{t})
		}
	}
	return buckets
}
