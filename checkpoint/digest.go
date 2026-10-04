package checkpoint

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"
	"strconv"
)

// Digest is a content-addressed identifier of the form "sha256:<hex>".
type Digest string

// BlockDigest returns the content address of raw block data.
func BlockDigest(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func writeString(h hash.Hash, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}

// computeDigest binds a snapshot to its parent digests (in declared order)
// and to the exact operation sequence: auto-merge ops first, then user ops.
// Reordering operations or reparenting yields a different digest.
func computeDigest(parentDigests []Digest, autoOps, ops []Operation) Digest {
	h := sha256.New()
	writeString(h, "checkpoint/snapshot/v1")
	writeString(h, strconv.Itoa(len(parentDigests)))
	for _, p := range parentDigests {
		writeString(h, string(p))
	}
	encodeOps(h, autoOps)
	encodeOps(h, ops)
	return Digest("sha256:" + hex.EncodeToString(h.Sum(nil)))
}

// encodeOps hashes operations in declared order (order is significant).
// Refs inside a single operation are sorted so the encoding is canonical.
func encodeOps(h hash.Hash, ops []Operation) {
	writeString(h, strconv.Itoa(len(ops)))
	for _, op := range ops {
		writeString(h, string(op.Kind))
		refs := make([]Ref, len(op.Refs))
		copy(refs, op.Refs)
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Path != refs[j].Path {
				return refs[i].Path < refs[j].Path
			}
			return refs[i].Block < refs[j].Block
		})
		writeString(h, strconv.Itoa(len(refs)))
		for _, r := range refs {
			writeString(h, r.Path)
			writeString(h, string(r.Block))
		}
	}
}
