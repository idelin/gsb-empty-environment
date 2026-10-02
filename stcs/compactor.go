package stcs

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Stats tracks cumulative I/O counters used to derive read/write
// amplification. All values are in bytes.
type Stats struct {
	// LogicalBytes is the payload (key+value) written by the user.
	LogicalBytes uint64
	// FlushBytes is the physical bytes written by memtable flushes.
	FlushBytes uint64
	// CompactionReadBytes is physical bytes read by compactions.
	CompactionReadBytes uint64
	// CompactionWriteBytes is physical bytes written by compactions.
	CompactionWriteBytes uint64
	// Compactions is the number of merge rounds executed.
	Compactions int
}

// WriteAmplification returns total physical bytes written divided by
// logical bytes written. For STCS merging N same-size tables once per
// tier, the theoretical bound is 1 (flush) + 1 (one rewrite per tier
// the data passes through). After a single tier-0 compaction the
// control line is therefore WA <= 2.0.
func (s Stats) WriteAmplification() float64 {
	if s.LogicalBytes == 0 {
		return 0
	}
	return float64(s.FlushBytes+s.CompactionWriteBytes) / float64(s.LogicalBytes)
}

// ReadAmplification returns physical bytes read by compaction divided
// by logical bytes written (write workload's read cost). After one
// merge of 4 same-size tables the expected value is ~1.0.
func (s Stats) ReadAmplification() float64 {
	if s.LogicalBytes == 0 {
		return 0
	}
	return float64(s.CompactionReadBytes) / float64(s.LogicalBytes)
}

// Options configures a Compactor.
type Options struct {
	// Fanout is N: how many similar-sized SSTables accumulate in one
	// tier before a merge is triggered. Default 4.
	Fanout int
	// SizeSimilarity is the max allowed ratio between the largest and
	// smallest table in a merge candidate set. Default 1.5.
	SizeSimilarity float64
}

// Compactor is a size-tiered compaction optimizer. It owns a directory
// of SSTables organized in tiers.
type Compactor struct {
	dir   string
	opts  Options
	tiers map[int][]*SSTable
	seq   uint64
	next  int
	Stats Stats
}

// NewCompactor creates a Compactor storing SSTables under dir.
func NewCompactor(dir string, opts Options) (*Compactor, error) {
	if opts.Fanout <= 0 {
		opts.Fanout = 4
	}
	if opts.SizeSimilarity <= 0 {
		opts.SizeSimilarity = 1.5
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Compactor{dir: dir, opts: opts, tiers: make(map[int][]*SSTable)}, nil
}

// Write ingests a batch of user mutations: it assigns sequence numbers,
// accounts logical bytes, and flushes the batch as a new tier-0 SSTable,
// then runs any triggered compactions.
func (c *Compactor) Write(entries []Entry) (*SSTable, error) {
	for i := range entries {
		c.seq++
		entries[i].Seq = c.seq
		c.Stats.LogicalBytes += uint64(len(entries[i].Key) + len(entries[i].Value))
	}
	return c.flush(entries)
}

// flush writes entries as a new SSTable in tier 0 and compacts as needed.
func (c *Compactor) flush(entries []Entry) (*SSTable, error) {
	path := c.tablePath(0)
	tbl, err := WriteSSTable(path, 0, entries)
	if err != nil {
		return nil, err
	}
	c.Stats.FlushBytes += uint64(tbl.Size)
	c.tiers[0] = append(c.tiers[0], tbl)
	if err := c.maybeCompact(); err != nil {
		return nil, err
	}
	return tbl, nil
}

func (c *Compactor) tablePath(tier int) string {
	name := fmt.Sprintf("sst-%06d-L%d.sst", c.next, tier)
	c.next++
	return filepath.Join(c.dir, name)
}

// maybeCompact repeatedly merges tiers that have accumulated Fanout
// similar-sized tables, cascading into higher tiers as needed.
func (c *Compactor) maybeCompact() error {
	for tier := 0; ; tier++ {
		tables := c.tiers[tier]
		if len(tables) < c.opts.Fanout {
			return nil
		}
		batch := pickSimilar(tables, c.opts.Fanout, c.opts.SizeSimilarity)
		if batch == nil {
			return nil
		}
		if err := c.compact(tier, batch); err != nil {
			return err
		}
	}
}

// pickSimilar returns the oldest fanout tables whose sizes are mutually
// similar (max <= similarity*min), or nil if no such set exists.
func pickSimilar(tables []*SSTable, fanout int, similarity float64) []*SSTable {
	sorted := make([]*SSTable, len(tables))
	copy(sorted, tables)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size < sorted[j].Size })
	for i := 0; i+fanout <= len(sorted); i++ {
		window := sorted[i : i+fanout]
		if float64(window[fanout-1].Size) <= similarity*float64(window[0].Size) {
			return window
		}
	}
	return nil
}

// compact merges batch (all in tier) into one SSTable in tier+1,
// keeping only the newest version of each key and dropping tombstones.
func (c *Compactor) compact(tier int, batch []*SSTable) error {
	var readBytes uint64
	for _, t := range batch {
		readBytes += uint64(t.Size)
	}

	out, outEntries, err := c.mergeTables(tier+1, batch)
	if err != nil {
		return err
	}
	_ = outEntries

	// Remove inputs from the tier and delete their files.
	inBatch := make(map[*SSTable]bool, len(batch))
	for _, t := range batch {
		inBatch[t] = true
	}
	remaining := c.tiers[tier][:0]
	for _, t := range c.tiers[tier] {
		if !inBatch[t] {
			remaining = append(remaining, t)
		}
	}
	c.tiers[tier] = remaining
	for _, t := range batch {
		if err := os.Remove(t.Path); err != nil {
			return err
		}
	}
	c.tiers[tier+1] = append(c.tiers[tier+1], out)

	c.Stats.CompactionReadBytes += readBytes
	c.Stats.CompactionWriteBytes += uint64(out.Size)
	c.Stats.Compactions++
	return nil
}

// mergeTables performs a k-way merge over the input tables and writes
// the result as one SSTable in outTier.
func (c *Compactor) mergeTables(outTier int, tables []*SSTable) (*SSTable, int, error) {
	h := &mergeHeap{}
	iters := make([]*Iterator, 0, len(tables))
	for _, t := range tables {
		it, err := OpenIterator(t.Path)
		if err != nil {
			return nil, 0, err
		}
		iters = append(iters, it)
		if it.Next() {
			heap.Push(h, mergeItem{entry: it.Entry(), src: it})
		}
	}
	defer func() {
		for _, it := range iters {
			it.Close()
		}
	}()

	var merged []Entry
	var lastKey []byte
	haveLast := false
	for h.Len() > 0 {
		item := heap.Pop(h).(mergeItem)
		e := item.entry
		if item.src.Next() {
			heap.Push(h, mergeItem{entry: item.src.Entry(), src: item.src})
		}
		// Tables are disjoint-sorted; equal keys surface newest-first
		// because the heap orders by (key, seq desc). Keep the first
		// occurrence of each key and skip older versions.
		if haveLast && compareBytes(e.Key, lastKey) == 0 {
			continue
		}
		lastKey = append(lastKey[:0], e.Key...)
		haveLast = true
		if e.Tombstone {
			continue // dropped: no older tiers below the output
		}
		merged = append(merged, e)
	}
	for _, it := range iters {
		if it.Err() != nil {
			return nil, 0, it.Err()
		}
	}

	out, err := WriteSSTable(c.tablePath(outTier), outTier, merged)
	if err != nil {
		return nil, 0, err
	}
	return out, len(merged), nil
}

// Tables returns the live SSTables of a tier (defensive copy).
func (c *Compactor) Tables(tier int) []*SSTable {
	out := make([]*SSTable, len(c.tiers[tier]))
	copy(out, c.tiers[tier])
	return out
}

// Tiers returns the sorted list of non-empty tier numbers.
func (c *Compactor) Tiers() []int {
	var ts []int
	for t, tables := range c.tiers {
		if len(tables) > 0 {
			ts = append(ts, t)
		}
	}
	sort.Ints(ts)
	return ts
}

// mergeItem is one heap element: the current entry of one input table.
type mergeItem struct {
	entry Entry
	src   *Iterator
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	c := compareBytes(h[i].entry.Key, h[j].entry.Key)
	if c != 0 {
		return c < 0
	}
	return h[i].entry.Seq > h[j].entry.Seq // newest version first
}
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(mergeItem)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
