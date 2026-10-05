package gsb

import (
	"hash/fnv"
	"sort"
	"sync"
)

// ShardOf maps a node ID to its shard using a fixed hash (FNV-1a). The
// mapping is a pure function of (nodeID, numShards), so every node with the
// same configuration agrees on shard ownership.
func ShardOf(nodeID string, numShards int) int {
	h := fnv.New32a()
	h.Write([]byte(nodeID))
	return int(h.Sum32() % uint32(numShards))
}

// Table is the local membership table, indexed by shard. Each shard carries
// a snapshot version that is bumped on every applied change, which is what
// resumption cursors bind to.
type Table struct {
	mu        sync.Mutex
	numShards int
	recs      map[string]Record
	versions  []uint64
}

func NewTable(numShards int) *Table {
	if numShards < 1 {
		numShards = 1
	}
	return &Table{
		numShards: numShards,
		recs:      make(map[string]Record),
		versions:  make([]uint64, numShards),
	}
}

func (t *Table) NumShards() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.numShards
}

// SetNumShards changes the sharding configuration. Records are kept; only
// the shard mapping changes. Versions reset, so peers observe changed
// digests and re-pull the affected ranges.
func (t *Table) SetNumShards(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 1 {
		n = 1
	}
	if n == t.numShards {
		return
	}
	t.numShards = n
	t.versions = make([]uint64, n)
}

// Apply merges records into the table and returns how many strictly
// advanced it. Because the merge is a lattice join, a stale record can
// never overwrite a newer local one.
func (t *Table) Apply(recs ...Record) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := 0
	for _, r := range recs {
		cur, ok := t.recs[r.NodeID]
		if !ok || r.Compare(cur) > 0 {
			t.recs[r.NodeID] = r
			t.versions[ShardOf(r.NodeID, t.numShards)]++
			changed++
		}
	}
	return changed
}

func (t *Table) Get(nodeID string) (Record, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.recs[nodeID]
	return r, ok
}

// Snapshot returns a copy of the full table.
func (t *Table) Snapshot() map[string]Record {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]Record, len(t.recs))
	for k, v := range t.recs {
		out[k] = v
	}
	return out
}

func (t *Table) shardRecordsLocked(shard int) []Record {
	out := make([]Record, 0)
	for _, r := range t.recs {
		if ShardOf(r.NodeID, t.numShards) == shard {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (t *Table) digestLocked(shard int) Digest {
	recs := t.shardRecordsLocked(shard)
	return Digest{
		Shard:     shard,
		NumShards: t.numShards,
		Count:     len(recs),
		Hash:      HashRecords(shard, t.numShards, recs),
		Version:   t.versions[shard],
	}
}

// Digest computes the digest of one shard at the current snapshot.
func (t *Table) Digest(shard int) Digest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.digestLocked(shard)
}

// Digests computes digests for all shards.
func (t *Table) Digests() []Digest {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Digest, t.numShards)
	for s := 0; s < t.numShards; s++ {
		out[s] = t.digestLocked(s)
	}
	return out
}

// Range returns the records of a shard with NodeID strictly greater than
// `after`, in ascending NodeID order, together with the shard snapshot
// version the result belongs to.
func (t *Table) Range(shard int, after string) ([]Record, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.shardRecordsLocked(shard)
	lo := sort.Search(len(all), func(i int) bool { return all[i].NodeID > after })
	out := make([]Record, len(all)-lo)
	copy(out, all[lo:])
	return out, t.versions[shard]
}
