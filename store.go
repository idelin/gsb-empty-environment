package main

import (
	"fmt"
	"os"
)

// Store 是一个追加式日志，数据与索引交替写入。
// Append 顺序：写数据 -> 写索引 -> （可选）fsync，崩溃恢复见 Recover。
type Store struct {
	data    *os.File
	index   *os.File
	entries []indexEntry // 内存索引，与索引文件一致
	tail    int64        // 数据文件逻辑末尾
	sync    bool         // 每次 Append 后是否 fsync
}

// Open 打开（必要时先恢复）一个 Store。
// 若遇到完整坏记录，仍返回覆盖腐败点之前有效前缀的 Store，
// 同时附带 *Report 与 ErrCorruptRecord，调用方可检查坏记录详情。
func Open(dataPath, indexPath string, sync bool) (*Store, *Report, error) {
	rep, rerr := Recover(dataPath, indexPath)
	if rerr != nil && rerr != ErrCorruptRecord {
		return nil, rep, rerr
	}
	data, err := os.OpenFile(dataPath, os.O_RDWR, 0644)
	if err != nil {
		return nil, rep, err
	}
	index, err := os.OpenFile(indexPath, os.O_RDWR, 0644)
	if err != nil {
		data.Close()
		return nil, rep, err
	}
	s := &Store{data: data, index: index, sync: sync}
	dfi, _ := data.Stat()
	s.tail = dfi.Size()
	ifi, _ := index.Stat()
	n := ifi.Size() / indexEntrySize
	buf := make([]byte, indexEntrySize)
	for i := int64(0); i < n; i++ {
		if _, err := index.ReadAt(buf, i*indexEntrySize); err != nil {
			data.Close()
			index.Close()
			return nil, rep, err
		}
		s.entries = append(s.entries, decodeIndexEntry(buf))
	}
	// 定位到文件末尾准备追加。
	if _, err := data.Seek(0, 2); err != nil {
		data.Close()
		index.Close()
		return nil, rep, err
	}
	if _, err := index.Seek(0, 2); err != nil {
		data.Close()
		index.Close()
		return nil, rep, err
	}
	return s, rep, rerr
}

// Append 追加一条记录：先写数据，再写索引。
func (s *Store) Append(payload []byte) (int, error) {
	rec := encodeRecord(payload)
	if _, err := s.data.Write(rec); err != nil {
		return -1, err
	}
	e := indexEntry{Offset: uint64(s.tail), Length: uint32(len(payload)), Digest: checksum(payload)}
	if _, err := s.index.Write(encodeIndexEntry(e)); err != nil {
		return -1, err
	}
	if s.sync {
		if err := s.data.Sync(); err != nil {
			return -1, err
		}
		if err := s.index.Sync(); err != nil {
			return -1, err
		}
	}
	s.entries = append(s.entries, e)
	s.tail += int64(len(rec))
	return len(s.entries) - 1, nil
}

// Len 返回记录条数。
func (s *Store) Len() int { return len(s.entries) }

// Get 读取第 i 条记录，并重新校验摘要（防御性校验）。
func (s *Store) Get(i int) ([]byte, error) {
	if i < 0 || i >= len(s.entries) {
		return nil, fmt.Errorf("logrec: index %d out of range", i)
	}
	e := s.entries[i]
	hdr := make([]byte, recordHeaderSize)
	if _, err := s.data.ReadAt(hdr, int64(e.Offset)); err != nil {
		return nil, err
	}
	if le32(hdr[0:4]) != recordMagic || le32(hdr[4:8]) != e.Length {
		return nil, fmt.Errorf("logrec: record %d header mismatch", i)
	}
	payload := make([]byte, e.Length)
	if _, err := s.data.ReadAt(payload, int64(e.Offset)+recordHeaderSize); err != nil {
		return nil, err
	}
	if checksum(payload) != e.Digest {
		return nil, fmt.Errorf("logrec: record %d digest mismatch", i)
	}
	return payload, nil
}

// Close 关闭底层文件。
func (s *Store) Close() error {
	err1 := s.data.Close()
	err2 := s.index.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
