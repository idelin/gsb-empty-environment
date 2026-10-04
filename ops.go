package bitmap

import (
	"sort"
	"sync/atomic"
)

// Batch boolean set operations: atomic and linearizable across all
// operands. Two-tier conflict arbitration:
//
//  1. Optimistic fast path (seqlock-style): read each operand's shard
//     occupancy mask, read-lock ONLY the shards that may hold containers,
//     compute, then validate that no operand's version changed during the
//     window. If no mutation occurred, every shard was constant for the
//     whole window, so the result is consistent with a single point in
//     time -- linearizable. Small/sparse bitmaps touch only a handful of
//     shard locks here, which is what makes batch ops microsecond-scale.
//  2. Pessimistic fallback: if validation keeps failing under write
//     contention, take read locks on ALL shards of ALL operands in the
//     single global order (bitmap id, then shard index). Deadlock-free by
//     construction, and always terminates because writers hold at most
//     one lock and never block on other locks.
//
// Results are freshly allocated; operands are never mutated.

const maxOptimisticAttempts = 4

// ---------------------------------------------------------------------------
// snapshot machinery
// ---------------------------------------------------------------------------

func loadMask(b *Bitmap) [4]uint64 {
	var m [4]uint64
	for w := 0; w < 4; w++ {
		m[w] = atomic.LoadUint64(&b.mask[w])
	}
	return m
}

func maskBit(m *[4]uint64, i int) bool {
	return m[i>>6]>>(uint(i)&63)&1 == 1
}

// dedupSort returns the distinct bitmaps sorted by id (global lock order).
func dedupSort(bs []*Bitmap) []*Bitmap {
	out := make([]*Bitmap, 0, len(bs))
	seen := make(map[uint64]struct{}, len(bs))
	for _, b := range bs {
		if _, dup := seen[b.id]; !dup {
			seen[b.id] = struct{}{}
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// snapshotLock read-locks, for every shard index in ascending order, the
// shards of every bitmap whose occupancy mask has that shard's bit set
// (bitmaps ordered by id). Returns the matching unlock function.
func snapshotLock(bs []*Bitmap, masks [][4]uint64) func() {
	for i := 0; i < numShards; i++ {
		for k, b := range bs {
			if maskBit(&masks[k], i) {
				b.shards[i].mu.RLock()
			}
		}
	}
	return func() {
		for i := numShards - 1; i >= 0; i-- {
			for k := len(bs) - 1; k >= 0; k-- {
				if maskBit(&masks[k], i) {
					bs[k].shards[i].mu.RUnlock()
				}
			}
		}
	}
}

func loadVersions(bs []*Bitmap) []uint64 {
	v := make([]uint64, len(bs))
	for k, b := range bs {
		v[k] = b.Version()
	}
	return v
}

func versionsUnchanged(bs []*Bitmap, v []uint64) bool {
	for k, b := range bs {
		if b.Version() != v[k] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// pair operations
// ---------------------------------------------------------------------------

// And returns the intersection of a and b.
func And(a, b *Bitmap) *Bitmap {
	if a == b {
		return cloneOf(a)
	}
	return combinePair(a, b, and, true)
}

// Or returns the union of a and b.
func Or(a, b *Bitmap) *Bitmap {
	if a == b {
		return cloneOf(a)
	}
	return combinePair(a, b, or, false)
}

// Xor returns the symmetric difference of a and b.
func Xor(a, b *Bitmap) *Bitmap {
	if a == b {
		return New()
	}
	return combinePair(a, b, xor, false)
}

// combinePair implements And/Or/Xor. intersect=true prunes shards not
// occupied in both operands.
func combinePair(a, b *Bitmap, op func(x, y container) container, intersect bool) *Bitmap {
	bs := dedupSort([]*Bitmap{a, b})
	for attempt := 0; attempt < maxOptimisticAttempts; attempt++ {
		versions := loadVersions(bs)
		masks := [][4]uint64{loadMask(bs[0]), loadMask(bs[1])}
		unlock := snapshotLock(bs, masks)
		res := New()
		for i := 0; i < numShards; i++ {
			inA := maskBit(&masks[0], i)
			inB := maskBit(&masks[1], i)
			if intersect && !(inA && inB) {
				continue
			}
			if !inA && !inB {
				continue
			}
			sa, sb := bs[0].shards[i], bs[1].shards[i]
			switch {
			case inA && inB:
				for hi, c1 := range sa.containers {
					if c2, ok := sb.containers[hi]; ok {
						if rc := op(c1, c2); rc.cardinality() > 0 {
							res.putContainer(i, hi, rc)
						}
					} else if !intersect {
						res.putContainer(i, hi, c1.clone())
					}
				}
				if !intersect {
					for hi, c2 := range sb.containers {
						if _, ok := sa.containers[hi]; !ok {
							res.putContainer(i, hi, c2.clone())
						}
					}
				}
			case inA:
				if !intersect {
					for hi, c := range sa.containers {
						res.putContainer(i, hi, c.clone())
					}
				}
			case inB:
				if !intersect {
					for hi, c := range sb.containers {
						res.putContainer(i, hi, c.clone())
					}
				}
			}
		}
		valid := versionsUnchanged(bs, versions)
		unlock()
		if valid {
			return res
		}
	}
	// Pessimistic fallback.
	unlock := lockBitmaps(bs[0], bs[1])
	defer unlock()
	return combinePairLocked(bs[0], bs[1], op, intersect)
}

func combinePairLocked(a, b *Bitmap, op func(x, y container) container, intersect bool) *Bitmap {
	res := New()
	for i := 0; i < numShards; i++ {
		sa, sb := a.shards[i], b.shards[i]
		for hi, c1 := range sa.containers {
			if c2, ok := sb.containers[hi]; ok {
				if rc := op(c1, c2); rc.cardinality() > 0 {
					res.putContainer(i, hi, rc)
				}
			} else if !intersect {
				res.putContainer(i, hi, c1.clone())
			}
		}
		if !intersect {
			for hi, c2 := range sb.containers {
				if _, ok := sa.containers[hi]; !ok {
					res.putContainer(i, hi, c2.clone())
				}
			}
		}
	}
	return res
}

// cloneOf returns a consistent deep copy of b.
func cloneOf(b *Bitmap) *Bitmap {
	for attempt := 0; attempt < maxOptimisticAttempts; attempt++ {
		v := b.Version()
		mask := loadMask(b)
		unlock := snapshotLock([]*Bitmap{b}, [][4]uint64{mask})
		res := New()
		for i := 0; i < numShards; i++ {
			if !maskBit(&mask, i) {
				continue
			}
			for hi, c := range b.shards[i].containers {
				res.putContainer(i, hi, c.clone())
			}
		}
		valid := v == b.Version()
		unlock()
		if valid {
			return res
		}
	}
	unlock := lockBitmaps(b)
	defer unlock()
	res := New()
	for i := 0; i < numShards; i++ {
		for hi, c := range b.shards[i].containers {
			res.putContainer(i, hi, c.clone())
		}
	}
	return res
}

// ---------------------------------------------------------------------------
// n-ary operations
// ---------------------------------------------------------------------------

// AndMany returns the intersection of all given bitmaps.
func AndMany(bs ...*Bitmap) *Bitmap {
	return combineMany(true, bs...)
}

// OrMany returns the union of all given bitmaps.
func OrMany(bs ...*Bitmap) *Bitmap {
	return combineMany(false, bs...)
}

func combineMany(intersect bool, operands ...*Bitmap) *Bitmap {
	bs := dedupSort(operands)
	switch len(bs) {
	case 0:
		return New()
	case 1:
		return cloneOf(bs[0])
	}
	for attempt := 0; attempt < maxOptimisticAttempts; attempt++ {
		versions := loadVersions(bs)
		masks := make([][4]uint64, len(bs))
		for k := range bs {
			masks[k] = loadMask(bs[k])
		}
		unlock := snapshotLock(bs, masks)
		res := New()
		for i := 0; i < numShards; i++ {
			combineShardMany(res, bs, masks, i, intersect)
		}
		valid := versionsUnchanged(bs, versions)
		unlock()
		if valid {
			return res
		}
	}
	unlock := lockBitmaps(bs...)
	defer unlock()
	res := New()
	full := make([][4]uint64, len(bs))
	for k := range full {
		full[k] = [4]uint64{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}
	}
	for i := 0; i < numShards; i++ {
		combineShardMany(res, bs, full, i, intersect)
	}
	return res
}

func combineShardMany(res *Bitmap, bs []*Bitmap, masks [][4]uint64, i int, intersect bool) {
	occupied := 0
	for k := range bs {
		if maskBit(&masks[k], i) {
			occupied++
		}
	}
	if intersect {
		if occupied < len(bs) {
			return // some operand's shard is empty: intersection is empty
		}
		for hi, c := range bs[0].shards[i].containers {
			acc := c
			ok := true
			for _, other := range bs[1:] {
				oc := other.shards[i].containers[hi]
				if oc == nil {
					ok = false
					break
				}
				acc = and(acc, oc)
				if acc.cardinality() == 0 {
					ok = false
					break
				}
			}
			if ok && acc.cardinality() > 0 {
				res.putContainer(i, hi, acc)
			}
		}
		return
	}
	if occupied == 0 {
		return
	}
	seen := make(map[uint16]struct{})
	for k, b := range bs {
		if !maskBit(&masks[k], i) {
			continue
		}
		for hi := range b.shards[i].containers {
			if _, dup := seen[hi]; dup {
				continue
			}
			seen[hi] = struct{}{}
			var acc container
			for k2, b2 := range bs {
				if !maskBit(&masks[k2], i) {
					continue // shard not locked: guaranteed empty
				}
				if c := b2.shards[i].containers[hi]; c != nil {
					if acc == nil {
						acc = c
					} else {
						acc = or(acc, c)
					}
				}
			}
			if acc != nil && acc.cardinality() > 0 {
				res.putContainer(i, hi, acc)
			}
		}
	}
}
