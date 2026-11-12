package netsec

import (
	"encoding/binary"
	"errors"
)

var errMalformed = errors.New("netsec: malformed encoding")

// putBytes 以 4 字节大端长度前缀追加 b，保证编码无歧义、可安全哈希。
func putBytes(dst, b []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	dst = append(dst, n[:]...)
	return append(dst, b...)
}

func putString(dst []byte, s string) []byte { return putBytes(dst, []byte(s)) }

// frame 把若干字段按长度前缀拼接为一个整体，用于 MAC 输入。
func frame(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = putBytes(out, p)
	}
	return out
}

type decoder struct {
	buf []byte
	err error
}

func (d *decoder) bytes() []byte {
	if d.err != nil {
		return nil
	}
	if len(d.buf) < 4 {
		d.err = errMalformed
		return nil
	}
	n := binary.BigEndian.Uint32(d.buf[:4])
	d.buf = d.buf[4:]
	if uint64(n) > uint64(len(d.buf)) {
		d.err = errMalformed
		return nil
	}
	out := d.buf[:n]
	d.buf = d.buf[n:]
	return out
}

func (d *decoder) str() string { return string(d.bytes()) }
