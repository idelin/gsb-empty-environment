package encobj

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"sync"
)

// The journal is an append-only log of checksummed frames. It records, in
// order: key registrations (fingerprints only, never key material), active
// key changes, nonce reservations and commit records. The commit record is
// the commit point of an object update.
//
// Frame layout:
//
//	magic   uint32  ('EJRN')
//	length  uint32  (payload length, <= maxFramePayload)
//	crc32   uint32  (IEEE, over payload)
//	payload []byte
//
// Recovery scans frames from the start. A torn or corrupt frame at the tail
// (nothing valid follows it) is truncated. A corrupt frame followed by any
// valid frame is mid-log corruption and recovery refuses to proceed.

const (
	frameMagic      uint32 = 0x454a524e // "EJRN"
	frameHeaderLen         = 12
	maxFramePayload        = 1 << 20
)

var ErrJournalCorrupt = errors.New("encobj: journal corrupt (mid-log damage)")

// Journal record types.
const (
	recKeyRegister  = 1 // str keyID, 32B key fingerprint (SHA-256, never the key)
	recActiveKey    = 2 // str keyID
	recNonceReserve = 3 // str keyID, u128 lo, u128 hi (reserved interval [lo,hi))
	recCommit       = 4 // object commit record (see store.go)
	recKeyRetire    = 5 // str keyID
)

type journal struct {
	st   Storage
	name string
	mu   sync.Mutex
}

// openJournal recovers the journal, truncating a torn tail and refusing
// mid-log corruption, and returns the decoded record payloads in order.
func openJournal(st Storage, name string) (*journal, [][]byte, error) {
	j := &journal{st: st, name: name}
	data, err := st.ReadAll(name)
	if errors.Is(err, fs.ErrNotExist) {
		return j, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var recs [][]byte
	off := 0
	for off < len(data) {
		payload, next, ok := parseFrame(data, off)
		if !ok {
			// Corrupt or incomplete frame at off. If any valid frame can be
			// found after it, the damage is in the middle of the log: refuse.
			for scan := off + 1; scan+frameHeaderLen <= len(data); scan++ {
				if _, _, ok := parseFrame(data, scan); ok {
					return nil, nil, fmt.Errorf("%w at offset %d", ErrJournalCorrupt, off)
				}
			}
			// Pure tail damage: truncate and continue.
			if err := st.Truncate(name, int64(off)); err != nil {
				return nil, nil, err
			}
			break
		}
		recs = append(recs, payload)
		off = next
	}
	return j, recs, nil
}

func parseFrame(data []byte, off int) (payload []byte, next int, ok bool) {
	if len(data)-off < frameHeaderLen {
		return nil, 0, false
	}
	hdr := data[off : off+frameHeaderLen]
	if binary.BigEndian.Uint32(hdr[0:4]) != frameMagic {
		return nil, 0, false
	}
	l := binary.BigEndian.Uint32(hdr[4:8])
	if l > maxFramePayload {
		return nil, 0, false
	}
	crc := binary.BigEndian.Uint32(hdr[8:12])
	if len(data)-off-frameHeaderLen < int(l) {
		return nil, 0, false
	}
	payload = data[off+frameHeaderLen : off+frameHeaderLen+int(l)]
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, 0, false
	}
	return payload, off + frameHeaderLen + int(l), true
}

// append frames the payload, appends it and fsyncs. The record is durable
// when append returns nil.
func (j *journal) append(payload []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	var hdr [frameHeaderLen]byte
	binary.BigEndian.PutUint32(hdr[0:4], frameMagic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(payload))
	frame := append(hdr[:], payload...)
	if err := j.st.Append(j.name, frame); err != nil {
		return err
	}
	return j.st.Sync(j.name)
}
