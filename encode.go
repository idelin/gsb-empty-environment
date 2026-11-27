package treekem

import (
	"crypto/sha256"
	"encoding/binary"
)

type hashBuilder struct {
	buf []byte
}

func (h *hashBuilder) byte(v byte) { h.buf = append(h.buf, v) }

func (h *hashBuilder) uint16(v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	h.buf = append(h.buf, b[:]...)
}

func (h *hashBuilder) uint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	h.buf = append(h.buf, b[:]...)
}

func (h *hashBuilder) uint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	h.buf = append(h.buf, b[:]...)
}

func (h *hashBuilder) bytes(v []byte) {
	h.uint32(uint32(len(v)))
	h.buf = append(h.buf, v...)
}

func (h *hashBuilder) indexes(values []Index) {
	h.uint16(uint16(len(values)))
	for _, value := range values {
		h.uint32(uint32(value))
	}
}

func (h *hashBuilder) positions(values []int) {
	h.uint16(uint16(len(values)))
	for _, value := range values {
		h.uint32(uint32(value))
	}
}

func (h *hashBuilder) sum(prefix string) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(prefix))
	_, _ = digest.Write(h.buf)
	return digest.Sum(nil)
}

func hashBytes(prefix string, parts ...[]byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(prefix))
	for _, part := range parts {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write(part)
	}
	return digest.Sum(nil)
}
