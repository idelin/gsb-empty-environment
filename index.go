package disclosure

import (
	"fmt"
	"sort"
)

// IndexKey is the immutable ordering key of an effective record. Records are
// ordered by tenant, event start, event end, and the original root sequence.
// RootSeq is the final tie-breaker, so records at the same instant remain
// distinct and orderable.
type IndexKey struct {
	Tenant  string `json:"tenant"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	RootSeq uint64 `json:"root_seq"`
}

// Less reports whether k sorts before other.
func (k IndexKey) Less(other IndexKey) bool {
	if k.Tenant != other.Tenant {
		return k.Tenant < other.Tenant
	}
	if k.Start != other.Start {
		return k.Start < other.Start
	}
	if k.End != other.End {
		return k.End < other.End
	}
	return k.RootSeq < other.RootSeq
}

// IndexEntry commits to one effective record at a checkpoint. VersionSeq and
// LeafDigest identify the latest version, while RootSeq remains stable across
// tenant/time corrections. TombstoneSeq/TombstoneDigest identify the
// authorizing erasure marker when Erased is true.
type IndexEntry struct {
	Key             IndexKey `json:"key"`
	VersionSeq      uint64   `json:"version_seq"`
	LeafDigest      Hash     `json:"leaf_digest"`
	Erased          bool     `json:"erased"`
	TombstoneSeq    uint64   `json:"tombstone_seq,omitempty"`
	TombstoneDigest Hash     `json:"tombstone_digest,omitempty"`
}

func (e IndexEntry) digest(pos uint64) Hash {
	var enc encoder
	enc.str("audit-index-entry/v1")
	enc.u64(pos)
	enc.str(e.Key.Tenant)
	enc.i64(e.Key.Start)
	enc.i64(e.Key.End)
	enc.u64(e.Key.RootSeq)
	enc.u64(e.VersionSeq)
	enc.hash(e.LeafDigest)
	enc.bool(e.Erased)
	if e.Erased {
		enc.u64(e.TombstoneSeq)
		enc.hash(e.TombstoneDigest)
	}
	return HashBytes(enc.buf)
}

func buildIndex(view *logView) ([]IndexEntry, error) {
	var entries []IndexEntry
	for _, root := range view.leaves {
		if root.Kind != KindEvent {
			continue
		}
		chain := []Leaf{root}
		cur := root
		for {
			next, exists := view.correctionNext[cur.Seq]
			if !exists {
				break
			}
			leaf, ok := view.bySeq[next]
			if !ok {
				return nil, fmt.Errorf("%w: correction seq %d", ErrMissingLeaf, next)
			}
			chain = append(chain, leaf)
			cur = leaf
		}
		latest := chain[len(chain)-1]
		tombSeq, tombDigest, erased := uint64(0), Hash{}, false
		for _, node := range chain {
			for _, seq := range view.tombstones[node.Seq] {
				tomb := view.bySeq[seq]
				if !erased || tomb.Seq > tombSeq {
					tombSeq = tomb.Seq
					tombDigest = tomb.Digest()
					erased = true
				}
			}
		}
		entries = append(entries, IndexEntry{
			Key: IndexKey{
				Tenant:  latest.Tenant,
				Start:   latest.EventStart,
				End:     latest.EventEnd,
				RootSeq: root.Seq,
			},
			VersionSeq:      latest.Seq,
			LeafDigest:      latest.Digest(),
			Erased:          erased,
			TombstoneSeq:    tombSeq,
			TombstoneDigest: tombDigest,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key.Less(entries[j].Key) })
	for i := 1; i < len(entries); i++ {
		if !entries[i-1].Key.Less(entries[i].Key) {
			return nil, fmt.Errorf("%w: duplicate effective index key", ErrInvalidLeaf)
		}
	}
	return entries, nil
}

func indexHashes(entries []IndexEntry) []Hash {
	out := make([]Hash, len(entries))
	for i := range entries {
		out[i] = entries[i].digest(uint64(i))
	}
	return out
}

// BuildIndex reconstructs the effective-record index from a complete log.
// This is primarily useful to independently audit a server's checkpoint;
// selective-disclosure verification itself relies on the signed index root.
func BuildIndex(leaves []Leaf) ([]IndexEntry, error) {
	view, err := prepareLog(leaves)
	if err != nil {
		return nil, err
	}
	return buildIndex(view)
}

// IndexKeyForQuery returns the tightest lower and upper keys that could contain
// an effective record intersecting q. The lower key starts at the tenant's
// minimum time so old intervals are not excluded; the upper key ends at q.To.
func IndexKeyForQuery(q Query) (lower, upper IndexKey, err error) {
	if err := q.Validate(); err != nil {
		return IndexKey{}, IndexKey{}, err
	}
	lower = IndexKey{Tenant: q.Tenant, Start: minInt64, End: minInt64, RootSeq: 0}
	upper = IndexKey{Tenant: q.Tenant, Start: q.To, End: maxInt64, RootSeq: maxUint64}
	return lower, upper, nil
}
