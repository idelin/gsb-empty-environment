package lsm

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
)

const sstableMagic = "SST1"

// SSTable 是落盘后的有序不可变数据块。
// 文件格式:magic | entry 总数(uvarint) | 按 Key 排序的 Entry 序列。
// 每条 Entry:seq(uvarint) | flags(byte, bit0=tombstone) |
//             keyLen(uvarint) | key | valueLen(uvarint) | value
type SSTable struct {
	path    string
	id      uint64
	entries int
}

// writeSSTable 将已按 Key 排序的 entries 原子写入 path。
func writeSSTable(path string, id uint64, entries []Entry) (*SSTable, error) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriter(f)
	if _, err := w.WriteString(sstableMagic); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(entries)))
	if _, err := w.Write(buf[:n]); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	for _, e := range entries {
		if err := writeEntry(w, &buf, e); err != nil {
			f.Close()
			os.Remove(tmp)
			return nil, err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return &SSTable{path: path, id: id, entries: len(entries)}, nil
}

func writeEntry(w *bufio.Writer, buf *[binary.MaxVarintLen64]byte, e Entry) error {
	n := binary.PutUvarint(buf[:], e.Seq)
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	var flags byte
	if e.Tombstone {
		flags = 1
	}
	if err := w.WriteByte(flags); err != nil {
		return err
	}
	n = binary.PutUvarint(buf[:], uint64(len(e.Key)))
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	if _, err := w.WriteString(e.Key); err != nil {
		return err
	}
	n = binary.PutUvarint(buf[:], uint64(len(e.Value)))
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	_, err := w.Write(e.Value)
	return err
}

func openSSTable(path string, id uint64) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	magic := make([]byte, len(sstableMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != sstableMagic {
		return nil, ErrCorrupt
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	return &SSTable{path: path, id: id, entries: int(count)}, nil
}

// Remove 物理删除底层文件。
func (t *SSTable) Remove() error { return os.Remove(t.path) }

// Get 在表内顺序查找 key(文件按 Key 有序,可提前终止)。
func (t *SSTable) Get(key string) (Entry, bool, error) {
	it, err := t.Iterator()
	if err != nil {
		return Entry{}, false, err
	}
	defer it.Close()
	for it.Next() {
		e := it.Entry()
		if e.Key == key {
			return e, true, nil
		}
		if e.Key > key {
			break
		}
	}
	return Entry{}, false, nil
}

// Iterator 返回流式迭代器,用毕需 Close。
func (t *SSTable) Iterator() (entryIterator, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	magic := make([]byte, len(sstableMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		f.Close()
		return nil, err
	}
	if string(magic) != sstableMagic {
		f.Close()
		return nil, ErrCorrupt
	}
	if _, err := binary.ReadUvarint(r); err != nil {
		f.Close()
		return nil, err
	}
	return &sstableIterator{f: f, r: r}, nil
}

type sstableIterator struct {
	f   *os.File
	r   *bufio.Reader
	cur Entry
	err error
}

func (it *sstableIterator) Next() bool {
	if it.err != nil {
		return false
	}
	e, err := readEntry(it.r)
	if err != nil {
		if err != io.EOF {
			it.err = err
		}
		return false
	}
	it.cur = e
	return true
}

func (it *sstableIterator) Entry() Entry { return it.cur }

func (it *sstableIterator) Close() error { return it.f.Close() }

func readEntry(r *bufio.Reader) (Entry, error) {
	var e Entry
	seq, err := binary.ReadUvarint(r)
	if err != nil {
		return e, err
	}
	e.Seq = seq
	flags, err := r.ReadByte()
	if err != nil {
		return e, err
	}
	e.Tombstone = flags&1 == 1
	klen, err := binary.ReadUvarint(r)
	if err != nil {
		return e, err
	}
	key := make([]byte, klen)
	if _, err := io.ReadFull(r, key); err != nil {
		return e, err
	}
	e.Key = string(key)
	vlen, err := binary.ReadUvarint(r)
	if err != nil {
		return e, err
	}
	if vlen > 0 {
		val := make([]byte, vlen)
		if _, err := io.ReadFull(r, val); err != nil {
			return e, err
		}
		e.Value = val
	}
	return e, nil
}
