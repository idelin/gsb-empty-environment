package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// BlockID identifies one logical block.
type BlockID uint64

// Block is a decrypted logical block.  Data always has exactly
// Config.BlockDataSize bytes.  MAC is HMAC-SHA256 over the tree ID, ID,
// version, and data.
type Block struct {
	ID      BlockID
	Version uint64
	Data    []byte
	MAC     [32]byte
}

func (b Block) clone() Block {
	out := b
	out.Data = append([]byte(nil), b.Data...)
	return out
}

func (b Block) sameVersionContent(other Block) bool {
	return b.ID == other.ID && b.Version == other.Version &&
		bytes.Equal(b.Data, other.Data) &&
		hmac.Equal(b.MAC[:], other.MAC[:])
}

func blockMAC(key []byte, treeID string, id BlockID, version uint64, data []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("pathoram-block-mac-v1"))
	writeStringField(mac, treeID)
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], uint64(id))
	binary.BigEndian.PutUint64(buf[8:16], version)
	mac.Write(buf[:])
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	mac.Write(length[:])
	mac.Write(data)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func (b Block) validMAC(key []byte, treeID string) bool {
	expected := blockMAC(key, treeID, b.ID, b.Version, b.Data)
	return hmac.Equal(expected[:], b.MAC[:])
}

// ComputeBlockMAC exposes the deterministic MAC construction for
// interoperability and tests.
func ComputeBlockMAC(key []byte, treeID string, id BlockID, version uint64, data []byte) [32]byte {
	return blockMAC(key, treeID, id, version, data)
}

// mergePathBlocks merges blocks recovered from one path into the current
// stash.  For each block ID:
//
//   - duplicate versions with differing contents are rejected;
//   - a version lower than the stashed version is stale and ignored;
//   - a version equal to the stashed version must have identical content;
//   - the highest version is accepted only when it is the next consecutive
//     version, or when every intervening version is also present on the
//     path.
//
// The operation is order-independent.
func mergePathBlocks(stash map[BlockID]Block, path []Block) error {
	type candidate struct {
		max   Block
		seenv map[uint64]Block
	}
	cands := make(map[BlockID]*candidate)
	for _, b := range path {
		c, ok := cands[b.ID]
		if !ok {
			c = &candidate{max: b, seenv: make(map[uint64]Block)}
			cands[b.ID] = c
		}
		if previous, exists := c.seenv[b.Version]; exists {
			if !previous.sameVersionContent(b) {
				return ErrVersionConflict
			}
		} else {
			c.seenv[b.Version] = b.clone()
		}
		if b.Version > c.max.Version {
			c.max = b.clone()
		} else if b.Version == c.max.Version && !c.max.sameVersionContent(b) {
			return ErrVersionConflict
		}
	}

	for id, c := range cands {
		existing, hasExisting := stash[id]
		if !hasExisting {
			stash[id] = c.max.clone()
			continue
		}
		switch {
		case c.max.Version < existing.Version:
			// Stale copy still in an off-path bucket.
		case c.max.Version == existing.Version:
			if !existing.sameVersionContent(c.max) {
				return ErrVersionConflict
			}
		case c.max.Version == existing.Version+1:
			stash[id] = c.max.clone()
		case c.max.Version > existing.Version+1:
			// Accept a jump only if the path carries the complete
			// consecutive chain.  Otherwise the server has hidden a
			// version, which is a fork.
			for v := existing.Version + 1; ; v++ {
				if _, ok := c.seenv[v]; !ok {
					return ErrVersionConflict
				}
				if v == c.max.Version {
					break
				}
			}
			stash[id] = c.max.clone()
		}
	}
	return nil
}

func writeStringField(w interface{ Write([]byte) (int, error) }, s string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(s)))
	_, _ = w.Write(length[:])
	_, _ = w.Write([]byte(s))
}
