package replay

import (
	"crypto/sha256"
	"encoding/binary"
)

// Snapshot is a byte-exact, content-hashed image of the full debuggable
// state: virtual clock, RNG state, component memory and pending timers.
type Snapshot struct {
	EventsApplied int      // number of log events applied to reach this state
	Tick          uint64   // virtual clock at snapshot time
	Bytes         []byte   // deterministic serialization of all state
	Hash          [32]byte // SHA-256 of Bytes
}

func putU64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func putSlice(buf []byte, n int) []byte {
	return putU64(buf, uint64(n))
}

// serialize encodes the complete kernel state in a fixed, deterministic
// field order. No maps, no pointers, no timestamps: identical logical
// states always produce identical bytes.
func (k *Kernel) serialize() []byte {
	buf := make([]byte, 0, 256)
	buf = putU64(buf, k.clock)
	buf = putU64(buf, k.rng.State())

	sys := k.sys
	buf = putU64(buf, sys.Retransmits)
	buf = putU64(buf, sys.Lost)
	buf = putU64(buf, sys.Bytes)
	buf = putU64(buf, sys.Checksum)

	buf = putSlice(buf, len(sys.Received))
	for _, p := range sys.Received {
		buf = putU64(buf, p.Seq)
		buf = putU64(buf, p.Size)
		buf = putU64(buf, p.Sum)
	}

	buf = putSlice(buf, len(sys.Delivered))
	for _, sum := range sys.Delivered {
		buf = putU64(buf, sum)
	}

	buf = putSlice(buf, len(k.timers))
	for _, t := range k.timers {
		buf = putU64(buf, t.fireAt)
		buf = putU64(buf, t.seq)
	}

	return buf
}

// Snapshot captures the current state of the kernel and every component.
func (k *Kernel) Snapshot() Snapshot {
	b := k.serialize()
	return Snapshot{
		EventsApplied: k.applied,
		Tick:          k.clock,
		Bytes:         b,
		Hash:          sha256.Sum256(b),
	}
}
