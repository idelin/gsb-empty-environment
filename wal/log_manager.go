package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// ErrCorrupt signals a truncated or checksum-invalid record frame. Readers
// treat it as the end of the valid log (the crash point).
var ErrCorrupt = errors.New("wal: corrupt or truncated record frame")

const (
	frameHeaderSize = 8 // payload length uint32 + CRC32 uint32
	maxRecordSize   = 4 << 20
)

// LogManager appends framed records to the WAL file and flushes them
// durably. LSNs are byte offsets of record frames.
type LogManager struct {
	mu         sync.Mutex
	file       *os.File
	buffer     []byte // encoded frames not yet flushed
	nextLSN    LSN    // offset where the next frame starts
	flushedLSN LSN    // watermark: frames before this offset are durable
}

// Open opens (or creates) the WAL file, scans it to find the end of the
// valid log, and truncates any torn tail left by a crash.
func Open(path string) (*LogManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	lm := &LogManager{file: f, flushedLSN: LSNInvalid}
	end, err := lm.findEnd()
	if err != nil {
		f.Close()
		return nil, err
	}
	lm.nextLSN = end
	lm.flushedLSN = end
	if err := f.Truncate(int64(end)); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(int64(end), io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return lm, nil
}

// findEnd scans the file and returns the offset just past the last valid frame.
func (lm *LogManager) findEnd() (LSN, error) {
	it := &Iterator{file: lm.file} // file-only: no buffer exists yet
	for {
		_, err := it.Next()
		if err == io.EOF {
			return LSN(it.offset), nil
		}
		if err == ErrCorrupt {
			return LSN(it.offset), nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// Append encodes rec and buffers it for the next flush. It returns the
// record's LSN and also stores it in rec.LSN.
func (lm *LogManager) Append(rec *LogRecord) (LSN, error) {
	payload := rec.encode()
	if len(payload) > maxRecordSize {
		return LSNInvalid, errors.New("wal: record too large")
	}
	frame := make([]byte, frameHeaderSize+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[frameHeaderSize:], payload)

	lm.mu.Lock()
	rec.LSN = lm.nextLSN
	lm.buffer = append(lm.buffer, frame...)
	lm.nextLSN += LSN(len(frame))
	lsn := rec.LSN
	lm.mu.Unlock()
	return lsn, nil
}

// Flush writes all buffered frames to the file and fsyncs.
func (lm *LogManager) Flush() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.flushLocked()
}

func (lm *LogManager) flushLocked() error {
	if len(lm.buffer) == 0 {
		return nil
	}
	if _, err := lm.file.Write(lm.buffer); err != nil {
		return err
	}
	lm.buffer = lm.buffer[:0]
	if err := lm.file.Sync(); err != nil {
		return err
	}
	lm.flushedLSN = lm.nextLSN
	return nil
}

// FlushedLSN returns the durability watermark: every frame starting at an
// offset strictly below it is on stable storage.
func (lm *LogManager) FlushedLSN() LSN {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.flushedLSN
}

// NextLSN returns the offset where the next appended record will land.
func (lm *LogManager) NextLSN() LSN {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.nextLSN
}

// Close flushes and closes the WAL file.
func (lm *LogManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if err := lm.flushLocked(); err != nil {
		return err
	}
	return lm.file.Close()
}

// Iterator returns a sequential scanner starting at the log beginning.
func (lm *LogManager) Iterator() *Iterator { return lm.IteratorFrom(0) }

// IteratorFrom returns a sequential scanner starting at the given LSN.
func (lm *LogManager) IteratorFrom(lsn LSN) *Iterator {
	if lsn < 0 {
		lsn = 0
	}
	return &Iterator{lm: lm, offset: int64(lsn)}
}

// readAt reads len(p) bytes at the given log offset, serving durable frames
// from the file and not-yet-flushed frames from the in-memory buffer. It
// returns io.EOF when the offset is at or past the end of the valid log.
func (lm *LogManager) readAt(p []byte, off int64) (int, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	durable := int64(lm.flushedLSN)
	if off+int64(len(p)) <= durable {
		return lm.file.ReadAt(p, off)
	}
	if off < durable {
		// A frame never straddles the flush watermark; treat as corrupt.
		return 0, ErrCorrupt
	}
	bufOff := off - durable
	if bufOff >= int64(len(lm.buffer)) {
		return 0, io.EOF
	}
	n := copy(p, lm.buffer[bufOff:])
	if n < len(p) {
		return n, ErrCorrupt
	}
	return n, nil
}

// ReadRecord reads the single record stored at the given LSN.
func (lm *LogManager) ReadRecord(lsn LSN) (*LogRecord, error) {
	return lm.IteratorFrom(lsn).Next()
}

// Iterator scans record frames sequentially, seeing both flushed and
// buffered (not yet flushed) records. It is safe for concurrent use with
// other iterators and with Append/Flush.
type Iterator struct {
	lm     *LogManager // when set, reads go through lm.readAt
	file   *os.File    // file-only mode (used during Open)
	offset int64
}

func (it *Iterator) readAt(p []byte, off int64) (int, error) {
	if it.lm != nil {
		return it.lm.readAt(p, off)
	}
	return it.file.ReadAt(p, off)
}

// Next returns the next record, io.EOF at the clean end of the log, or
// ErrCorrupt when a torn/invalid frame is encountered.
func (it *Iterator) Next() (*LogRecord, error) {
	var header [frameHeaderSize]byte
	n, err := it.readAt(header[:], it.offset)
	if err == io.EOF && n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, ErrCorrupt
	}
	length := binary.BigEndian.Uint32(header[0:4])
	if length == 0 || length > maxRecordSize {
		return nil, ErrCorrupt
	}
	crc := binary.BigEndian.Uint32(header[4:8])
	payload := make([]byte, length)
	if _, err := it.readAt(payload, it.offset+frameHeaderSize); err != nil {
		return nil, ErrCorrupt
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, ErrCorrupt
	}
	rec, err := decodeRecord(LSN(it.offset), payload)
	if err != nil {
		return nil, err
	}
	it.offset += frameHeaderSize + int64(length)
	return rec, nil
}
