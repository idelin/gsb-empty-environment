package noncelease

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrStoreClosed is returned when a write hits an already closed or failed
// store. A failed store never silently drops a reservation.
var ErrStoreClosed = errors.New("noncelease: durable store is closed")

// Record types on disk.
const (
	recCreateKey byte = 1 + iota
	recRotateKey
	recAllocate
)

// Frame layout (all integers little-endian):
//
//	magic   [4]byte  "NLC1"
//	length  uint32   length of body
//	body    [length]byte
//	crc32   uint32   IEEE CRC over magic|length|body
//
// Bodies:
//
//	create:   typ(1) version(4) limit(8) keyLen(2) key(keyLen)
//	rotate:   typ(1) version(4) keyLen(2) key(keyLen)
//	allocate: typ(1) version(4) start(8) end(8) expiresAt(8) keyLen(2) key(keyLen)

var frameMagic = [4]byte{'N', 'L', 'C', '1'}

const (
	headerLen   = 4 + 4
	footerLen   = 4
	maxKeyLen   = 4096
	maxBodyLen  = 1 + 4 + 8 + 8 + 8 + 2 + maxKeyLen
	maxFrameLen = headerLen + maxBodyLen + footerLen
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// record is one durable log entry.
type record struct {
	typ       byte
	key       KeyID
	version   uint32
	limit     uint64
	start     uint64
	end       uint64
	expiresAt int64
}

func (r record) encodeBody() ([]byte, error) {
	if len(r.key) > maxKeyLen {
		return nil, errors.New("noncelease: key id too long")
	}
	size := 1 + 4 + 2 + len(r.key)
	switch r.typ {
	case recAllocate:
		size += 8 + 8 + 8
	case recCreateKey:
		size += 8
	}
	body := make([]byte, size)
	body[0] = r.typ
	off := 1
	binary.LittleEndian.PutUint32(body[off:], r.version)
	off += 4
	switch r.typ {
	case recCreateKey:
		binary.LittleEndian.PutUint64(body[off:], r.limit)
		off += 8
	case recAllocate:
		binary.LittleEndian.PutUint64(body[off:], r.start)
		off += 8
		binary.LittleEndian.PutUint64(body[off:], r.end)
		off += 8
		binary.LittleEndian.PutUint64(body[off:], uint64(r.expiresAt))
		off += 8
	}
	binary.LittleEndian.PutUint16(body[off:], uint16(len(r.key)))
	off += 2
	copy(body[off:], string(r.key))
	return body, nil
}

func decodeBody(body []byte) (record, error) {
	if len(body) < 1+4+2 {
		return record{}, errors.New("noncelease: truncated record body")
	}
	r := record{typ: body[0]}
	off := 1
	r.version = binary.LittleEndian.Uint32(body[off:])
	off += 4
	switch r.typ {
	case recCreateKey:
		if len(body) < off+8+2 {
			return record{}, errors.New("noncelease: truncated create body")
		}
		r.limit = binary.LittleEndian.Uint64(body[off:])
		off += 8
	case recAllocate:
		if len(body) < off+8+8+8+2 {
			return record{}, errors.New("noncelease: truncated allocate body")
		}
		r.start = binary.LittleEndian.Uint64(body[off:])
		off += 8
		r.end = binary.LittleEndian.Uint64(body[off:])
		off += 8
		r.expiresAt = int64(binary.LittleEndian.Uint64(body[off:]))
		off += 8
	}
	keyLen := int(binary.LittleEndian.Uint16(body[off:]))
	off += 2
	if keyLen == 0 || off+keyLen != len(body) {
		return record{}, errors.New("noncelease: malformed key in record")
	}
	r.key = KeyID(body[off : off+keyLen])
	switch r.typ {
	case recCreateKey, recRotateKey, recAllocate:
	default:
		return record{}, errors.New("noncelease: unknown record type")
	}
	return r, nil
}

// durableFile is the storage primitive. *os.File satisfies it; tests provide
// a crash-injecting fake that can lose or tear the last write.
type durableFile interface {
	io.ReaderAt
	io.Writer
	io.Closer
	Name() string
	Seek(offset int64, whence int) (int64, error)
	Sync() error
	Truncate(size int64) error
}

// Store is the append-only, checksummed write-ahead log of reservations.
type Store struct {
	mu      sync.Mutex
	file    durableFile
	records []record
	closed  bool
}

// newStore opens file and replays every intact frame. A torn frame at the tail
// (short write, failed checksum or garbage) ends the log: the file is
// truncated back to the last valid frame, matching what an OS crash could
// expose after a torn page write.
func newStore(file durableFile) (*Store, error) {
	s := &Store{file: file}
	size, err := s.fileSize()
	if err != nil {
		return nil, err
	}
	valid := int64(0)
	buf := make([]byte, maxFrameLen)
	for valid < size {
		remaining := size - valid
		if remaining < headerLen {
			break // torn header
		}
		if _, err := file.ReadAt(buf[:headerLen], valid); err != nil {
			return nil, err
		}
		var magic [4]byte
		copy(magic[:], buf[:4])
		if magic != frameMagic {
			break // torn/garbled frame
		}
		bodyLen := int(binary.LittleEndian.Uint32(buf[4:headerLen]))
		if bodyLen == 0 || bodyLen > maxBodyLen {
			break
		}
		frameLen := int64(headerLen + bodyLen + footerLen)
		if remaining < frameLen {
			break // torn body/footer
		}
		if _, err := file.ReadAt(buf[:frameLen], valid); err != nil {
			return nil, err
		}
		wantCRC := binary.LittleEndian.Uint32(buf[frameLen-footerLen : frameLen])
		gotCRC := crc32.Checksum(buf[:headerLen+bodyLen], crcTable)
		if wantCRC != gotCRC {
			break // torn bytes inside the frame
		}
		rec, err := decodeBody(buf[headerLen : headerLen+bodyLen])
		if err != nil {
			break
		}
		s.records = append(s.records, rec)
		valid += frameLen
	}
	if valid != size {
		if err := file.Truncate(valid); err != nil {
			return nil, err
		}
		if err := file.Sync(); err != nil {
			return nil, err
		}
	}
	// Replay only used ReadAt; position append writes after the last valid
	// frame so the next reservation never overwrites replayed data.
	if _, err := file.Seek(valid, io.SeekStart); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) fileSize() (int64, error) {
	type statter interface{ Stat() (os.FileInfo, error) }
	st, ok := s.file.(statter)
	if !ok {
		return 0, errors.New("noncelease: storage backend cannot report size")
	}
	info, err := st.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// append durably persists rec. The frame is fully written and fsynced before
// returning; any I/O failure marks the store closed so callers cannot mistake
// an unknown persistence outcome for success.
func (s *Store) append(rec record) error {
	body, err := rec.encodeBody()
	if err != nil {
		return err
	}
	frame := make([]byte, headerLen+len(body)+footerLen)
	copy(frame[0:4], frameMagic[:])
	binary.LittleEndian.PutUint32(frame[4:headerLen], uint32(len(body)))
	copy(frame[headerLen:headerLen+len(body)], body)
	csum := crc32.Checksum(frame[:headerLen+len(body)], crcTable)
	binary.LittleEndian.PutUint32(frame[len(frame)-footerLen:], csum)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if _, err := s.file.Write(frame); err != nil {
		s.closed = true
		return err
	}
	if err := s.file.Sync(); err != nil {
		s.closed = true
		return err
	}
	s.records = append(s.records, rec)
	return nil
}

// Records returns the replayed log. The returned slice must not be modified.
func (s *Store) Records() []record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records
}

// Close flushes the underlying file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.file.Close()
}

// osFile wraps *os.File to satisfy durableFile (Seek is used only at open).
type osFile struct{ f *os.File }

func (o osFile) Write(p []byte) (int, error) { return o.f.Write(p) }
func (o osFile) ReadAt(p []byte, off int64) (int, error) {
	return o.f.ReadAt(p, off)
}
func (o osFile) Close() error { return o.f.Close() }
func (o osFile) Name() string { return o.f.Name() }
func (o osFile) Seek(off int64, whence int) (int64, error) {
	return o.f.Seek(off, whence)
}
func (o osFile) Sync() error                { return o.f.Sync() }
func (o osFile) Truncate(n int64) error     { return o.f.Truncate(n) }
func (o osFile) Stat() (os.FileInfo, error) { return o.f.Stat() }

// OpenFileStore opens (creating if needed) the WAL file at path.
func OpenFileStore(path string) (*Store, error) {
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		created = true
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	store, err := newStore(osFile{f: f})
	if err != nil {
		f.Close()
		return nil, err
	}
	if created {
		if d, err := os.Open(filepath.Dir(path)); err == nil {
			err = d.Sync()
			d.Close()
			if err != nil {
				f.Close()
				return nil, err
			}
		}
	}
	return store, nil
}
