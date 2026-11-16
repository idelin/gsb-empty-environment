package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// Journal is the durable append-only event log used by a Host.
//
// Append must return nil only after the record has been durably stored and is
// guaranteed to be returned by a later Replay. If Append returns an error, the
// record must not be visible as a successfully replayed record.
//
// Replay invokes fn once for each durable record, in append order. A short
// torn tail left by an interrupted append may be discarded, but records in the
// middle of the log must not be silently skipped.
type Journal interface {
	Append(record []byte) error
	Replay(fn func(record []byte) error) error
}

const (
	recordHeaderSize = 12
	maxRecordSize    = 4 << 20
)

var (
	recordMagic = [4]byte{'Q', '1', '1', '5'}
	crcTable    = crc32.MakeTable(crc32.Castagnoli)
)

// FileJournal is a single-writer, append-only Journal backed by a file.
//
// Each record is framed as
//
//	magic[4] || payload length[4] || CRC32C[4] || payload
//
// with all integers in big-endian order. Every successful append is followed
// by File.Sync. A torn trailing record is removed when the file is opened or
// replayed. A malformed record followed by additional data is reported as
// ErrJournalCorrupt.
type FileJournal struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	broken error
	closed bool
}

// OpenFileJournal opens or creates a FileJournal at path.
func OpenFileJournal(path string) (*FileJournal, error) {
	if path == "" {
		return nil, fmt.Errorf("netsec: empty journal path")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("netsec: open journal: %w", err)
	}
	j := &FileJournal{file: f, path: path}
	if err := j.recoverTailLocked(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return j, nil
}

// Append durably appends one record. If the write or sync fails, the file is
// truncated back to its previous size whenever possible, so a failed append is
// not later replayed as an accepted event.
func (j *FileJournal) Append(record []byte) error {
	if len(record) == 0 {
		return fmt.Errorf("netsec: empty journal record")
	}
	if len(record) > maxRecordSize {
		return fmt.Errorf("netsec: journal record too large: %d bytes", len(record))
	}

	frame := make([]byte, recordHeaderSize+len(record))
	copy(frame[0:4], recordMagic[:])
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(record)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.Checksum(record, crcTable))
	copy(frame[recordHeaderSize:], record)

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return ErrJournalClosed
	}
	if j.broken != nil {
		return j.broken
	}

	info, err := j.file.Stat()
	if err != nil {
		return fmt.Errorf("netsec: stat journal: %w", err)
	}
	oldSize := info.Size()
	if err := writeFull(j.file, frame); err != nil {
		return j.failAppendLocked(oldSize, fmt.Errorf("netsec: append journal: %w", err))
	}
	if err := j.file.Sync(); err != nil {
		return j.failAppendLocked(oldSize, fmt.Errorf("netsec: sync journal: %w", err))
	}
	return nil
}

// Replay reads the journal and calls fn for each complete record in order.
func (j *FileJournal) Replay(fn func(record []byte) error) error {
	if fn == nil {
		return fmt.Errorf("netsec: nil replay callback")
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return ErrJournalClosed
	}
	if j.broken != nil {
		return j.broken
	}

	data, err := readAllFromStart(j.file)
	if err != nil {
		return fmt.Errorf("netsec: read journal: %w", err)
	}
	records, valid, err := splitRecords(data)
	if err != nil {
		return err
	}
	if valid != len(data) {
		if err := j.file.Truncate(int64(valid)); err != nil {
			return fmt.Errorf("netsec: remove torn journal tail: %w", err)
		}
		if err := j.file.Sync(); err != nil {
			return fmt.Errorf("netsec: sync truncated journal: %w", err)
		}
	}
	for _, record := range records {
		if err := fn(record); err != nil {
			return err
		}
	}
	return nil
}

// Path returns the path supplied to OpenFileJournal.
func (j *FileJournal) Path() string {
	return j.path
}

// Close closes the underlying file.
func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.file.Close()
}

func (j *FileJournal) recoverTailLocked() error {
	data, err := readAllFromStart(j.file)
	if err != nil {
		return fmt.Errorf("netsec: read journal: %w", err)
	}
	_, valid, err := splitRecords(data)
	if err != nil {
		return err
	}
	if valid == len(data) {
		return nil
	}
	if err := j.file.Truncate(int64(valid)); err != nil {
		return fmt.Errorf("netsec: remove torn journal tail: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("netsec: sync truncated journal: %w", err)
	}
	return nil
}

func (j *FileJournal) failAppendLocked(oldSize int64, appendErr error) error {
	if err := j.file.Truncate(oldSize); err != nil {
		j.broken = errors.Join(appendErr, fmt.Errorf("netsec: roll back journal: %w", err))
		return j.broken
	}
	if err := j.file.Sync(); err != nil {
		j.broken = errors.Join(appendErr, fmt.Errorf("netsec: sync journal rollback: %w", err))
		return j.broken
	}
	return appendErr
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readAllFromStart(f *os.File) ([]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// splitRecords parses as many complete records as possible. valid is the
// offset after the last valid record. An incomplete or CRC-damaged final
// record is treated as a torn tail; the same conditions in the middle of the
// file are corruption.
func splitRecords(data []byte) (records [][]byte, valid int, err error) {
	for valid < len(data) {
		if len(data)-valid < recordHeaderSize {
			return records, valid, nil
		}
		header := data[valid : valid+recordHeaderSize]
		if !bytes.Equal(header[0:4], recordMagic[:]) {
			return records, valid, fmt.Errorf("%w: bad magic at offset %d", ErrJournalCorrupt, valid)
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 || size > maxRecordSize {
			return records, valid, fmt.Errorf("%w: invalid record size %d at offset %d", ErrJournalCorrupt, size, valid)
		}
		end := valid + recordHeaderSize + int(size)
		if end > len(data) {
			return records, valid, nil
		}
		payload := data[valid+recordHeaderSize : end]
		wantCRC := binary.BigEndian.Uint32(header[8:12])
		if crc32.Checksum(payload, crcTable) != wantCRC {
			if end == len(data) {
				return records, valid, nil
			}
			return records, valid, fmt.Errorf("%w: bad CRC at offset %d", ErrJournalCorrupt, valid)
		}
		record := make([]byte, len(payload))
		copy(record, payload)
		records = append(records, record)
		valid = end
	}
	return records, valid, nil
}

// MemoryJournal is an in-memory Journal. It is useful for tests and for
// callers that provide their own durability layer, but it does not itself
// provide crash persistence.
type MemoryJournal struct {
	mu      sync.Mutex
	records [][]byte
}

// NewMemoryJournal returns an empty MemoryJournal.
func NewMemoryJournal() *MemoryJournal {
	return &MemoryJournal{}
}

// Append copies and stores a record.
func (j *MemoryJournal) Append(record []byte) error {
	if len(record) == 0 {
		return fmt.Errorf("netsec: empty journal record")
	}
	copyOfRecord := append([]byte(nil), record...)
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, copyOfRecord)
	return nil
}

// Replay calls fn for every stored record in order.
func (j *MemoryJournal) Replay(fn func(record []byte) error) error {
	if fn == nil {
		return fmt.Errorf("netsec: nil replay callback")
	}
	j.mu.Lock()
	records := make([][]byte, len(j.records))
	for i, record := range j.records {
		records[i] = append([]byte(nil), record...)
	}
	j.mu.Unlock()
	for _, record := range records {
		if err := fn(record); err != nil {
			return err
		}
	}
	return nil
}
