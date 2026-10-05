package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"strings"
)

// record 是一条待排序记录。seq 为全局输入序号（uint64，足够精度），
// 用于相同键的稳定排序：比较 (key, seq)。
type record struct {
	seq uint64
	key string
	val string
}

// parseLine 把输入行解析为记录：首个制表符前为键，其余为值。
func parseLine(line string, seq uint64) record {
	key, val := line, ""
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		key, val = line[:i], line[i+1:]
	}
	return record{seq: seq, key: key, val: val}
}

// writeRecord 以 varint 前缀的二进制格式追加一条记录。
func writeRecord(w *bufio.Writer, r record) error {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], r.seq)
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	n = binary.PutUvarint(tmp[:], uint64(len(r.key)))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	if _, err := w.WriteString(r.key); err != nil {
		return err
	}
	n = binary.PutUvarint(tmp[:], uint64(len(r.val)))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	if _, err := w.WriteString(r.val); err != nil {
		return err
	}
	return nil
}

// readRecord 读取一条记录；流结束时返回 io.EOF。
func readRecord(r *bufio.Reader) (record, error) {
	var rec record
	seq, err := binary.ReadUvarint(r)
	if err != nil {
		return rec, err
	}
	klen, err := binary.ReadUvarint(r)
	if err != nil {
		return rec, err
	}
	kb := make([]byte, klen)
	if _, err := io.ReadFull(r, kb); err != nil {
		return rec, err
	}
	vlen, err := binary.ReadUvarint(r)
	if err != nil {
		return rec, err
	}
	vb := make([]byte, vlen)
	if _, err := io.ReadFull(r, vb); err != nil {
		return rec, err
	}
	rec.seq, rec.key, rec.val = seq, string(kb), string(vb)
	return rec, nil
}
