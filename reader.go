package polyglot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// boundedReader 把 ReaderAt 包装成带边界检查的顺序/随机读取器。
// 所有读取都被限制在 [0,size) 内，越界返回明确错误而不是静默截断，
// 避免恶意文件用越界偏移触发不可预期的行为。
type boundedReader struct {
	ra   io.ReaderAt
	size int64
}

// newBoundedReader 校验大小并构造读取器。
func newBoundedReader(ra io.ReaderAt, size int64) (*boundedReader, error) {
	if ra == nil {
		return nil, fmt.Errorf("%w: ReaderAt 为 nil", ErrInvalidSize)
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: size=%d", ErrInvalidSize, size)
	}
	return &boundedReader{ra: ra, size: size}, nil
}

// Size 返回文件大小。
func (r *boundedReader) Size() int64 { return r.size }

// readAt 读取 off 开始的 len(dst) 字节，任何越界都返回错误。
func (r *boundedReader) readAt(off int64, dst []byte) error {
	if off < 0 {
		return fmt.Errorf("读取偏移为负: %d", off)
	}
	if int64(len(dst)) > r.size-off {
		return fmt.Errorf("读取范围 [%d,%d) 超出文件大小 %d", off, off+int64(len(dst)), r.size)
	}
	n, err := r.ra.ReadAt(dst, off)
	if err == io.EOF && n == len(dst) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取偏移 %d 失败: %w", off, err)
	}
	if n != len(dst) {
		return fmt.Errorf("读取偏移 %d 仅得到 %d 字节，期望 %d", off, n, len(dst))
	}
	return nil
}

// byteAt 返回单个字节。
func (r *boundedReader) byteAt(off int64) (byte, error) {
	var buf [1]byte
	err := r.readAt(off, buf[:])
	return buf[0], err
}

// u16leAt 读取小端 uint16。
func (r *boundedReader) u16leAt(off int64) (uint16, error) {
	var buf [2]byte
	if err := r.readAt(off, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(buf[:]), nil
}

// u32leAt 读取小端 uint32。
func (r *boundedReader) u32leAt(off int64) (uint32, error) {
	var buf [4]byte
	if err := r.readAt(off, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// u32beAt 读取大端 uint32。
func (r *boundedReader) u32beAt(off int64) (uint32, error) {
	var buf [4]byte
	if err := r.readAt(off, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

// uint64leAt 读取小端 uint64，供 ZIP64 结构使用。
func (r *boundedReader) uint64leAt(off int64) (uint64, error) {
	var buf [8]byte
	if err := r.readAt(off, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

// bytesAt 返回 off 开始的 n 个字节副本，避免调用方持有共享缓冲。
func (r *boundedReader) bytesAt(off int64, n int) ([]byte, error) {
	buf := make([]byte, n)
	if err := r.readAt(off, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// hasPrefixAt 判断 off 处是否以 prefix 开头（越界即不匹配）。
func (r *boundedReader) hasPrefixAt(off int64, prefix []byte) bool {
	if off < 0 || int64(len(prefix)) > r.size-off {
		return false
	}
	buf := make([]byte, len(prefix))
	return r.readAt(off, buf) == nil && bytes.Equal(buf, prefix)
}

// lastIndex 在 [start,end) 范围内反向查找 sep 的最后一次出现。
func (r *boundedReader) lastIndex(start, end int64, sep []byte) (int64, bool) {
	if start < 0 || end > r.size || start > end {
		return 0, false
	}
	window, err := r.bytesAt(start, int(end-start))
	if err != nil {
		return 0, false
	}
	idx := bytes.LastIndex(window, sep)
	if idx < 0 {
		return 0, false
	}
	return start + int64(idx), true
}

// indexAt 在 [start,end) 范围内查找 sep 的首次出现。
func (r *boundedReader) indexAt(start, end int64, sep []byte) (int64, bool) {
	if start < 0 || end > r.size || start > end {
		return 0, false
	}
	window, err := r.bytesAt(start, int(end-start))
	if err != nil {
		return 0, false
	}
	idx := bytes.Index(window, sep)
	if idx < 0 {
		return 0, false
	}
	return start + int64(idx), true
}
