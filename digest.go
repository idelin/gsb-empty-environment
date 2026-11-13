package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// ComputeModuleDigest derives the host-visible identity of a module. It binds
// the immutable code bytes and the trusted decoder's instruction and function
// intervals to one digest, including landing-pad registrations and type tables.
func ComputeModuleDigest(m Module) ModuleDigest {
	h := sha256.New()
	writeU64(h, uint64(len(m.Code)))
	h.Write(m.Code)
	writeU64(h, uint64(len(m.Instructions)))
	for _, ins := range m.Instructions {
		writeU64(h, ins.Start)
		writeU64(h, ins.End)
		writeBool(h, ins.LandingPad)
	}
	writeU64(h, uint64(len(m.Functions)))
	for _, fn := range m.Functions {
		writeU32(h, uint32(fn.ID))
		writeU64(h, fn.Start)
		writeU64(h, fn.End)
		writeU64(h, fn.EntryPrefixEnd)
		writeU64(h, uint64(len(fn.TypeIDs)))
		for _, id := range fn.TypeIDs {
			writeU32(h, uint32(id))
		}
	}
	var out ModuleDigest
	copy(out[:], h.Sum(nil))
	return out
}

// Digest is a convenience form of ComputeModuleDigest.
func (m Module) Digest() ModuleDigest { return ComputeModuleDigest(m) }

func writeU64(h hash.Hash, v uint64) { _ = binary.Write(h, binary.BigEndian, v) }
func writeU32(h hash.Hash, v uint32) { _ = binary.Write(h, binary.BigEndian, v) }

func writeBool(h hash.Hash, v bool) {
	if v {
		h.Write([]byte{1})
		return
	}
	h.Write([]byte{0})
}
