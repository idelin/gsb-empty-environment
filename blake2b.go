package netsec

import "encoding/binary"

// blake2bBlockSize is the BLAKE2b compression block size in bytes.
const blake2bBlockSize = 128

// blake2bIV is the initialization vector defined in RFC 7693, section 2.6.
var blake2bIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b,
	0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f,
	0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

// blake2bSigma are the word permutation schedule from RFC 7693, section 2.7.
var blake2bSigma = [12][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
}

// blake2b is an unkeyed BLAKE2b hash implementing hash.Hash.
// Only unkeyed hashing is required because minisign prehashes content
// with the unkeyed BLAKE2b-512 variant.
type blake2b struct {
	h      [8]uint64
	t      uint64
	buf    [blake2bBlockSize]byte
	buflen int
	size   int
}

// newBlake2b returns an unkeyed BLAKE2b hash producing digestSize bytes
// (1..64). minisign uses the 64-byte (BLAKE2b-512) digest.
func newBlake2b(digestSize int) *blake2b {
	if digestSize < 1 || digestSize > 64 {
		panic("netsec: invalid BLAKE2b digest size")
	}
	h := blake2b{size: digestSize}
	h.Reset()
	return &h
}

func (h *blake2b) Reset() {
	h.h = blake2bIV
	// XOR the parameter block word 0 (RFC 7693, section 2.5). It is stored
	// little-endian: digest length in byte 0, fanout=1 in byte 1 and
	// depth=1 in byte 2. The remaining parameter bytes are zero because the
	// hash is unkeyed with no salt or personalization string.
	h.h[0] ^= blake2bParamWord0(h.size)
	h.t = 0
	h.buflen = 0
}

// blake2bParamWord0 builds parameter block word 0 for an unkeyed hash.
func blake2bParamWord0(digestSize int) uint64 {
	return uint64(digestSize) | (uint64(1) << 16) | (uint64(1) << 24)
}

func (h *blake2b) Size() int      { return h.size }
func (h *blake2b) BlockSize() int { return blake2bBlockSize }

func (h *blake2b) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if h.buflen == len(h.buf) {
			// The buffer holds a complete, non-final block: compress it to
			// make room. Whether a block is final is decided only in checkSum.
			h.t += blake2bBlockSize
			h.compress(h.buf[:], false)
			h.buflen = 0
		}
		take := copy(h.buf[h.buflen:], p)
		h.buflen += take
		p = p[take:]
	}
	return n, nil
}

func (h *blake2b) Sum(b []byte) []byte {
	sum := h.checkSum()
	return append(b, sum[:h.size]...)
}

func (h *blake2b) checkSum() [64]byte {
	work := *h
	// The buffered bytes (possibly an empty block for an empty message or a
	// message whose length is a block multiple) are always the final block.
	work.t += uint64(work.buflen)
	for i := work.buflen; i < blake2bBlockSize; i++ {
		work.buf[i] = 0
	}
	work.compress(work.buf[:], true)

	var out [64]byte
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], work.h[i])
	}
	return out
}

// compress is the BLAKE2b mixing function G over 12 rounds (RFC 7693 3.1).
func (h *blake2b) compress(block []byte, last bool) {
	var m [16]uint64
	for i := 0; i < 16; i++ {
		m[i] = binary.LittleEndian.Uint64(block[i*8:])
	}

	v := [16]uint64{
		h.h[0], h.h[1], h.h[2], h.h[3],
		h.h[4], h.h[5], h.h[6], h.h[7],
		blake2bIV[0], blake2bIV[1], blake2bIV[2], blake2bIV[3],
		blake2bIV[4], blake2bIV[5], blake2bIV[6], blake2bIV[7],
	}
	v[12] ^= h.t
	v[13] ^= 0
	if last {
		v[14] ^= ^uint64(0)
	}

	for round := 0; round < 12; round++ {
		s := blake2bSigma[round]
		g := func(a, b, c, d int, x, y uint64) {
			v[a] = v[a] + v[b] + x
			v[d] = rotr64(v[d]^v[a], 32)
			v[c] = v[c] + v[d]
			v[b] = rotr64(v[b]^v[c], 24)
			v[a] = v[a] + v[b] + y
			v[d] = rotr64(v[d]^v[a], 16)
			v[c] = v[c] + v[d]
			v[b] = rotr64(v[b]^v[c], 63)
		}
		g(0, 4, 8, 12, m[s[0]], m[s[1]])
		g(1, 5, 9, 13, m[s[2]], m[s[3]])
		g(2, 6, 10, 14, m[s[4]], m[s[5]])
		g(3, 7, 11, 15, m[s[6]], m[s[7]])
		g(0, 5, 10, 15, m[s[8]], m[s[9]])
		g(1, 6, 11, 12, m[s[10]], m[s[11]])
		g(2, 7, 8, 13, m[s[12]], m[s[13]])
		g(3, 4, 9, 14, m[s[14]], m[s[15]])
	}

	for i := 0; i < 8; i++ {
		h.h[i] ^= v[i] ^ v[i+8]
	}
}

func rotr64(x uint64, k uint) uint64 {
	return (x >> k) | (x << (64 - k))
}
