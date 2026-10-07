package objstore

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
)

// walMagic identifies the start of a WAL record ("OBJ1").
const walMagic uint32 = 0x4f424a31

// maxWALPayload bounds a single record payload to reject corrupt lengths.
const maxWALPayload = 64 << 20

// walRecord is the atomic unit of durability. A committed transaction (object
// puts plus all root changes) is encoded as exactly one record, so a crash
// either applies the whole record or none of it. GC sweep deletions are also
// logged as a single record before they are applied in memory.
type walRecord struct {
	Puts     []*Object     `json:"puts,omitempty"`
	SetRoots map[string]ID `json:"setRoots,omitempty"`
	DelRoots []string      `json:"delRoots,omitempty"`
	Dels     []ID          `json:"dels,omitempty"`
}

// appendWALRecord writes one record and fsyncs, making it durable before the
// caller applies any in-memory change.
func appendWALRecord(f *os.File, rec *walRecord) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var hdr [12]byte
	binary.BigEndian.PutUint32(hdr[0:4], walMagic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[8:12], crc32.ChecksumIEEE(payload))
	if _, err := f.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	return f.Sync()
}

// replayWAL applies every intact record in f and returns the offset of the
// first byte that does not belong to a valid record (i.e. where a crash
// truncated the log). Callers truncate the file at that offset.
func replayWAL(f *os.File, apply func(*walRecord)) (int64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	var off int64
	hdr := make([]byte, 12)
	for {
		_, err := io.ReadFull(f, hdr)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return off, nil
		}
		if err != nil {
			return 0, err
		}
		magic := binary.BigEndian.Uint32(hdr[0:4])
		n := binary.BigEndian.Uint32(hdr[4:8])
		crc := binary.BigEndian.Uint32(hdr[8:12])
		if magic != walMagic || n > maxWALPayload {
			return off, nil
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(f, payload); err != nil {
			return off, nil
		}
		if crc32.ChecksumIEEE(payload) != crc {
			return off, nil
		}
		var rec walRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return off, nil
		}
		apply(&rec)
		off += 12 + int64(n)
	}
}
