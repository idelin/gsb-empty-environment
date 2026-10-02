package kernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// SnapshotHash is the SHA-256 digest of a canonical state encoding.
type SnapshotHash [32]byte

// byteWriter accumulates the canonical binary encoding of the state.
// Every multi-byte integer is little-endian; collections are written in a
// sorted or insertion order that is itself deterministic.
type byteWriter struct {
	buf bytes.Buffer
	tmp [8]byte
}

func (w *byteWriter) u64(v uint64) {
	binary.LittleEndian.PutUint64(w.tmp[:], v)
	w.buf.Write(w.tmp[:])
}

func (w *byteWriter) u32(v uint32) {
	binary.LittleEndian.PutUint32(w.tmp[:4], v)
	w.buf.Write(w.tmp[:4])
}

func (w *byteWriter) u8(v uint8) {
	w.buf.WriteByte(v)
}

func (w *byteWriter) boolByte(v bool) {
	if v {
		w.buf.WriteByte(1)
	} else {
		w.buf.WriteByte(0)
	}
}

func (w *byteWriter) event(ev Event) {
	w.u64(ev.Tick)
	w.u8(uint8(ev.Kind))
	w.u64(ev.Packet.ID)
	w.u32(ev.Packet.Size)
	w.u32(ev.Packet.Src)
	w.u32(ev.Packet.Dst)
	w.u64(ev.TimerID)
}

// snapshotBytes produces the canonical encoding of the whole kernel state:
// virtual clock, queued scheduler events, and every system component.
func (k *Kernel) snapshotBytes() []byte {
	w := &byteWriter{}
	w.u64(k.sched.Now())
	pending := k.sched.pendingSnapshot()
	w.u64(uint64(len(pending)))
	for _, item := range pending {
		w.event(item.ev)
	}
	w.u64(k.sys.rng.State())
	w.u64(k.sys.stats.Arrivals)
	w.u64(k.sys.stats.Processed)
	w.u64(k.sys.stats.Timeouts)
	w.u64(k.sys.stats.ClockInterrupt)
	w.u64(k.sys.stats.BytesProcessed)
	w.u64(k.sys.stats.Retransmits)
	w.u64(uint64(len(k.sys.inbox)))
	for _, p := range k.sys.inbox {
		w.u64(p.ID)
		w.u32(p.Size)
		w.u32(p.Src)
		w.u32(p.Dst)
		w.u64(p.ArrivalTick)
		w.boolByte(p.Lost)
		w.u32(p.Retries)
	}
	ids := make([]uint64, 0, len(k.sys.timers))
	for id := range k.sys.timers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	w.u64(uint64(len(ids)))
	for _, id := range ids {
		w.u64(id)
		w.u64(k.sys.timers[id])
	}
	w.u64(uint64(len(k.sys.processed)))
	for _, id := range k.sys.processed {
		w.u64(id)
	}
	return w.buf.Bytes()
}

// hashOf digests a canonical state encoding.
func hashOf(b []byte) SnapshotHash {
	return SnapshotHash(sha256.Sum256(b))
}
