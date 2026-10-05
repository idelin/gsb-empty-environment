package pagestore

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

// The reference ledger is an append-only log of BIND/RELEASE records. It is
// the single source of truth for which page is bound to which exact
// dictionary version; dictionary reference counts are always derived from
// it by replay, never maintained as independent mutable state that could
// drift.

const (
	ledgerOpBind    = 0xB1
	ledgerOpRelease = 0xB2

	// bind:    op(1) pageID(8) dictID(8) digest(32) crc32(4)
	bindRecordSize = 1 + 8 + 8 + 32 + 4
	// release: op(1) pageID(8) crc32(4)
	releaseRecordSize = 1 + 8 + 4
)

// LedgerState is the result of replaying the ledger: the committed
// page -> dictionary-version bindings and the reference counts derived
// from them.
type LedgerState struct {
	Bindings map[uint64]DictKey // pageID -> exact dictionary version
	Refs     map[DictKey]int    // dictionary version -> bound page count
}

func newLedgerState() *LedgerState {
	return &LedgerState{
		Bindings: make(map[uint64]DictKey),
		Refs:     make(map[DictKey]int),
	}
}

func computeRefs(bindings map[uint64]DictKey) map[DictKey]int {
	refs := make(map[DictKey]int)
	for _, key := range bindings {
		refs[key]++
	}
	return refs
}

func encodeBindRecord(pageID uint64, key DictKey) []byte {
	rec := make([]byte, bindRecordSize)
	rec[0] = ledgerOpBind
	binary.LittleEndian.PutUint64(rec[1:9], pageID)
	binary.LittleEndian.PutUint64(rec[9:17], key.ID)
	copy(rec[17:49], key.Digest[:])
	binary.LittleEndian.PutUint32(rec[49:53], crc32.ChecksumIEEE(rec[:49]))
	return rec
}

func encodeReleaseRecord(pageID uint64) []byte {
	rec := make([]byte, releaseRecordSize)
	rec[0] = ledgerOpRelease
	binary.LittleEndian.PutUint64(rec[1:9], pageID)
	binary.LittleEndian.PutUint32(rec[9:13], crc32.ChecksumIEEE(rec[:9]))
	return rec
}

// ReplayLedger independently rebuilds the reference ledger state from the
// log file at path. It tolerates a torn tail (a crash mid-append): replay
// stops at the first incomplete or corrupt record and reports the number of
// valid bytes so the caller can truncate the log. A missing file replays to
// an empty state.
func ReplayLedger(path string) (state *LedgerState, validBytes int, err error) {
	state = newLedgerState()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, 0, nil
		}
		return nil, 0, err
	}

	off := 0
	for off < len(data) {
		switch data[off] {
		case ledgerOpBind:
			if off+bindRecordSize > len(data) {
				goto done
			}
			rec := data[off : off+bindRecordSize]
			if crc32.ChecksumIEEE(rec[:49]) != binary.LittleEndian.Uint32(rec[49:53]) {
				goto done
			}
			pageID := binary.LittleEndian.Uint64(rec[1:9])
			key := DictKey{ID: binary.LittleEndian.Uint64(rec[9:17])}
			copy(key.Digest[:], rec[17:49])
			state.Bindings[pageID] = key
			off += bindRecordSize
		case ledgerOpRelease:
			if off+releaseRecordSize > len(data) {
				goto done
			}
			rec := data[off : off+releaseRecordSize]
			if crc32.ChecksumIEEE(rec[:9]) != binary.LittleEndian.Uint32(rec[9:13]) {
				goto done
			}
			delete(state.Bindings, binary.LittleEndian.Uint64(rec[1:9]))
			off += releaseRecordSize
		default:
			goto done
		}
	}
done:
	state.Refs = computeRefs(state.Bindings)
	return state, off, nil
}
