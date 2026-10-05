package checkpoint

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// computeDigest derives the snapshot digest. It binds, in order:
//   - the digests of the parent snapshots (in declared parent order, so
//     swapping merge parents changes the digest), and
//   - the exact operation sequence (reordering ops changes the digest).
func computeDigest(parentDigests []Hash, ops []Op) Hash {
	h := sha256.New()
	h.Write([]byte("checkpoint/digest/v1\n"))
	writeCount(h, uint64(len(parentDigests)))
	for _, d := range parentDigests {
		h.Write(d[:])
	}
	writeCount(h, uint64(len(ops)))
	for _, op := range ops {
		h.Write([]byte{byte(op.Kind)})
		writeCount(h, uint64(len(op.Name)))
		h.Write([]byte(op.Name))
		h.Write(op.Chunk[:])
	}
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func writeCount(h hash.Hash, n uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	h.Write(buf[:])
}
