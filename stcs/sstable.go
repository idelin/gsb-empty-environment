// Package stcs implements a Size-Tiered Compaction Strategy (STCS)
// optimizer for an LSM-tree style storage engine.
//
// SSTables flushed from the memtable land in tier 0. Whenever a tier
// accumulates Fanout tables of similar size, they are merged in a single
// multi-way k-merge into one larger SSTable placed in the next tier.
// Duplicate keys keep only the newest version (highest sequence number).
package stcs

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
)

// Entry is a single key/value record. Seq is a monotonically increasing
// sequence number; a larger Seq wins when the same Key appears in
// multiple SSTables. Tombstone marks a deleted key.
type Entry struct {
	Key       []byte
	Value     []byte
	Seq       uint64
	Tombstone bool
}

const (
	sstMagic   = "STCSST01"
	flagNormal = byte(0)
	flagTomb   = byte(1)
)

// SSTable is an immutable sorted run stored on disk.
type SSTable struct {
	Path    string
	Size    int64
	Entries int
	Tier    int
}

// WriteSSTable sorts entries by (Key, Seq desc) and writes them to path.
// It returns a handle describing the created table.
func WriteSSTable(path string, tier int, entries []Entry) (*SSTable, error) {
	sort.Slice(entries, func(i, j int) bool {
		c := compareBytes(entries[i].Key, entries[j].Key)
		if c != 0 {
			return c < 0
		}
		return entries[i].Seq > entries[j].Seq
	})

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	if _, err := w.WriteString(sstMagic); err != nil {
		f.Close()
		return nil, err
	}
	var hdr [8]byte
	binary.BigEndian.PutUint64(hdr[:], uint64(len(entries)))
	if _, err := w.Write(hdr[:]); err != nil {
		f.Close()
		return nil, err
	}
	for i := range entries {
		e := &entries[i]
		flag := flagNormal
		if e.Tombstone {
			flag = flagTomb
		}
		if err := w.WriteByte(flag); err != nil {
			f.Close()
			return nil, err
		}
		var lens [16]byte
		binary.BigEndian.PutUint64(lens[0:8], e.Seq)
		binary.BigEndian.PutUint32(lens[8:12], uint32(len(e.Key)))
		binary.BigEndian.PutUint32(lens[12:16], uint32(len(e.Value)))
		if _, err := w.Write(lens[:]); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := w.Write(e.Key); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := w.Write(e.Value); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &SSTable{Path: path, Size: st.Size(), Entries: len(entries), Tier: tier}, nil
}

// Iterator streams entries from an SSTable in sorted order.
type Iterator struct {
	f         *os.File
	r         *bufio.Reader
	remaining int
	entry     Entry
	err       error
	started   bool
}

// OpenIterator opens a streaming iterator over the SSTable at path.
func OpenIterator(path string) (*Iterator, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	magic := make([]byte, len(sstMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		f.Close()
		return nil, fmt.Errorf("read magic: %v", err)
	}
	if string(magic) != sstMagic {
		f.Close()
		return nil, fmt.Errorf("bad magic in %s", path)
	}
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("read header: %v", err)
	}
	return &Iterator{f: f, r: r, remaining: int(binary.BigEndian.Uint64(hdr[:]))}, nil
}

// Next advances the iterator. Returns false at EOF or on error (see Err).
func (it *Iterator) Next() bool {
	if it.err != nil || it.remaining == 0 {
		return false
	}
	flag, err := it.r.ReadByte()
	if err != nil {
		it.err = err
		return false
	}
	var lens [16]byte
	if _, err := io.ReadFull(it.r, lens[:]); err != nil {
		it.err = err
		return false
	}
	seq := binary.BigEndian.Uint64(lens[0:8])
	klen := binary.BigEndian.Uint32(lens[8:12])
	vlen := binary.BigEndian.Uint32(lens[12:16])
	key := make([]byte, klen)
	val := make([]byte, vlen)
	if _, err := io.ReadFull(it.r, key); err != nil {
		it.err = err
		return false
	}
	if _, err := io.ReadFull(it.r, val); err != nil {
		it.err = err
		return false
	}
	it.entry = Entry{Key: key, Value: val, Seq: seq, Tombstone: flag == flagTomb}
	it.remaining--
	it.started = true
	return true
}

// Entry returns the current entry. Valid only after Next returned true.
func (it *Iterator) Entry() Entry { return it.entry }

// Err returns the first error encountered during iteration.
func (it *Iterator) Err() error { return it.err }

// Close releases the underlying file handle.
func (it *Iterator) Close() error { return it.f.Close() }

func compareBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
