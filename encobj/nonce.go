package encobj

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sync"
)

// nonceAlloc hands out 96-bit nonces from a single monotonic counter shared
// by field and manifest encryption for every key ID in the store.
//
// Durability protocol (persistent high-water reservation):
//   - The allocator may only use counter values below a reserved high-water
//     mark. A reservation becomes usable only after a record containing the
//     new upper bound has been appended to nonce.log and fsynced.
//   - On recovery the counter restarts at the highest recorded bound; the
//     unused tail of the last reserved interval is skipped (wasted, never
//     reused).
//   - When the counter reaches the configured limit, encryption is refused
//     with ErrNonceExhausted.
//
// Log record format (all little-endian):
//
//	crc32(payload) uint32 | reservedHi uint64
//
// A torn tail (short record or bad crc at the end) is truncated by rewriting
// the log with the valid prefix. Corruption in the middle (bad crc or
// non-increasing bound before EOF) aborts recovery with ErrCorrupt.
type nonceAlloc struct {
	mu         sync.Mutex
	next       uint64 // next value to hand out
	reservedHi uint64 // values < reservedHi are usable
	max        uint64 // hard limit; next must stay < max
	block      uint64 // reservation block size
	st         Storage
	logName    string
	// onAlloc, when set (tests), is called with every handed-out nonce.
	onAlloc func(nonce [12]byte)
	// hook, when set (fault-injection tests), runs before the reservation
	// record is appended.
	hook func() error
}

const nonceLogName = "nonce.log"
const nonceRecLen = 12

func openNonceAlloc(st Storage, block, max uint64) (*nonceAlloc, error) {
	if block == 0 {
		block = 1024
	}
	a := &nonceAlloc{st: st, logName: nonceLogName, block: block, max: max}
	raw, err := st.ReadFile(nonceLogName)
	if err == ErrNotExist {
		a.next, a.reservedHi = 0, 0
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	var hi uint64
	valid := 0
	for off := 0; off+nonceRecLen <= len(raw); off += nonceRecLen {
		rec := raw[off : off+nonceRecLen]
		if crc32.ChecksumIEEE(rec[4:]) != binary.LittleEndian.Uint32(rec[:4]) {
			if off+nonceRecLen == len(raw) {
				break // torn tail: truncate below
			}
			return nil, fmt.Errorf("%w: nonce.log bad crc at offset %d", ErrCorrupt, off)
		}
		v := binary.LittleEndian.Uint64(rec[4:])
		if v <= hi {
			return nil, fmt.Errorf("%w: nonce.log non-increasing bound at offset %d", ErrCorrupt, off)
		}
		hi = v
		valid = off + nonceRecLen
	}
	if valid != len(raw) {
		// Truncate torn tail by atomically replacing the log with its
		// valid prefix.
		if err := st.WriteFileAtomic(nonceLogName, raw[:valid]); err != nil {
			return nil, err
		}
	}
	a.next, a.reservedHi = hi, hi
	return a, nil
}

// Next returns the next unique 96-bit nonce, reserving (and fsyncing) a new
// high-water interval first when the current one is exhausted.
func (a *nonceAlloc) Next() ([12]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.next >= a.max {
		return [12]byte{}, ErrNonceExhausted
	}
	if a.next >= a.reservedHi {
		newHi := a.reservedHi + a.block
		if newHi > a.max {
			newHi = a.max
		}
		if newHi <= a.next {
			return [12]byte{}, ErrNonceExhausted
		}
		var rec [nonceRecLen]byte
		binary.LittleEndian.PutUint64(rec[4:], newHi)
		binary.LittleEndian.PutUint32(rec[:4], crc32.ChecksumIEEE(rec[4:]))
		// The upper bound must be durable before any value inside the
		// interval may be used.
		if a.hook != nil {
			if err := a.hook(); err != nil {
				return [12]byte{}, err
			}
		}
		if err := a.st.AppendFile(a.logName, rec[:]); err != nil {
			return [12]byte{}, err
		}
		a.reservedHi = newHi
	}
	v := a.next
	a.next++
	var n [12]byte
	binary.BigEndian.PutUint64(n[4:], v)
	if a.onAlloc != nil {
		a.onAlloc(n)
	}
	return n, nil
}
