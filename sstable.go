package lsm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// SSTable file layout (all integers little-endian):
//
//	[data entries]   klen u32 | vlen u32 | key | val   (ascending key order)
//	[index entries]  klen u32 | key | offset u64       (one per indexInterval data entries)
//	[footer]         indexOffset u64 | indexCount u32 | magic u64
const (
	sstMagic      uint64 = 0x4C534D5353543031 // "LSMSST01"
	indexInterval        = 16
	sstFooterSize        = 20
)

type sstIndexEntry struct {
	key    []byte
	offset uint64
}

func sstFilePath(dir string, id uint64) string {
	return filepath.Join(dir, fmt.Sprintf("L0-%06d.sst", id))
}

// writeSSTable streams all entries from it into a new SSTable file and
// fsyncs it. It returns the number of entries written.
func writeSSTable(path string, it *slIterator) (int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriter(f)

	var index []sstIndexEntry
	var off uint64
	count := 0
	var hdr [8]byte
	for it.Valid() {
		if count%indexInterval == 0 {
			index = append(index, sstIndexEntry{key: bytes.Clone(it.Key()), offset: off})
		}
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(it.Key())))
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(it.Val())))
		if _, err := w.Write(hdr[:]); err != nil {
			f.Close()
			return 0, err
		}
		if _, err := w.Write(it.Key()); err != nil {
			f.Close()
			return 0, err
		}
		if _, err := w.Write(it.Val()); err != nil {
			f.Close()
			return 0, err
		}
		off += 8 + uint64(len(it.Key())) + uint64(len(it.Val()))
		count++
		it.Next()
	}

	indexOff := off
	for _, e := range index {
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(e.key)))
		if _, err := w.Write(hdr[0:4]); err != nil {
			f.Close()
			return 0, err
		}
		if _, err := w.Write(e.key); err != nil {
			f.Close()
			return 0, err
		}
		binary.LittleEndian.PutUint64(hdr[:8], e.offset)
		if _, err := w.Write(hdr[:8]); err != nil {
			f.Close()
			return 0, err
		}
	}

	var footer [sstFooterSize]byte
	binary.LittleEndian.PutUint64(footer[0:8], indexOff)
	binary.LittleEndian.PutUint32(footer[8:12], uint32(len(index)))
	binary.LittleEndian.PutUint64(footer[12:20], sstMagic)
	if _, err := w.Write(footer[:]); err != nil {
		f.Close()
		return 0, err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, err
	}
	return count, f.Close()
}

// SSTable is a read handle for one immutable Level-0 file.
type SSTable struct {
	path    string
	id      uint64
	f       *os.File
	index   []sstIndexEntry
	dataEnd uint64
}

func openSSTable(path string, id uint64) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() < sstFooterSize {
		f.Close()
		return nil, fmt.Errorf("sstable %s too small", path)
	}
	var footer [sstFooterSize]byte
	if _, err := f.ReadAt(footer[:], st.Size()-sstFooterSize); err != nil {
		f.Close()
		return nil, err
	}
	if binary.LittleEndian.Uint64(footer[12:20]) != sstMagic {
		f.Close()
		return nil, fmt.Errorf("sstable %s: bad magic", path)
	}
	indexOff := binary.LittleEndian.Uint64(footer[0:8])
	indexCount := binary.LittleEndian.Uint32(footer[8:12])

	t := &SSTable{path: path, id: id, f: f, dataEnd: indexOff}
	off := int64(indexOff)
	for i := uint32(0); i < indexCount; i++ {
		var klenBuf [4]byte
		if _, err := f.ReadAt(klenBuf[:], off); err != nil {
			f.Close()
			return nil, err
		}
		off += 4
		klen := binary.LittleEndian.Uint32(klenBuf[:])
		key := make([]byte, klen)
		if _, err := f.ReadAt(key, off); err != nil {
			f.Close()
			return nil, err
		}
		off += int64(klen)
		var offBuf [8]byte
		if _, err := f.ReadAt(offBuf[:], off); err != nil {
			f.Close()
			return nil, err
		}
		off += 8
		t.index = append(t.index, sstIndexEntry{key: key, offset: binary.LittleEndian.Uint64(offBuf[:])})
	}
	return t, nil
}

func (t *SSTable) Close() error { return t.f.Close() }

// readEntry parses the data entry at offset, returning key, value and the
// offset of the next entry.
func (t *SSTable) readEntry(off uint64) (key, val []byte, next uint64, err error) {
	var hdr [8]byte
	if _, err = t.f.ReadAt(hdr[:], int64(off)); err != nil {
		return nil, nil, 0, err
	}
	klen := binary.LittleEndian.Uint32(hdr[0:4])
	vlen := binary.LittleEndian.Uint32(hdr[4:8])
	payload := make([]byte, int(klen)+int(vlen))
	if _, err = t.f.ReadAt(payload, int64(off)+8); err != nil {
		return nil, nil, 0, err
	}
	return payload[:klen], payload[klen:], off + 8 + uint64(klen) + uint64(vlen), nil
}

// Get looks up key using the sparse in-memory index plus a short scan.
func (t *SSTable) Get(key []byte) ([]byte, bool, error) {
	if len(t.index) == 0 {
		return nil, false, nil
	}
	idx := sort.Search(len(t.index), func(i int) bool {
		return bytes.Compare(t.index[i].key, key) > 0
	}) - 1
	if idx < 0 {
		return nil, false, nil
	}
	off := t.index[idx].offset
	for i := 0; i < indexInterval && off < t.dataEnd; i++ {
		k, v, next, err := t.readEntry(off)
		if err != nil {
			return nil, false, err
		}
		switch cmp := bytes.Compare(k, key); {
		case cmp == 0:
			return v, true, nil
		case cmp > 0:
			return nil, false, nil
		}
		off = next
	}
	return nil, false, nil
}

// Iterate walks every entry in ascending key order. Returning false from fn
// stops the walk early.
func (t *SSTable) Iterate(fn func(key, val []byte) bool) error {
	off := uint64(0)
	for off < t.dataEnd {
		k, v, next, err := t.readEntry(off)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !fn(k, v) {
			return nil
		}
		off = next
	}
	return nil
}
