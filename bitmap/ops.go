package bitmap

// Bitmap-level AND/OR/XOR. Each op snapshots both operands shard-by-shard
// (never holding more than one shard lock at a time, so it cannot deadlock
// against Commit) and computes results lock-free on immutable containers.

const (
	opAnd = iota
	opOr
	opXor
)

// And returns the intersection of bm and other as a new Bitmap.
func (bm *Bitmap) And(other *Bitmap) *Bitmap { return binaryOp(bm, other, opAnd) }

// Or returns the union of bm and other as a new Bitmap.
func (bm *Bitmap) Or(other *Bitmap) *Bitmap { return binaryOp(bm, other, opOr) }

// Xor returns the symmetric difference of bm and other as a new Bitmap.
func (bm *Bitmap) Xor(other *Bitmap) *Bitmap { return binaryOp(bm, other, opXor) }

func binaryOp(a, b *Bitmap, op int) *Bitmap {
	out := New()
	for i := 0; i < shardCount; i++ {
		sa := a.shardSnapshot(i)
		sb := b.shardSnapshot(i)
		if len(sa) == 0 && len(sb) == 0 {
			continue
		}
		dst := out.shards[i].slots
		switch op {
		case opAnd:
			for k, ca := range sa {
				if cb, ok := sb[k]; ok {
					if nc := andContainers(ca, cb); nc.cardinality() > 0 {
						dst[k] = newSlot(nc)
					}
				}
			}
		case opOr:
			for k, ca := range sa {
				if cb, ok := sb[k]; ok {
					dst[k] = newSlot(orContainers(ca, cb))
				} else if ca.cardinality() > 0 {
					dst[k] = newSlot(ca.clone())
				}
			}
			for k, cb := range sb {
				if _, ok := sa[k]; !ok && cb.cardinality() > 0 {
					dst[k] = newSlot(cb.clone())
				}
			}
		case opXor:
			for k, ca := range sa {
				if cb, ok := sb[k]; ok {
					if nc := xorContainers(ca, cb); nc.cardinality() > 0 {
						dst[k] = newSlot(nc)
					}
				} else if ca.cardinality() > 0 {
					dst[k] = newSlot(ca.clone())
				}
			}
			for k, cb := range sb {
				if _, ok := sa[k]; !ok && cb.cardinality() > 0 {
					dst[k] = newSlot(cb.clone())
				}
			}
		}
	}
	return out
}
