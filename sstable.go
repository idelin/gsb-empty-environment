package lsm

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OpType 标记一条记录是写入还是删除（Tombstone）。
type OpType byte

const (
	OpPut    OpType = 1
	OpDelete OpType = 2
)

// Entry 是 LSM 中的一条记录，Seq 越大版本越新。
type Entry struct {
	Seq   uint64
	Op    OpType
	Key   string
	Value []byte
}

var sstMagic = []byte{'S', 'S', 'T', '1'}

// SSTable 是一个磁盘上的有序不可变文件及其元数据。
type SSTable struct {
	ID     int
	Path   string
	Level  int
	Size   int64
	MinKey string
	MaxKey string
}

// Contains 判断 key 是否可能落在本文件的 Key 范围内。
func (t *SSTable) Contains(key string) bool {
	return key >= t.MinKey && key <= t.MaxKey
}

// Overlaps 判断本文件的 Key 范围是否与 [lo, hi] 有交集。
func (t *SSTable) Overlaps(lo, hi string) bool {
	return t.MinKey <= hi && lo <= t.MaxKey
}

func (t *SSTable) String() string {
	return fmt.Sprintf("L%d#%06d[%s..%s](%dB)", t.Level, t.ID, t.MinKey, t.MaxKey, t.Size)
}

// writeSSTable 将已按 Key 排序的 entries 写入磁盘，返回文件元数据。
//
// 文件格式（小端）：
//
//	magic(4B) | count(uint32) | [ seq(uint64) op(byte) klen(uint32) key vlen(uint32) value ]...
func writeSSTable(dir string, id, level int, entries []Entry) (*SSTable, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("sstable: no entries to write")
	}
	path := filepath.Join(dir, fmt.Sprintf("%06d.sst", id))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	if _, err := w.Write(sstMagic); err != nil {
		return nil, err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(entries))); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, e.Seq); err != nil {
			return nil, err
		}
		if err := w.WriteByte(byte(e.Op)); err != nil {
			return nil, err
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(e.Key))); err != nil {
			return nil, err
		}
		if _, err := w.WriteString(e.Key); err != nil {
			return nil, err
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(e.Value))); err != nil {
			return nil, err
		}
		if _, err := w.Write(e.Value); err != nil {
			return nil, err
		}
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &SSTable{
		ID:     id,
		Path:   path,
		Level:  level,
		Size:   info.Size(),
		MinKey: entries[0].Key,
		MaxKey: entries[len(entries)-1].Key,
	}, nil
}

// readSSTable 读取一个 SSTable 文件中的全部记录。
func readSSTable(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != string(sstMagic) {
		return nil, fmt.Errorf("sstable: bad magic in %s", path)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, count)
	for i := uint32(0); i < count; i++ {
		var e Entry
		if err := binary.Read(r, binary.LittleEndian, &e.Seq); err != nil {
			return nil, err
		}
		op, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		e.Op = OpType(op)
		var klen uint32
		if err := binary.Read(r, binary.LittleEndian, &klen); err != nil {
			return nil, err
		}
		key := make([]byte, klen)
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, err
		}
		e.Key = string(key)
		var vlen uint32
		if err := binary.Read(r, binary.LittleEndian, &vlen); err != nil {
			return nil, err
		}
		e.Value = make([]byte, vlen)
		if _, err := io.ReadFull(r, e.Value); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}
