package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// File formats (version 1). All multi-byte lengths are unsigned varints.
//
// Dictionary file:
//
//	"DCD1" | varint numEntries | [varint len | bytes]... | crc32(preceding bytes)
//
// Stream file:
//
//	"DCS1" | dictCRC(4, big endian) | varint origLen | varint numTokens |
//	tokens... | crc32(decoded output)
//
// Token: tag 0 = literal run: varint length (1..maxLitRunLen) + raw bytes.
//
//	tag 1 = dictionary match: varint entry index.
const (
	dictMagic   = "DCD1"
	streamMagic = "DCS1"
)

var (
	errBadMagic    = errors.New("bad magic or unsupported format version")
	errBadCRC      = errors.New("checksum mismatch")
	errTruncated   = errors.New("unexpected end of input")
	errTrailing    = errors.New("trailing bytes after end of stream")
	errTooLarge    = errors.New("decoded output exceeds configured limit")
	errWrongDict   = errors.New("stream was not compressed with this dictionary")
	errBadToken    = errors.New("malformed token")
	errLenMismatch = errors.New("decoded length does not match header")
)

// marshalDict serializes a dictionary. The dictionary is stored as-is; callers
// keep entries sorted for determinism.
func marshalDict(dict [][]byte) []byte {
	buf := []byte(dictMagic)
	buf = binary.AppendUvarint(buf, uint64(len(dict)))
	for _, p := range dict {
		buf = binary.AppendUvarint(buf, uint64(len(p)))
		buf = append(buf, p...)
	}
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(buf))
	return append(buf, crc[:]...)
}

// dictFileCRC returns the checksum stored in a serialized dictionary file.
// It is embedded into stream headers to bind streams to a dictionary.
func dictFileCRC(dictFile []byte) uint32 {
	return binary.BigEndian.Uint32(dictFile[len(dictFile)-4:])
}

func unmarshalDict(data []byte) ([][]byte, error) {
	if len(data) < len(dictMagic)+4 {
		return nil, errTruncated
	}
	if string(data[:4]) != dictMagic {
		return nil, errBadMagic
	}
	body, crcBytes := data[:len(data)-4], data[len(data)-4:]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(crcBytes) {
		return nil, errBadCRC
	}
	r := &reader{buf: body[4:]}
	n := r.uvarint()
	if r.err != nil {
		return nil, r.err
	}
	if n > maxDictEntries {
		return nil, fmt.Errorf("dictionary has %d entries, max %d", n, maxDictEntries)
	}
	dict := make([][]byte, 0, n)
	for i := uint64(0); i < n; i++ {
		l := r.uvarint()
		if r.err != nil {
			return nil, r.err
		}
		if l == 0 || l > 1<<20 {
			return nil, fmt.Errorf("invalid entry length %d", l)
		}
		p := r.bytes(int(l))
		if r.err != nil {
			return nil, r.err
		}
		dict = append(dict, p)
	}
	if len(r.buf) != 0 {
		return nil, errTrailing
	}
	return dict, nil
}

// encodeStream serializes tokens into the stream format. Literal runs longer
// than maxLitRunLen are split into multiple runs.
func encodeStream(dictCRC uint32, src []byte, toks []token) []byte {
	var split []token
	for _, t := range toks {
		if t.match < 0 && len(t.literal) > maxLitRunLen {
			lit := t.literal
			for len(lit) > maxLitRunLen {
				split = append(split, token{match: -1, literal: lit[:maxLitRunLen]})
				lit = lit[maxLitRunLen:]
			}
			split = append(split, token{match: -1, literal: lit})
			continue
		}
		split = append(split, t)
	}
	buf := []byte(streamMagic)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], dictCRC)
	buf = append(buf, crc[:]...)
	buf = binary.AppendUvarint(buf, uint64(len(src)))
	buf = binary.AppendUvarint(buf, uint64(len(split)))
	for _, t := range split {
		if t.match >= 0 {
			buf = append(buf, tagMatch)
			buf = binary.AppendUvarint(buf, uint64(t.match))
		} else {
			buf = append(buf, tagLiteralRun)
			buf = binary.AppendUvarint(buf, uint64(len(t.literal)))
			buf = append(buf, t.literal...)
		}
	}
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(src))
	return append(buf, crc[:]...)
}

// decodeStream decodes a stream, verifying the dictionary binding, declared
// length and output checksum. Output is bounded: decoding fails as soon as
// more than maxOut bytes would be produced.
func decodeStream(data []byte, dict [][]byte, maxOut int) ([]byte, error) {
	if len(data) < len(streamMagic)+4 {
		return nil, errTruncated
	}
	if string(data[:4]) != streamMagic {
		return nil, errBadMagic
	}
	r := &reader{buf: data[4:]}
	streamDictCRC := r.uint32()
	origLen := r.uvarint()
	numToks := r.uvarint()
	if r.err != nil {
		return nil, r.err
	}
	if streamDictCRC != dictFileCRC(marshalDict(dict)) {
		return nil, errWrongDict
	}
	if origLen > uint64(maxOut) {
		return nil, errTooLarge
	}
	out := make([]byte, 0, origLen)
	for i := uint64(0); i < numToks; i++ {
		tag := r.byte()
		if r.err != nil {
			return nil, r.err
		}
		switch tag {
		case tagLiteralRun:
			l := r.uvarint()
			if r.err != nil {
				return nil, r.err
			}
			if l == 0 || l > maxLitRunLen {
				return nil, errBadToken
			}
			if len(out)+int(l) > maxOut {
				return nil, errTooLarge
			}
			b := r.bytes(int(l))
			if r.err != nil {
				return nil, r.err
			}
			out = append(out, b...)
		case tagMatch:
			idx := r.uvarint()
			if r.err != nil {
				return nil, r.err
			}
			if idx >= uint64(len(dict)) {
				return nil, errBadToken
			}
			if len(out)+len(dict[idx]) > maxOut {
				return nil, errTooLarge
			}
			out = append(out, dict[idx]...)
		default:
			return nil, errBadToken
		}
	}
	if uint64(len(out)) != origLen {
		return nil, errLenMismatch
	}
	stored := r.uint32()
	if r.err != nil {
		return nil, r.err
	}
	if crc32.ChecksumIEEE(out) != stored {
		return nil, errBadCRC
	}
	if len(r.buf) != 0 {
		return nil, errTrailing
	}
	return out, nil
}

// reader is a cursor over a byte slice with sticky error.
type reader struct {
	buf []byte
	err error
}

func (r *reader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.buf)
	if n <= 0 {
		r.err = errTruncated
		return 0
	}
	r.buf = r.buf[n:]
	return v
}

func (r *reader) byte() byte {
	if r.err != nil {
		return 0
	}
	if len(r.buf) < 1 {
		r.err = errTruncated
		return 0
	}
	b := r.buf[0]
	r.buf = r.buf[1:]
	return b
}

func (r *reader) uint32() uint32 {
	if r.err != nil {
		return 0
	}
	if len(r.buf) < 4 {
		r.err = errTruncated
		return 0
	}
	v := binary.BigEndian.Uint32(r.buf)
	r.buf = r.buf[4:]
	return v
}

func (r *reader) bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if len(r.buf) < n {
		r.err = errTruncated
		return nil
	}
	b := r.buf[:n]
	r.buf = r.buf[n:]
	return b
}
