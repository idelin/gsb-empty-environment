package lsm

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
)

// SSTable file layout (all integers big-endian):
//
//	magic   "SST1"                       4 bytes
//	count   uint64                       number of entries
//	entry*  seq uint64 | tombstone byte | keyLen uint32 | key | valLen uint32 | value
//
// Entries are stored sorted by key. For this kernel the entry list is also
// kept in memory; the file is the durable source of truth and is what
// compaction rewrites when tombstones are physically eliminated.

var sstMagic = []byte("SST1")

type SSTable struct {
	Path    string
	ID      uint64
	entries []Entry // sorted by Key
}

// WriteSSTable persists entries (must be sorted by key) to path.
func WriteSSTable(path string, id uint64, entries []Entry) (*SSTable, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriter(f)
	if _, err := w.Write(sstMagic); err != nil {
		f.Close()
		return nil, err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(entries)))
	if _, err := w.Write(buf[:]); err != nil {
		f.Close()
		return nil, err
	}
	for _, e := range entries {
		binary.BigEndian.PutUint64(buf[:], e.Seq)
		if _, err := w.Write(buf[:]); err != nil {
			f.Close()
			return nil, err
		}
		if e.Tombstone {
			w.WriteByte(1)
		} else {
			w.WriteByte(0)
		}
		binary.BigEndian.PutUint32(buf[:4], uint32(len(e.Key)))
		if _, err := w.Write(buf[:4]); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := w.WriteString(e.Key); err != nil {
			f.Close()
			return nil, err
		}
		binary.BigEndian.PutUint32(buf[:4], uint32(len(e.Value)))
		if _, err := w.Write(buf[:4]); err != nil {
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
	return &SSTable{Path: path, ID: id, entries: entries}, nil
}

// LoadSSTable reads an SSTable file back into memory.
func LoadSSTable(path string, id uint64) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	magic := make([]byte, len(sstMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != string(sstMagic) {
		return nil, errCorrupt
	}
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, err
	}
	count := binary.BigEndian.Uint64(buf[:])
	entries := make([]Entry, 0, count)
	for i := uint64(0); i < count; i++ {
		var e Entry
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		e.Seq = binary.BigEndian.Uint64(buf[:])
		tomb, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		e.Tombstone = tomb == 1
		if _, err := io.ReadFull(r, buf[:4]); err != nil {
			return nil, err
		}
		key := make([]byte, binary.BigEndian.Uint32(buf[:4]))
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, err
		}
		e.Key = string(key)
		if _, err := io.ReadFull(r, buf[:4]); err != nil {
			return nil, err
		}
		e.Value = make([]byte, binary.BigEndian.Uint32(buf[:4]))
		if _, err := io.ReadFull(r, e.Value); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return &SSTable{Path: path, ID: id, entries: entries}, nil
}

// Get returns the entry for key and whether it exists.
func (t *SSTable) Get(key string) (Entry, bool) {
	lo, hi := 0, len(t.entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if t.entries[mid].Key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.entries) && t.entries[lo].Key == key {
		return t.entries[lo], true
	}
	return Entry{}, false
}

func (t *SSTable) Entries() []Entry { return t.entries }

func (t *SSTable) Remove() error { return os.Remove(t.Path) }
