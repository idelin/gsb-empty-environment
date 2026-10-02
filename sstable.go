package lsm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

// sstMagic 是 SSTable 文件尾魔数 "LSMSST01"。
const sstMagic uint64 = 0x4C53534D53535430

// indexInterval 是稀疏索引间隔: 每 64 条记录索引一次。
const indexInterval = 64

// SSTable 文件布局:
//
//	数据区: [klen(4) vlen(4) flags(1) key value] * N   (按 key 有序)
//	索引区: [klen(4) offset(8) key] * M                (稀疏索引)
//	脚注:   indexOffset(8) | magic(8)
const sstEntryHeaderSize = 4 + 4 + 1

var errBadSSTable = errors.New("lsm: invalid sstable file")

type indexEntry struct {
	key    string
	offset uint64
}

// writeSSTable 将排序好的记录写入 SSTable: 先写临时文件，fsync 后原子改名，
// 保证任何时刻 L0 层出现的都是完整文件。
func writeSSTable(path string, items []kv) (err error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	w := bufio.NewWriterSize(f, 1<<20)
	var index []indexEntry
	var offset uint64
	var hdr [sstEntryHeaderSize]byte
	for i, item := range items {
		if i%indexInterval == 0 {
			index = append(index, indexEntry{key: item.key, offset: offset})
		}
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(item.key)))
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(item.e.value)))
		if item.e.deleted {
			hdr[8] = 1
		} else {
			hdr[8] = 0
		}
		if _, err = w.Write(hdr[:]); err != nil {
			return err
		}
		if _, err = w.WriteString(item.key); err != nil {
			return err
		}
		if _, err = w.Write(item.e.value); err != nil {
			return err
		}
		offset += sstEntryHeaderSize + uint64(len(item.key)) + uint64(len(item.e.value))
	}

	indexOffset := offset
	var ihdr [12]byte
	for _, ie := range index {
		binary.LittleEndian.PutUint32(ihdr[0:4], uint32(len(ie.key)))
		binary.LittleEndian.PutUint64(ihdr[4:12], ie.offset)
		if _, err = w.Write(ihdr[:]); err != nil {
			return err
		}
		if _, err = w.WriteString(ie.key); err != nil {
			return err
		}
	}

	var footer [16]byte
	binary.LittleEndian.PutUint64(footer[0:8], indexOffset)
	binary.LittleEndian.PutUint64(footer[8:16], sstMagic)
	if _, err = w.Write(footer[:]); err != nil {
		return err
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sstable 是一个已打开的 Level 0 SSTable 文件。
type sstable struct {
	path    string
	f       *os.File
	index   []indexEntry
	dataEnd uint64 // 数据区结束(索引区开始)偏移
}

func openSSTable(path string) (*sstable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := fi.Size()
	if size < 16 {
		f.Close()
		return nil, errBadSSTable
	}
	var footer [16]byte
	if _, err := f.ReadAt(footer[:], size-16); err != nil {
		f.Close()
		return nil, err
	}
	if binary.LittleEndian.Uint64(footer[8:16]) != sstMagic {
		f.Close()
		return nil, errBadSSTable
	}
	indexOffset := binary.LittleEndian.Uint64(footer[0:8])
	if indexOffset > uint64(size)-16 {
		f.Close()
		return nil, errBadSSTable
	}
	raw := make([]byte, uint64(size)-16-indexOffset)
	if _, err := f.ReadAt(raw, int64(indexOffset)); err != nil {
		f.Close()
		return nil, err
	}
	var index []indexEntry
	for len(raw) > 0 {
		if len(raw) < 12 {
			f.Close()
			return nil, errBadSSTable
		}
		klen := binary.LittleEndian.Uint32(raw[0:4])
		off := binary.LittleEndian.Uint64(raw[4:12])
		raw = raw[12:]
		if uint64(len(raw)) < uint64(klen) {
			f.Close()
			return nil, errBadSSTable
		}
		index = append(index, indexEntry{key: string(raw[:klen]), offset: off})
		raw = raw[klen:]
	}
	return &sstable{path: path, f: f, index: index, dataEnd: indexOffset}, nil
}

func (s *sstable) close() error { return s.f.Close() }

// get 在 SSTable 中查找 key: 稀疏索引二分定位所在块, 单次 ReadAt 读入整块后在内存中扫描。
func (s *sstable) get(key string) (entry, bool, error) {
	i := sort.Search(len(s.index), func(i int) bool { return s.index[i].key > key }) - 1
	if i < 0 {
		return entry{}, false, nil
	}
	end := s.dataEnd
	if i+1 < len(s.index) {
		end = s.index[i+1].offset
	}
	blk := make([]byte, end-s.index[i].offset)
	if _, err := s.f.ReadAt(blk, int64(s.index[i].offset)); err != nil {
		return entry{}, false, err
	}
	for len(blk) >= sstEntryHeaderSize {
		klen := binary.LittleEndian.Uint32(blk[0:4])
		vlen := binary.LittleEndian.Uint32(blk[4:8])
		deleted := blk[8] == 1
		if uint64(klen) > maxRecordPayload || uint64(vlen) > maxRecordPayload {
			return entry{}, false, errBadSSTable
		}
		blk = blk[sstEntryHeaderSize:]
		if uint64(len(blk)) < uint64(klen)+uint64(vlen) {
			return entry{}, false, errBadSSTable
		}
		kb := blk[:klen]
		if string(kb) == key {
			if deleted {
				return entry{deleted: true}, true, nil
			}
			return entry{value: append([]byte(nil), blk[klen:uint32(klen)+vlen]...)}, true, nil
		}
		if string(kb) > key {
			return entry{}, false, nil // 记录有序, 越过目标即不存在
		}
		blk = blk[uint32(klen)+vlen:]
	}
	return entry{}, false, nil
}

func (s *sstable) String() string { return fmt.Sprintf("sstable{%s}", s.path) }
