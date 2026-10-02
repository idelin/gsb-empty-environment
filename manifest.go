// Package versionset 实现 LSM 树元数据的崩溃一致性管理：
// 通过向 MANIFEST 日志追加 VersionEdit 记录来原子演进版本，
// 重启后重放日志恢复目录结构，并清理未提交 Compaction 留下的孤儿文件。
package versionset

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// VersionEdit 记录标签（类似 LevelDB 的 tag 编码）。
const (
	tagDeletedFile = 1
	tagNewFile     = 2
	tagNextFileNum = 3
)

// DeletedFile 标记某个 level 中的一个文件被移除。
type DeletedFile struct {
	Level   int
	FileNum uint64
}

// VersionEdit 是一次版本演进的原子单元：要么整体生效，要么整体无效。
type VersionEdit struct {
	DeletedFiles   []DeletedFile
	NewFiles       []FileMeta
	NextFileNum    uint64
	HasNextFileNum bool
}

// encode 将 VersionEdit 序列化为字节流。
func (e *VersionEdit) encode() []byte {
	var buf []byte
	putUvarint := func(v uint64) {
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], v)
		buf = append(buf, b[:n]...)
	}
	if e.HasNextFileNum {
		buf = append(buf, tagNextFileNum)
		putUvarint(e.NextFileNum)
	}
	for _, d := range e.DeletedFiles {
		buf = append(buf, tagDeletedFile)
		putUvarint(uint64(d.Level))
		putUvarint(d.FileNum)
	}
	for i := range e.NewFiles {
		f := &e.NewFiles[i]
		buf = append(buf, tagNewFile)
		putUvarint(uint64(f.Level))
		putUvarint(f.FileNum)
		putUvarint(f.Size)
		var c [4]byte
		binary.LittleEndian.PutUint32(c[:], f.Checksum)
		buf = append(buf, c[:]...)
		putUvarint(uint64(len(f.Smallest)))
		buf = append(buf, f.Smallest...)
		putUvarint(uint64(len(f.Largest)))
		buf = append(buf, f.Largest...)
	}
	return buf
}

// decode 从字节流反序列化 VersionEdit。
func (e *VersionEdit) decode(src []byte) error {
	d := &decoder{buf: src}
	for len(d.buf) > 0 {
		tag, err := d.uvarint()
		if err != nil {
			return err
		}
		switch tag {
		case tagNextFileNum:
			v, err := d.uvarint()
			if err != nil {
				return err
			}
			e.NextFileNum, e.HasNextFileNum = v, true
		case tagDeletedFile:
			level, err := d.uvarint()
			if err != nil {
				return err
			}
			num, err := d.uvarint()
			if err != nil {
				return err
			}
			e.DeletedFiles = append(e.DeletedFiles, DeletedFile{Level: int(level), FileNum: num})
		case tagNewFile:
			var f FileMeta
			level, err := d.uvarint()
			if err != nil {
				return err
			}
			f.Level = int(level)
			if f.FileNum, err = d.uvarint(); err != nil {
				return err
			}
			if f.Size, err = d.uvarint(); err != nil {
				return err
			}
			c, err := d.bytes(4)
			if err != nil {
				return err
			}
			f.Checksum = binary.LittleEndian.Uint32(c)
			if f.Smallest, err = d.lengthPrefixed(); err != nil {
				return err
			}
			if f.Largest, err = d.lengthPrefixed(); err != nil {
				return err
			}
			e.NewFiles = append(e.NewFiles, f)
		default:
			return fmt.Errorf("versionset: unknown edit tag %d", tag)
		}
	}
	return nil
}

// decoder 是带游标的字节流读取器。
type decoder struct {
	buf []byte
}

func (d *decoder) uvarint() (uint64, error) {
	v, n := binary.Uvarint(d.buf)
	if n <= 0 {
		return 0, fmt.Errorf("versionset: corrupt varint")
	}
	d.buf = d.buf[n:]
	return v, nil
}

func (d *decoder) bytes(n int) ([]byte, error) {
	if len(d.buf) < n {
		return nil, fmt.Errorf("versionset: unexpected end of record")
	}
	b := d.buf[:n]
	d.buf = d.buf[n:]
	return b, nil
}

func (d *decoder) lengthPrefixed() ([]byte, error) {
	n, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	return d.bytes(int(n))
}

// ---------------------------------------------------------------------------
// 记录层：MANIFEST 是定长头 + payload 的追加日志。
// 每条记录格式：CRC32(payload) 4B | 长度 4B | payload。
// ---------------------------------------------------------------------------

const recordHeaderSize = 8

// appendRecord 向 w 追加一条带校验和的记录。
func appendRecord(w io.Writer, payload []byte) error {
	var hdr [recordHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], crc32.ChecksumIEEE(payload))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// recordReader 顺序读取记录，容忍日志尾部的撕裂写（崩溃残留）。
type recordReader struct {
	r        io.Reader
	off      int64 // 已消费的字节数
	LastGood int64 // 最后一条完整记录结束处的偏移
}

func newRecordReader(r io.Reader) *recordReader { return &recordReader{r: r} }

// next 返回下一条记录的 payload；io.EOF 表示日志正常结束。
// 尾部损坏（撕裂写 / CRC 不匹配）时返回 errCorruptTail，调用方应截断日志。
var errCorruptTail = fmt.Errorf("versionset: corrupt manifest tail")

func (rr *recordReader) next() ([]byte, error) {
	var hdr [recordHeaderSize]byte
	if _, err := io.ReadFull(rr.r, hdr[:]); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		// 头部不完整：崩溃时只写了一半头。
		return nil, errCorruptTail
	}
	crc := binary.LittleEndian.Uint32(hdr[0:4])
	length := binary.LittleEndian.Uint32(hdr[4:8])
	payload := make([]byte, length)
	if _, err := io.ReadFull(rr.r, payload); err != nil {
		return nil, errCorruptTail
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, errCorruptTail
	}
	rr.off += recordHeaderSize + int64(length)
	rr.LastGood = rr.off
	return payload, nil
}
