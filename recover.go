package slotstore

import (
	"fmt"
	"io"
	"os"
	"sort"
)

// RecoveryReport describes what recovery found and repaired while opening a
// store. Repairs are informational; a CorruptError from Open means the
// damage (e.g. a free-chain cycle) was not safely repairable.
type RecoveryReport struct {
	ScrubbedSlots []uint32 // half-written / bad-checksum slots reset to free
	Repairs       []string // human-readable repair log
}

func (r *RecoveryReport) add(format string, args ...interface{}) {
	r.Repairs = append(r.Repairs, fmt.Sprintf(format, args...))
}

// recoverStore opens an existing store and reconciles the superblock's free
// chain with the ground truth of the slot area.
//
// Protocol:
//  1. Validate the superblock (magic, version, CRC). A bad superblock is
//     fatal: geometry and free-head cannot be trusted.
//  2. Decode every slot. Bad magic/CRC or state==writing means a torn or
//     half-written slot: it is never a valid record and gets scrubbed free.
//  3. Walk the on-disk free chain from freeHead. A cycle can never arise
//     from our crash windows (we only splice at the head), so it is hard
//     corruption and must be reported as an error. Out-of-range links too.
//  4. Cross-validate both directions: chain membership vs. slot state.
//     Mismatches match known crash windows (slot write vs. superblock
//     update ordering) and are repaired by rebuilding the chain from slot
//     ground truth. Backlink mismatches are likewise repaired.
func recoverStore(dir, metaPath, dataPath string, cfg Config) (*Store, *RecoveryReport, error) {
	rep := &RecoveryReport{}

	meta, err := os.OpenFile(metaPath, os.O_RDWR, 0644)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.OpenFile(dataPath, os.O_RDWR, 0644)
	if err != nil {
		meta.Close()
		return nil, nil, err
	}

	sbBuf := make([]byte, superSize)
	if _, err := meta.ReadAt(sbBuf, 0); err != nil {
		meta.Close()
		data.Close()
		return nil, nil, &CorruptError{Reason: "cannot read superblock: " + err.Error()}
	}
	sb, ok := decodeSuper(sbBuf)
	if !ok {
		meta.Close()
		data.Close()
		return nil, nil, &CorruptError{Reason: "superblock magic/version/checksum invalid"}
	}

	s := &Store{
		dir:        dir,
		meta:       meta,
		data:       data,
		inj:        cfg.Injector,
		slotCount:  sb.slotCount,
		payloadCap: sb.payloadCap,
		stride:     int64(slotOverhead + sb.payloadCap),
		freeHead:   sb.freeHead,
		gen:        make([]uint64, sb.slotCount),
		free:       make([]bool, sb.slotCount),
		freeNext:   make([]int32, sb.slotCount),
		freePrev:   make([]int32, sb.slotCount),
	}
	n := sb.slotCount

	// Pass 1: decode every slot; classify and scrub torn/half-written ones.
	decoded := make([]slot, n)
	valid := make([]bool, n)
	for i := uint32(0); i < n; i++ {
		sl, ok := s.readSlot(i)
		if !ok {
			rep.add("slot %d: bad magic/checksum (torn write); scrubbing", i)
			s.scrub(i, rawGen(s.data, s.offset(i), s.stride)+1, rep)
			continue
		}
		if sl.state == stateWriting {
			rep.add("slot %d: half-written (state=writing); scrubbing", i)
			s.scrub(i, sl.gen+1, rep)
			continue
		}
		decoded[i] = sl
		valid[i] = true
	}

	// Pass 2: walk the on-disk free chain. Cycles and wild links are hard
	// corruption; anything else is reconciled in pass 3.
	inChain := make([]bool, n)
	prev := int32(-1)
	for cur := sb.freeHead; cur >= 0; {
		if uint32(cur) >= n {
			meta.Close()
			data.Close()
			return nil, nil, &CorruptError{Reason: fmt.Sprintf("free chain link %d out of range (slots=%d)", cur, n)}
		}
		if inChain[cur] {
			meta.Close()
			data.Close()
			return nil, nil, &CorruptError{Reason: fmt.Sprintf("free chain contains a cycle at slot %d", cur)}
		}
		inChain[cur] = true
		if !valid[cur] || decoded[cur].state != stateFree {
			rep.add("slot %d: on free chain but not a valid free slot; chain truncated here", cur)
			break
		}
		if decoded[cur].prev != prev {
			rep.add("slot %d: backlink prev=%d, expected %d; will rebuild", cur, decoded[cur].prev, prev)
		}
		prev = cur
		cur = decoded[cur].next
	}

	// Pass 3: slot state is ground truth. Rebuild the free chain from it.
	var freeIdx []uint32
	for i := uint32(0); i < n; i++ {
		switch {
		case !valid[i]:
			freeIdx = append(freeIdx, i) // scrubbed in pass 1
		case decoded[i].state == stateFree:
			freeIdx = append(freeIdx, i)
			if !inChain[i] {
				rep.add("slot %d: free but missing from chain; re-linking", i)
			}
			s.gen[i] = decoded[i].gen
		default: // occupied
			if inChain[i] {
				rep.add("slot %d: occupied but on free chain (crash before superblock update); un-linking", i)
			}
			s.gen[i] = decoded[i].gen
			s.free[i] = false
			s.freeNext[i], s.freePrev[i] = -1, -1
		}
	}
	sort.Slice(freeIdx, func(a, b int) bool { return freeIdx[a] < freeIdx[b] })

	s.freeHead = -1
	buf := make([]byte, s.stride)
	for pos, i := range freeIdx {
		next := int32(-1)
		if pos+1 < len(freeIdx) {
			next = int32(freeIdx[pos+1])
		}
		prev := int32(-1)
		if pos > 0 {
			prev = int32(freeIdx[pos-1])
		}
		if pos == 0 {
			s.freeHead = int32(i)
		}
		s.free[i] = true
		s.freeNext[i], s.freePrev[i] = next, prev
		encodeSlot(buf, slot{state: stateFree, gen: s.gen[i], next: next, prev: prev}, s.payloadCap)
		if _, err := s.data.WriteAt(buf, s.offset(i)); err != nil {
			s.Close()
			return nil, nil, err
		}
	}
	if err := s.data.Sync(); err != nil {
		s.Close()
		return nil, nil, err
	}
	if err := s.writeSuper(); err != nil {
		s.Close()
		return nil, nil, err
	}
	return s, rep, nil
}

// scrub rewrites slot i as a clean free slot with the given generation.
func (s *Store) scrub(i uint32, gen uint64, rep *RecoveryReport) {
	rep.ScrubbedSlots = append(rep.ScrubbedSlots, i)
	s.gen[i] = gen
	buf := make([]byte, s.stride)
	encodeSlot(buf, slot{state: stateFree, gen: gen, next: -1, prev: -1}, s.payloadCap)
	s.data.WriteAt(buf, s.offset(i)) // rewritten again with links in pass 3
}

// rawGen reads the generation field directly, best effort, for slots whose
// checksum failed. See README for the guarantee boundary this implies.
func rawGen(f *os.File, off, stride int64) uint64 {
	buf := make([]byte, 16)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return 0
	}
	return uint64(buf[8]) | uint64(buf[9])<<8 | uint64(buf[10])<<16 | uint64(buf[11])<<24 |
		uint64(buf[12])<<32 | uint64(buf[13])<<40 | uint64(buf[14])<<48 | uint64(buf[15])<<56
}
