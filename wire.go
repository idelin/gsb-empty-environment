package netsec

import (
	"encoding/binary"
	"errors"
)

// errWireFormat 表示 SSH 线格式（wire format）数据损坏或长度不合法。
var errWireFormat = errors.New("netsec: malformed SSH wire format")

// wireWriter 按 SSH 线格式（RFC 4251 第 5 节）序列化字段。
type wireWriter struct {
	buf []byte
}

func (w *wireWriter) uint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

func (w *wireWriter) uint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

// string 写入一个 SSH string 字段：4 字节大端长度 + 原始字节。
func (w *wireWriter) string(b []byte) {
	w.uint32(uint32(len(b)))
	w.buf = append(w.buf, b...)
}

// stringList 写入一个由若干 SSH string 拼接而成的字段，
// 用于证书的 valid principals、critical options 和 extensions。
func (w *wireWriter) stringList(items []string) {
	var inner wireWriter
	for _, item := range items {
		inner.string([]byte(item))
	}
	w.string(inner.buf)
}

// wireReader 按 SSH 线格式解析字段，任何错误都会使后续读取失败。
type wireReader struct {
	data []byte
	err  error
}

func (r *wireReader) read(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.data) < n {
		r.err = errWireFormat
		return nil
	}
	b := r.data[:n]
	r.data = r.data[n:]
	return b
}

func (r *wireReader) uint32() uint32 {
	b := r.read(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *wireReader) uint64() uint64 {
	b := r.read(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *wireReader) string() []byte {
	n := r.uint32()
	if r.err != nil {
		return nil
	}
	return r.read(int(n))
}

// stringList 解析由若干 SSH string 拼接而成的字段。
func (r *wireReader) stringList() ([]string, error) {
	raw := r.string()
	if r.err != nil {
		return nil, r.err
	}
	inner := &wireReader{data: raw}
	var items []string
	for len(inner.data) > 0 && inner.err == nil {
		items = append(items, string(inner.string()))
	}
	if inner.err != nil {
		return nil, inner.err
	}
	return items, nil
}
