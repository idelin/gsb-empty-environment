package receiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Write-ahead log record types.
const (
	recAccept byte = 1 // code accepted; unlock is pending
	recDone   byte = 2 // unlock callback completed for opID
	recResync byte = 3 // first half of a two-code resync observed
)

const maxRecordLen = 1 << 16

var errCorruptTail = errors.New("receiver: corrupt wal record")

type walRecord struct {
	typ      byte
	deviceID uint64
	counter  uint32
	opID     string
}

// encodeRecord lays out a record as:
//
//	len(4) | type(1) deviceID(8) counter(4) opLen(2) opID | crc32(4)
//
// where len covers everything after itself and crc32 covers the payload.
func encodeRecord(r walRecord) []byte {
	payload := make([]byte, 0, 1+8+4+2+len(r.opID))
	payload = append(payload, r.typ)
	payload = binary.BigEndian.AppendUint64(payload, r.deviceID)
	payload = binary.BigEndian.AppendUint32(payload, r.counter)
	payload = binary.BigEndian.AppendUint16(payload, uint16(len(r.opID)))
	payload = append(payload, r.opID...)

	out := make([]byte, 0, 4+len(payload)+4)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)+4))
	out = append(out, payload...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(payload))
	return out
}

// decodeRecord parses one record from buf, returning the record and the
// number of bytes consumed. errCorruptTail is returned for torn or
// corrupted tails left by a crash mid-write.
func decodeRecord(buf []byte) (walRecord, int, error) {
	if len(buf) < 4 {
		return walRecord{}, 0, errCorruptTail
	}
	n := int(binary.BigEndian.Uint32(buf[:4]))
	if n < 1+8+4+2+4 || n > maxRecordLen || len(buf) < 4+n {
		return walRecord{}, 0, errCorruptTail
	}
	payload := buf[4 : 4+n-4]
	crc := binary.BigEndian.Uint32(buf[4+n-4 : 4+n])
	if crc32.ChecksumIEEE(payload) != crc {
		return walRecord{}, 0, errCorruptTail
	}
	opLen := int(binary.BigEndian.Uint16(payload[13:15]))
	if len(payload) != 15+opLen {
		return walRecord{}, 0, errCorruptTail
	}
	return walRecord{
		typ:      payload[0],
		deviceID: binary.BigEndian.Uint64(payload[1:9]),
		counter:  binary.BigEndian.Uint32(payload[9:13]),
		opID:     string(payload[15:]),
	}, 4 + n, nil
}

// wal is an append-only, fsync-on-write log. Every mutation of receiver
// state is persisted here before it takes effect.
type wal struct {
	f *os.File
}

// openWAL opens (creating if needed) the log at path and replays all valid
// records. A corrupt or torn tail from a crash is truncated so future
// appends start at a clean boundary.
func openWAL(path string) (*wal, []walRecord, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	var recs []walRecord
	off := 0
	for off < len(data) {
		rec, n, err := decodeRecord(data[off:])
		if err != nil {
			if truncErr := f.Truncate(int64(off)); truncErr != nil {
				f.Close()
				return nil, nil, fmt.Errorf("truncate corrupt tail: %w", truncErr)
			}
			break
		}
		recs = append(recs, rec)
		off += n
	}
	if _, err := f.Seek(int64(off), io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	return &wal{f: f}, recs, nil
}

// append writes the record and fsyncs before returning. After append
// returns nil the record survives power loss.
func (w *wal) append(r walRecord) error {
	if _, err := w.f.Write(encodeRecord(r)); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *wal) close() error {
	return w.f.Close()
}
