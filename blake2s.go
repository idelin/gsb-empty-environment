package wireguard

import (
	"encoding/binary"
	"math/bits"
)

var blake2sIV = [8]uint32{
	0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
	0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
}

var blake2sSigma = [10][16]uint8{
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
}

type blake2sDigest struct {
	h      [8]uint32
	t      uint64
	buf    [64]byte
	buflen int
	outLen int
	keyLen int
}

func newBlake2s(outLen int, key []byte) *blake2sDigest {
	if outLen < 1 || outLen > 32 {
		panic("wireguard: invalid BLAKE2s digest size")
	}
	if len(key) > 32 {
		panic("wireguard: BLAKE2s key too long")
	}
	d := &blake2sDigest{outLen: outLen, keyLen: len(key)}
	d.h = blake2sIV
	d.h[0] ^= 0x01010000 ^ uint32(len(key))<<8 ^ uint32(outLen)
	if len(key) > 0 {
		copy(d.buf[:], key)
		d.buflen = 64
	}
	return d
}

func (d *blake2sDigest) Write(p []byte) {
	if d.buflen == 64 {
		if len(p) == 0 {
			return
		}
		d.t += 64
		d.compress(d.buf[:], false)
		d.buflen = 0
	}

	if d.buflen > 0 {
		n := copy(d.buf[d.buflen:], p)
		d.buflen += n
		p = p[n:]
		if len(p) == 0 {
			return
		}
		d.t += 64
		d.compress(d.buf[:], false)
		d.buflen = 0
	}

	for len(p) > 64 {
		d.t += 64
		d.compress(p[:64], false)
		p = p[64:]
	}
	if len(p) > 0 {
		d.buflen = copy(d.buf[:], p)
	}
}

func (d *blake2sDigest) Sum() [32]byte {
	d.t += uint64(d.buflen)
	for i := d.buflen; i < 64; i++ {
		d.buf[i] = 0
	}
	d.compress(d.buf[:], true)

	var out [32]byte
	var tmp [8][4]byte
	for i, w := range d.h {
		binary.LittleEndian.PutUint32(tmp[i][:], w)
	}
	for i := 0; i < d.outLen; i++ {
		out[i] = tmp[i/4][i%4]
	}
	return out
}

func (d *blake2sDigest) compress(block []byte, last bool) {
	var m [16]uint32
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(block[i*4 : i*4+4])
	}

	var v [16]uint32
	copy(v[:8], d.h[:])
	copy(v[8:], blake2sIV[:])
	v[12] ^= uint32(d.t)
	v[13] ^= uint32(d.t >> 32)
	if last {
		v[14] = ^v[14]
	}

	g := func(a, b, c, e int, x, y uint32) {
		v[a] = v[a] + v[b] + x
		v[e] = bits.RotateLeft32(v[e]^v[a], -16)
		v[c] += v[e]
		v[b] = bits.RotateLeft32(v[b]^v[c], -12)
		v[a] = v[a] + v[b] + y
		v[e] = bits.RotateLeft32(v[e]^v[a], -8)
		v[c] += v[e]
		v[b] = bits.RotateLeft32(v[b]^v[c], -7)
	}

	for i := 0; i < 10; i++ {
		s := &blake2sSigma[i]
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
		d.h[i] ^= v[i] ^ v[i+8]
	}
}

func blake2sHash(data ...[]byte) [32]byte {
	d := newBlake2s(32, nil)
	for _, part := range data {
		d.Write(part)
	}
	return d.Sum()
}

func blake2sMAC(key, data []byte) [16]byte {
	d := newBlake2s(16, key)
	d.Write(data)
	sum := d.Sum()
	var out [16]byte
	copy(out[:], sum[:16])
	return out
}

func hmacBlake2s(key, data []byte) [32]byte {
	const blockSize = 64
	if len(key) > blockSize {
		sum := blake2sHash(key)
		key = sum[:]
	}
	var ipad, opad [blockSize]byte
	copy(ipad[:], key)
	copy(opad[:], key)
	for i := range ipad {
		ipad[i] ^= 0x36
		opad[i] ^= 0x5c
	}
	inner := blake2sHash(ipad[:], data)
	return blake2sHash(opad[:], inner[:])
}
