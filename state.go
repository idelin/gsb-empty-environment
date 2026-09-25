package pagealloc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// 状态文件布局（小端）：
//
//	magic    4B  "PGS1"
//	pageSize 4B
//	pageCnt  8B
//	ivCount  4B
//	ivs      ivCount * 16B: start(8B) | count(8B)
//	crc32    4B  覆盖之前全部内容
//
// 写入采用 tmp 文件 + fsync + rename + 目录 fsync，保证原子替换。
const stateMagic = "PGS1"

type state struct {
	pageSize  uint32
	pageCount uint64
	free      *freeSet
}

func encodeState(st *state) []byte {
	buf := make([]byte, 0, 20+len(st.free.ivs)*16+4)
	buf = append(buf, stateMagic...)
	buf = binary.LittleEndian.AppendUint32(buf, st.pageSize)
	buf = binary.LittleEndian.AppendUint64(buf, st.pageCount)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(st.free.ivs)))
	for _, iv := range st.free.ivs {
		buf = binary.LittleEndian.AppendUint64(buf, iv.start)
		buf = binary.LittleEndian.AppendUint64(buf, iv.count)
	}
	return binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

func decodeState(data []byte) (*state, error) {
	if len(data) < 24 {
		return nil, errors.New("pagealloc: state file too short")
	}
	if string(data[:4]) != stateMagic {
		return nil, errors.New("pagealloc: bad state magic")
	}
	want := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != want {
		return nil, errors.New("pagealloc: state checksum mismatch")
	}
	st := &state{
		pageSize:  binary.LittleEndian.Uint32(data[4:8]),
		pageCount: binary.LittleEndian.Uint64(data[8:16]),
		free:      &freeSet{},
	}
	n := binary.LittleEndian.Uint32(data[16:20])
	if 20+uint64(n)*16+4 != uint64(len(data)) {
		return nil, errors.New("pagealloc: state length mismatch")
	}
	off := 20
	prevEnd := uint64(0)
	for i := uint32(0); i < n; i++ {
		iv := interval{
			start: binary.LittleEndian.Uint64(data[off : off+8]),
			count: binary.LittleEndian.Uint64(data[off+8 : off+16]),
		}
		off += 16
		if iv.count == 0 || iv.start < prevEnd || iv.start+iv.count > st.pageCount {
			return nil, fmt.Errorf("pagealloc: invalid interval %+v", iv)
		}
		prevEnd = iv.start + iv.count
		st.free.ivs = append(st.free.ivs, iv)
	}
	return st, nil
}

func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeState(data)
}

// persistState 原子地把状态写盘：写 tmp、fsync、rename、fsync 目录。
func persistState(dir, path string, st *state) error {
	tmp := filepath.Join(dir, filepath.Base(path)+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(encodeState(st)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
