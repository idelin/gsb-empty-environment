// Package stcs implements a size-tiered compaction strategy (STCS)
// optimizer for an LSM-tree write path: similarly sized SSTables
// accumulate in a tier, and once N of them pile up they are merged
// into a single larger SSTable via a full k-way merge.
package stcs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

var (
	headerMagic = []byte("STCSST01")
	footerMagic = []byte("STCSEND1")
)

// Entry is a single key/value record. Seq is a monotonically increasing
// sequence number assigned by the engine; the highest Seq wins when the
// same key appears multiple times.
type Entry struct {
	Key   []byte
	Value []byte
	Seq   uint64
}

// Table describes an immutable SSTable stored on disk.
type Table struct {
	Path     string
	Size     int64
	Count    uint64
	MinSeq   uint64
	MaxSeq   uint64
	FirstKey []byte
	LastKey  []byte
}

// TableWriter streams sorted entries into a new SSTable file.
//
// File layout:
//
//	header:  8-byte magic
//	records: [seq u64][keyLen u32][valLen u32][key][value] ...
//	footer:  [count u64][minSeq u64][maxSeq u64]
//	         [firstKeyLen u32][firstKey][lastKeyLen u32][lastKey]
//	         [footerLen u32][8-byte magic]
type TableWriter struct {
	f        *os.File
	w        *bufio.Writer
	count    uint64
	size     int64
	minSeq   uint64
	maxSeq   uint64
	firstKey []byte
	lastKey  []byte
}

// NewTableWriter creates a writer that will produce an SSTable at path.
func NewTableWriter(path string) (*TableWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	tw := &TableWriter{f: f, w: bufio.NewWriterSize(f, 1<<20)}
	if err := tw.writeRaw(headerMagic); err != nil {
		f.Close()
		return nil, err
	}
	return tw, nil
}

func (tw *TableWriter) writeRaw(p []byte) error {
	n, err := tw.w.Write(p)
	tw.size += int64(n)
	return err
}

// Add appends an entry. Keys must be added in strictly increasing order.
func (tw *TableWriter) Add(e Entry) error {
	if len(e.Key) == 0 {
		return fmt.Errorf("stcs: empty key")
	}
	if tw.count > 0 && bytes.Compare(tw.lastKey, e.Key) >= 0 {
		return fmt.Errorf("stcs: keys out of order: %q after %q", e.Key, tw.lastKey)
	}
	var hdr [16]byte
	binary.BigEndian.PutUint64(hdr[0:8], e.Seq)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(e.Key)))
	binary.BigEndian.PutUint32(hdr[12:16], uint32(len(e.Value)))
	if err := tw.writeRaw(hdr[:]); err != nil {
		return err
	}
	if err := tw.writeRaw(e.Key); err != nil {
		return err
	}
	if err := tw.writeRaw(e.Value); err != nil {
		return err
	}
	if tw.count == 0 {
		tw.firstKey = append([]byte(nil), e.Key...)
		tw.minSeq = e.Seq
	} else if e.Seq < tw.minSeq {
		tw.minSeq = e.Seq
	}
	if e.Seq > tw.maxSeq {
		tw.maxSeq = e.Seq
	}
	tw.lastKey = append(tw.lastKey[:0], e.Key...)
	tw.count++
	return nil
}

// Finish writes the footer, fsyncs and closes the file, and returns the
// table metadata.
func (tw *TableWriter) Finish() (*Table, error) {
	var footer bytes.Buffer
	var fixed [24]byte
	binary.BigEndian.PutUint64(fixed[0:8], tw.count)
	binary.BigEndian.PutUint64(fixed[8:16], tw.minSeq)
	binary.BigEndian.PutUint64(fixed[16:24], tw.maxSeq)
	footer.Write(fixed[:])
	writeLenPrefixed(&footer, tw.firstKey)
	writeLenPrefixed(&footer, tw.lastKey)
	var tail [12]byte
	binary.BigEndian.PutUint32(tail[0:4], uint32(footer.Len()))
	copy(tail[4:12], footerMagic)
	footer.Write(tail[:])
	if err := tw.writeRaw(footer.Bytes()); err != nil {
		tw.f.Close()
		return nil, err
	}
	if err := tw.w.Flush(); err != nil {
		tw.f.Close()
		return nil, err
	}
	if err := tw.f.Sync(); err != nil {
		tw.f.Close()
		return nil, err
	}
	if err := tw.f.Close(); err != nil {
		return nil, err
	}
	return &Table{
		Path:     tw.f.Name(),
		Size:     tw.size,
		Count:    tw.count,
		MinSeq:   tw.minSeq,
		MaxSeq:   tw.maxSeq,
		FirstKey: tw.firstKey,
		LastKey:  tw.lastKey,
	}, nil
}

func writeLenPrefixed(buf *bytes.Buffer, b []byte) {
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(b)))
	buf.Write(lb[:])
	buf.Write(b)
}

// OpenTable reads the footer of an existing SSTable and returns its metadata.
func OpenTable(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size < int64(len(headerMagic)+12) {
		return nil, fmt.Errorf("stcs: %s: file too small", path)
	}
	var tail [12]byte
	if _, err := f.ReadAt(tail[:], size-12); err != nil {
		return nil, err
	}
	if !bytes.Equal(tail[4:12], footerMagic) {
		return nil, fmt.Errorf("stcs: %s: bad footer magic", path)
	}
	footerLen := int64(binary.BigEndian.Uint32(tail[0:4]))
	if footerLen < 24 || footerLen > size {
		return nil, fmt.Errorf("stcs: %s: bad footer length", path)
	}
	footer := make([]byte, footerLen)
	if _, err := f.ReadAt(footer, size-12-footerLen); err != nil {
		return nil, err
	}
	t := &Table{Path: path, Size: size}
	t.Count = binary.BigEndian.Uint64(footer[0:8])
	t.MinSeq = binary.BigEndian.Uint64(footer[8:16])
	t.MaxSeq = binary.BigEndian.Uint64(footer[16:24])
	rest := footer[24:]
	var ok bool
	if t.FirstKey, rest, ok = readLenPrefixed(rest); !ok {
		return nil, fmt.Errorf("stcs: %s: corrupt footer", path)
	}
	if t.LastKey, _, ok = readLenPrefixed(rest); !ok {
		return nil, fmt.Errorf("stcs: %s: corrupt footer", path)
	}
	return t, nil
}

func readLenPrefixed(b []byte) (out, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b[0:4])
	if uint64(len(b)-4) < uint64(n) {
		return nil, nil, false
	}
	out = make([]byte, n)
	copy(out, b[4:4+n])
	return out, b[4+n:], true
}

// Iterator streams the entries of a table in key order.
type Iterator struct {
	f         *os.File
	r         *bufio.Reader
	remaining uint64
	entry     Entry
	err       error
}

// Iterator opens a streaming iterator over the table's records.
func (t *Table) Iterator() (*Iterator, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		return nil, err
	}
	magic := make([]byte, len(headerMagic))
	if _, err := io.ReadFull(f, magic); err != nil {
		f.Close()
		return nil, err
	}
	if !bytes.Equal(magic, headerMagic) {
		f.Close()
		return nil, fmt.Errorf("stcs: %s: bad header magic", t.Path)
	}
	return &Iterator{f: f, r: bufio.NewReaderSize(f, 1<<20), remaining: t.Count}, nil
}

// Next advances to the next entry; returns false at EOF or on error.
func (it *Iterator) Next() bool {
	if it.err != nil || it.remaining == 0 {
		return false
	}
	var hdr [16]byte
	if _, err := io.ReadFull(it.r, hdr[:]); err != nil {
		it.err = err
		return false
	}
	key := make([]byte, binary.BigEndian.Uint32(hdr[8:12]))
	val := make([]byte, binary.BigEndian.Uint32(hdr[12:16]))
	if _, err := io.ReadFull(it.r, key); err != nil {
		it.err = err
		return false
	}
	if _, err := io.ReadFull(it.r, val); err != nil {
		it.err = err
		return false
	}
	it.entry = Entry{Key: key, Value: val, Seq: binary.BigEndian.Uint64(hdr[0:8])}
	it.remaining--
	return true
}

// Entry returns the current entry.
func (it *Iterator) Entry() Entry { return it.entry }

// Err returns the first error encountered while iterating.
func (it *Iterator) Err() error { return it.err }

// Close releases the underlying file handle.
func (it *Iterator) Close() error {
	err := it.f.Close()
	if it.err != nil {
		return it.err
	}
	return err
}
