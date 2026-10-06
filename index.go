// Package kvindex implements a versioned key-value index that accepts point
// writes and half-open range deletes (tombstones). Visibility is decided by
// monotonically assigned sequence numbers: for any key, the affecting
// operation with the highest sequence number not exceeding the read snapshot
// wins, so a newer point write resurrects a key covered by an older
// tombstone.
package kvindex

import (
	"errors"
	"fmt"
	"sort"
)

// OpKind identifies the kind of a mutation.
type OpKind int

const (
	// OpPut is a point write of Key/Value.
	OpPut OpKind = iota
	// OpDeleteRange is a tombstone over the half-open interval [Start, End).
	OpDeleteRange
)

// Op is a single mutation applied to the index.
type Op struct {
	Kind  OpKind
	Seq   uint64
	Key   string // OpPut
	Value string // OpPut
	Start string // OpDeleteRange, inclusive
	End   string // OpDeleteRange, exclusive
}

// ErrSeqConflict is returned when a sequence number is reused with an
// operation that differs from the one originally recorded for it.
var ErrSeqConflict = errors.New("kvindex: sequence number reused with a different operation")

// KV is one visible key/value pair returned by a scan.
type KV struct {
	Key   string
	Value string
}

// Page is the result of a paginated scan. When Done is false, ResumeAfter
// holds the last returned key; pass it to ScanAfter to continue.
type Page struct {
	Items       []KV
	ResumeAfter string
	Done        bool
}

type pointVersion struct {
	seq   uint64
	value string
}

type tombstone struct {
	start string
	end   string
	seq   uint64
}

// Index stores versioned point writes and range tombstones.
type Index struct {
	points     map[string][]pointVersion // per key, sorted by seq ascending
	tombstones []tombstone               // sorted by seq ascending
	keys       []string                  // sorted unique keys that have point writes
	seqs       map[uint64]Op             // seq -> canonical op, for conflict detection
}

// New returns an empty index.
func New() *Index {
	return &Index{
		points: make(map[string][]pointVersion),
		seqs:   make(map[uint64]Op),
	}
}

func opsEqual(a, b Op) bool {
	return a == b
}

// Apply records a mutation. Re-applying an identical operation with the same
// sequence number is an idempotent no-op; reusing the sequence number with a
// different operation fails with ErrSeqConflict.
func (ix *Index) Apply(op Op) error {
	switch op.Kind {
	case OpPut:
	case OpDeleteRange:
		if op.Start >= op.End {
			return fmt.Errorf("kvindex: invalid delete range [%q, %q)", op.Start, op.End)
		}
	default:
		return fmt.Errorf("kvindex: unknown op kind %d", op.Kind)
	}

	if prev, ok := ix.seqs[op.Seq]; ok {
		if opsEqual(prev, op) {
			return nil
		}
		return ErrSeqConflict
	}
	ix.seqs[op.Seq] = op

	switch op.Kind {
	case OpPut:
		versions := ix.points[op.Key]
		if versions == nil {
			pos := sort.SearchStrings(ix.keys, op.Key)
			ix.keys = append(ix.keys, "")
			copy(ix.keys[pos+1:], ix.keys[pos:])
			ix.keys[pos] = op.Key
		}
		pos := sort.Search(len(versions), func(i int) bool { return versions[i].seq > op.Seq })
		versions = append(versions, pointVersion{})
		copy(versions[pos+1:], versions[pos:])
		versions[pos] = pointVersion{seq: op.Seq, value: op.Value}
		ix.points[op.Key] = versions
	case OpDeleteRange:
		pos := sort.Search(len(ix.tombstones), func(i int) bool { return ix.tombstones[i].seq > op.Seq })
		ix.tombstones = append(ix.tombstones, tombstone{})
		copy(ix.tombstones[pos+1:], ix.tombstones[pos:])
		ix.tombstones[pos] = tombstone{start: op.Start, end: op.End, seq: op.Seq}
	}
	return nil
}

// latestPoint returns the newest point version of key with seq <= snapshot.
func (ix *Index) latestPoint(key string, snapshot uint64) (pointVersion, bool) {
	versions := ix.points[key]
	pos := sort.Search(len(versions), func(i int) bool { return versions[i].seq > snapshot })
	if pos == 0 {
		return pointVersion{}, false
	}
	return versions[pos-1], true
}

// latestTombstone returns the highest tombstone seq <= snapshot covering key,
// and whether any such tombstone exists.
func (ix *Index) latestTombstone(key string, snapshot uint64) (uint64, bool) {
	// Tombstones are sorted by seq, so the last covering entry within the
	// seq <= snapshot prefix has the highest seq.
	end := sort.Search(len(ix.tombstones), func(i int) bool { return ix.tombstones[i].seq > snapshot })
	for i := end - 1; i >= 0; i-- {
		t := ix.tombstones[i]
		if t.start <= key && key < t.end {
			return t.seq, true
		}
	}
	return 0, false
}

// visibleAt reports whether key is visible at snapshot and its value.
func (ix *Index) visibleAt(key string, snapshot uint64) (string, bool) {
	point, hasPoint := ix.latestPoint(key, snapshot)
	if !hasPoint {
		return "", false
	}
	tseq, hasTomb := ix.latestTombstone(key, snapshot)
	if hasTomb && tseq >= point.seq {
		return "", false
	}
	return point.value, true
}

// GetAt returns the value of key visible at the given snapshot sequence.
func (ix *Index) GetAt(key string, snapshot uint64) (string, bool) {
	return ix.visibleAt(key, snapshot)
}

// scan collects up to limit visible keys in [start, end) at snapshot,
// starting strictly after the key at index lo.
func (ix *Index) scan(lo int, end string, snapshot uint64, limit int) Page {
	page := Page{Done: true}
	for i := lo; i < len(ix.keys); i++ {
		key := ix.keys[i]
		if key >= end {
			break
		}
		value, ok := ix.visibleAt(key, snapshot)
		if !ok {
			continue
		}
		if limit > 0 && len(page.Items) == limit {
			page.ResumeAfter = page.Items[len(page.Items)-1].Key
			page.Done = false
			return page
		}
		page.Items = append(page.Items, KV{Key: key, Value: value})
	}
	return page
}

// ScanAt returns up to limit visible keys in the half-open interval
// [start, end) at snapshot. A limit <= 0 means no limit.
func (ix *Index) ScanAt(start, end string, snapshot uint64, limit int) Page {
	lo := sort.SearchStrings(ix.keys, start)
	return ix.scan(lo, end, snapshot, limit)
}

// ScanAfter is like ScanAt but starts strictly after the key after, which is
// typically the ResumeAfter token of a previous page.
func (ix *Index) ScanAfter(after, end string, snapshot uint64, limit int) Page {
	lo := sort.SearchStrings(ix.keys, after)
	if lo < len(ix.keys) && ix.keys[lo] == after {
		lo++
	}
	return ix.scan(lo, end, snapshot, limit)
}
