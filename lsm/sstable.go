package lsm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
)

// sstableMagic 标识文件格式版本。
const sstableMagic = "SST1"

// Entry 是 SSTable 中的一条键值记录。
type Entry struct {
	Key   []byte
	Value []byte
}

// WriteSSTable 将已按 key 排序的 entries 原子化写入 path（先写临时文件再 rename），
// 文件布局: magic | count(uvarint) | entries... | crc32(此前全部字节)。
// 生成的文件从此不可变，这是硬链接快照成立的前提。
func WriteSSTable(path string, entries []Entry) (err error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()

	w := bufio.NewWriter(f)
	crc := crc32.NewIEEE()
	mw := io.MultiWriter(w, crc)

	var buf [binary.MaxVarintLen64]byte
	if _, err = mw.Write([]byte(sstableMagic)); err != nil {
		return err
	}
	n := binary.PutUvarint(buf[:], uint64(len(entries)))
	if _, err = mw.Write(buf[:n]); err != nil {
		return err
	}
	for _, e := range entries {
		n := binary.PutUvarint(buf[:], uint64(len(e.Key)))
		if _, err = mw.Write(buf[:n]); err != nil {
			return err
		}
		if _, err = mw.Write(e.Key); err != nil {
			return err
		}
		n = binary.PutUvarint(buf[:], uint64(len(e.Value)))
		if _, err = mw.Write(buf[:n]); err != nil {
			return err
		}
		if _, err = mw.Write(e.Value); err != nil {
			return err
		}
	}
	var footer [4]byte
	binary.BigEndian.PutUint32(footer[:], crc.Sum32())
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

// ReadSSTable 读取整个 SSTable，校验 CRC 后返回全部记录。
func ReadSSTable(path string) ([]Entry, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < len(sstableMagic)+4 {
		return nil, fmt.Errorf("%s: file too short", path)
	}
	body, footer := data[:len(data)-4], data[len(data)-4:]
	if binary.BigEndian.Uint32(footer) != crc32.ChecksumIEEE(body) {
		return nil, fmt.Errorf("%s: crc mismatch", path)
	}

	r := bytes.NewReader(body)
	magic := make([]byte, len(sstableMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("%s: read magic: %v", path, err)
	}
	if string(magic) != sstableMagic {
		return nil, fmt.Errorf("%s: bad magic %q", path, magic)
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("%s: read count: %v", path, err)
	}

	entries := make([]Entry, 0, count)
	for i := uint64(0); i < count; i++ {
		klen, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("%s: read key len: %v", path, err)
		}
		key := make([]byte, klen)
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, fmt.Errorf("%s: read key: %v", path, err)
		}
		vlen, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("%s: read value len: %v", path, err)
		}
		val := make([]byte, vlen)
		if _, err := io.ReadFull(r, val); err != nil {
			return nil, fmt.Errorf("%s: read value: %v", path, err)
		}
		entries = append(entries, Entry{Key: key, Value: val})
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("%s: %d trailing bytes", path, r.Len())
	}
	return entries, nil
}
