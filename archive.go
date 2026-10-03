package bitmapckpt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// Archive is an append-only file that persists checkpoints and incremental
// snapshots. Layout:
//
//	magic ("RBARC001")
//	record*: type(1) | payloadLen uint32 | payload | crc32(payload) uint32
//
// Checkpoint payload: gen uint64 | n uint32 | n containers
// Snapshot  payload: gen uint64 | base uint64 | n uint32 | n containers
// Container encoding: key uint64 | type byte | type-specific data
const archiveMagic = "RBARC001"

const (
	recCheckpoint  byte = 1
	recIncremental byte = 2
)

// ErrClosed is returned when operating on a closed archive.
var ErrClosed = errors.New("bitmapckpt: archive already closed")

// Archive appends snapshots and checkpoints to a single file. Safe for
// concurrent use.
type Archive struct {
	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	closed bool
}

// OpenArchive creates (or truncates) an archive file at path.
func OpenArchive(path string) (*Archive, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	a := &Archive{f: f, w: bufio.NewWriterSize(f, 1<<16)}
	if _, err := a.w.WriteString(archiveMagic); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

// AppendCheckpoint writes a full checkpoint record.
func (a *Archive) AppendCheckpoint(cp *Checkpoint) error {
	buf := &bytes.Buffer{}
	writeU64(buf, cp.Gen)
	writeU32(buf, uint32(len(cp.Cs)))
	if err := writeContainers(buf, cp.Cs); err != nil {
		return err
	}
	return a.writeRecord(recCheckpoint, buf.Bytes())
}

// AppendSnapshot writes an incremental snapshot record.
func (a *Archive) AppendSnapshot(s *Snapshot) error {
	buf := &bytes.Buffer{}
	writeU64(buf, s.Gen)
	writeU64(buf, s.Base)
	writeU32(buf, uint32(len(s.Cs)))
	if err := writeContainers(buf, s.Cs); err != nil {
		return err
	}
	return a.writeRecord(recIncremental, buf.Bytes())
}

func writeContainers(buf *bytes.Buffer, cs map[uint64]container) error {
	for k, c := range cs {
		if err := writeU64(buf, k); err != nil {
			return err
		}
		buf.WriteByte(byte(c.typeID()))
		if err := c.writeTo(buf); err != nil {
			return err
		}
	}
	return nil
}

func (a *Archive) writeRecord(typ byte, payload []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if err := a.w.WriteByte(typ); err != nil {
		return err
	}
	if err := writeU32(a.w, uint32(len(payload))); err != nil {
		return err
	}
	if _, err := a.w.Write(payload); err != nil {
		return err
	}
	return writeU32(a.w, crc32.ChecksumIEEE(payload))
}

// Sync flushes buffered data and fsyncs the underlying file.
func (a *Archive) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if err := a.w.Flush(); err != nil {
		return err
	}
	return a.f.Sync()
}

// Close flushes and closes the archive. It is idempotent in effect: any
// further operation returns ErrClosed.
func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	a.closed = true
	if err := a.w.Flush(); err != nil {
		a.f.Close()
		return err
	}
	return a.f.Close()
}

// ReadArchive reads an archive file and returns the latest checkpoint plus
// the incremental snapshots recorded after it. Earlier records are skipped
// because the latest checkpoint already includes their state.
func ReadArchive(path string) (*Checkpoint, []*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	magic := make([]byte, len(archiveMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, nil, fmt.Errorf("bitmapckpt: reading magic: %v", err)
	}
	if string(magic) != archiveMagic {
		return nil, nil, errors.New("bitmapckpt: bad archive magic")
	}
	var cp *Checkpoint
	var snaps []*Snapshot
	for {
		typ, err := r.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		n, err := readU32(r)
		if err != nil {
			return nil, nil, fmt.Errorf("bitmapckpt: corrupt record header: %v", err)
		}
		if n > 1<<31 {
			return nil, nil, errors.New("bitmapckpt: record too large")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, nil, fmt.Errorf("bitmapckpt: truncated record: %v", err)
		}
		crc, err := readU32(r)
		if err != nil {
			return nil, nil, fmt.Errorf("bitmapckpt: truncated crc: %v", err)
		}
		if crc32.ChecksumIEEE(payload) != crc {
			return nil, nil, errors.New("bitmapckpt: crc mismatch, archive is corrupt")
		}
		br := bytes.NewReader(payload)
		switch typ {
		case recCheckpoint:
			gen, err := readU64(br)
			if err != nil {
				return nil, nil, err
			}
			cs, err := readContainerMap(br)
			if err != nil {
				return nil, nil, err
			}
			cp = &Checkpoint{Gen: gen, Cs: cs}
			snaps = nil
		case recIncremental:
			gen, err := readU64(br)
			if err != nil {
				return nil, nil, err
			}
			base, err := readU64(br)
			if err != nil {
				return nil, nil, err
			}
			cs, err := readContainerMap(br)
			if err != nil {
				return nil, nil, err
			}
			snaps = append(snaps, &Snapshot{Gen: gen, Base: base, Cs: cs})
		default:
			return nil, nil, fmt.Errorf("bitmapckpt: unknown record type %d", typ)
		}
	}
	return cp, snaps, nil
}

func readContainerMap(r io.Reader) (map[uint64]container, error) {
	n, err := readU32(r)
	if err != nil {
		return nil, err
	}
	if n > 1<<24 {
		return nil, errors.New("bitmapckpt: unreasonable container count")
	}
	cs := make(map[uint64]container, n)
	for i := uint32(0); i < n; i++ {
		key, err := readU64(r)
		if err != nil {
			return nil, err
		}
		t, err := readU8(r)
		if err != nil {
			return nil, err
		}
		c, err := readContainer(containerType(t), r)
		if err != nil {
			return nil, err
		}
		cs[key] = c
	}
	return cs, nil
}

// RestoreFromArchive reads an archive file and rebuilds the bitmap from the
// latest checkpoint plus the incremental snapshots that follow it.
func RestoreFromArchive(path string) (*Bitmap, error) {
	cp, snaps, err := ReadArchive(path)
	if err != nil {
		return nil, err
	}
	return Restore(cp, snaps...)
}
