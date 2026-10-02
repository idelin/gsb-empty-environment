package lsm

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// WAL record layout (all integers little-endian):
//
//	crc32(key|val) : 4 bytes
//	key length     : 4 bytes
//	value length   : 4 bytes
//	key            : key length bytes
//	value          : value length bytes
const walRecordHeaderSize = 12

// walWriter appends records to a single WAL segment. It is only used while
// the caller holds the DB write mutex, so it needs no internal locking.
type walWriter struct {
	f    *os.File
	path string
	sync bool
}

func walFilePath(dir string, id uint64) string {
	return filepath.Join(dir, fmt.Sprintf("wal-%06d.log", id))
}

// openWALSegment opens (or creates) a WAL segment for appending.
func openWALSegment(path string, syncWrites bool) (*walWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &walWriter{f: f, path: path, sync: syncWrites}, nil
}

func (w *walWriter) Append(key, val []byte) error {
	var hdr [walRecordHeaderSize]byte
	h := crc32.NewIEEE()
	h.Write(key)
	h.Write(val)
	binary.LittleEndian.PutUint32(hdr[0:4], h.Sum32())
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(key)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(val)))
	if _, err := w.f.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.f.Write(key); err != nil {
		return err
	}
	if _, err := w.f.Write(val); err != nil {
		return err
	}
	if w.sync {
		return w.f.Sync()
	}
	return nil
}

func (w *walWriter) Sync() error { return w.f.Sync() }

func (w *walWriter) Close() error {
	w.f.Sync()
	return w.f.Close()
}

// replayWALSegment applies every intact record in the segment, in order.
// Replay stops silently at the first torn or corrupt record (a crash can
// leave a partial tail). It returns the offset of the end of the valid
// region so the caller can truncate the segment before appending again.
func replayWALSegment(path string, apply func(key, val []byte) error) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var off int64
	for {
		if int64(len(data))-off < walRecordHeaderSize {
			break
		}
		crc := binary.LittleEndian.Uint32(data[off:])
		klen := binary.LittleEndian.Uint32(data[off+4:])
		vlen := binary.LittleEndian.Uint32(data[off+8:])
		end := off + walRecordHeaderSize + int64(klen) + int64(vlen)
		if end > int64(len(data)) {
			break // torn tail
		}
		key := data[off+walRecordHeaderSize : off+walRecordHeaderSize+int64(klen)]
		val := data[off+walRecordHeaderSize+int64(klen) : end]
		h := crc32.NewIEEE()
		h.Write(key)
		h.Write(val)
		if h.Sum32() != crc {
			break // corrupt record
		}
		if err := apply(key, val); err != nil {
			return 0, err
		}
		off = end
	}
	return off, nil
}
