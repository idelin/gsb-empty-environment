package wireguard

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	chacha20KeySize   = 32
	chacha20NonceSize = 12
	poly1305TagSize   = 16
)

var errAuth = errors.New("wireguard: message authentication failed")

func chacha20Block(key *[32]byte, counter uint32, nonce *[12]byte) [64]byte {
	var state [16]uint32
	state[0] = 0x61707865
	state[1] = 0x3320646e
	state[2] = 0x79622d32
	state[3] = 0x6b206574
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4 : i*4+4])
	}
	state[12] = counter
	for i := 0; i < 3; i++ {
		state[13+i] = binary.LittleEndian.Uint32(nonce[i*4 : i*4+4])
	}

	x := state
	qr := func(a, b, c, d int) {
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 16)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 12)
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 8)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 7)
	}
	for i := 0; i < 10; i++ {
		qr(0, 4, 8, 12)
		qr(1, 5, 9, 13)
		qr(2, 6, 10, 14)
		qr(3, 7, 11, 15)
		qr(0, 5, 10, 15)
		qr(1, 6, 11, 12)
		qr(2, 7, 8, 13)
		qr(3, 4, 9, 14)
	}

	var out [64]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], x[i]+state[i])
	}
	return out
}

func hchacha20(key *[32]byte, nonce *[16]byte) [32]byte {
	var state [16]uint32
	state[0] = 0x61707865
	state[1] = 0x3320646e
	state[2] = 0x79622d32
	state[3] = 0x6b206574
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4 : i*4+4])
	}
	for i := 0; i < 4; i++ {
		state[12+i] = binary.LittleEndian.Uint32(nonce[i*4 : i*4+4])
	}

	x := state
	qr := func(a, b, c, d int) {
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 16)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 12)
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 8)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 7)
	}
	for i := 0; i < 10; i++ {
		qr(0, 4, 8, 12)
		qr(1, 5, 9, 13)
		qr(2, 6, 10, 14)
		qr(3, 7, 11, 15)
		qr(0, 5, 10, 15)
		qr(1, 6, 11, 12)
		qr(2, 7, 8, 13)
		qr(3, 4, 9, 14)
	}

	var out [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], x[i])
		binary.LittleEndian.PutUint32(out[(i+4)*4:(i+4)*4+4], x[12+i])
	}
	return out
}

func chacha20XOR(dst, src []byte, key *[32]byte, nonce *[12]byte, counter uint32) {
	for len(src) > 0 {
		block := chacha20Block(key, counter, nonce)
		n := len(src)
		if n > 64 {
			n = 64
		}
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ block[i]
		}
		dst = dst[n:]
		src = src[n:]
		counter++
	}
}

type poly1305MAC struct {
	r      [5]uint32
	h      [5]uint32
	pad    [4]uint32
	buf    [16]byte
	bufLen int
}

func newPoly1305(key []byte) *poly1305MAC {
	if len(key) != 32 {
		panic("wireguard: invalid Poly1305 key size")
	}
	var p poly1305MAC
	t0 := binary.LittleEndian.Uint32(key[0:4])
	t1 := binary.LittleEndian.Uint32(key[4:8])
	t2 := binary.LittleEndian.Uint32(key[8:12])
	t3 := binary.LittleEndian.Uint32(key[12:16])
	p.r[0] = t0 & 0x3ffffff
	p.r[1] = ((t0 >> 26) | (t1 << 6)) & 0x3ffff03
	p.r[2] = ((t1 >> 20) | (t2 << 12)) & 0x3ffc0ff
	p.r[3] = ((t2 >> 14) | (t3 << 18)) & 0x3f03fff
	p.r[4] = (t3 >> 8) & 0x00fffff
	for i := 0; i < 4; i++ {
		p.pad[i] = binary.LittleEndian.Uint32(key[16+i*4 : 20+i*4])
	}
	return &p
}

func (p *poly1305MAC) blocks(block []byte, hibit uint32) {
	h0, h1, h2, h3, h4 := p.h[0], p.h[1], p.h[2], p.h[3], p.h[4]
	r0, r1, r2, r3, r4 := p.r[0], p.r[1], p.r[2], p.r[3], p.r[4]

	h0 += binary.LittleEndian.Uint32(block[0:4]) & 0x3ffffff
	h1 += (binary.LittleEndian.Uint32(block[3:7]) >> 2) & 0x3ffffff
	h2 += (binary.LittleEndian.Uint32(block[6:10]) >> 4) & 0x3ffffff
	h3 += (binary.LittleEndian.Uint32(block[9:13]) >> 6) & 0x3ffffff
	h4 += (binary.LittleEndian.Uint32(block[12:16]) >> 8) | hibit

	s1 := r1 * 5
	s2 := r2 * 5
	s3 := r3 * 5
	s4 := r4 * 5

	d0 := uint64(h0)*uint64(r0) + uint64(h1)*uint64(s4) + uint64(h2)*uint64(s3) + uint64(h3)*uint64(s2) + uint64(h4)*uint64(s1)
	d1 := uint64(h0)*uint64(r1) + uint64(h1)*uint64(r0) + uint64(h2)*uint64(s4) + uint64(h3)*uint64(s3) + uint64(h4)*uint64(s2)
	d2 := uint64(h0)*uint64(r2) + uint64(h1)*uint64(r1) + uint64(h2)*uint64(r0) + uint64(h3)*uint64(s4) + uint64(h4)*uint64(s3)
	d3 := uint64(h0)*uint64(r3) + uint64(h1)*uint64(r2) + uint64(h2)*uint64(r1) + uint64(h3)*uint64(r0) + uint64(h4)*uint64(s4)
	d4 := uint64(h0)*uint64(r4) + uint64(h1)*uint64(r3) + uint64(h2)*uint64(r2) + uint64(h3)*uint64(r1) + uint64(h4)*uint64(r0)

	c := d0 >> 26
	h0 = uint32(d0) & 0x3ffffff
	d1 += c
	c = d1 >> 26
	h1 = uint32(d1) & 0x3ffffff
	d2 += c
	c = d2 >> 26
	h2 = uint32(d2) & 0x3ffffff
	d3 += c
	c = d3 >> 26
	h3 = uint32(d3) & 0x3ffffff
	d4 += c
	c = d4 >> 26
	h4 = uint32(d4) & 0x3ffffff
	h0 += uint32(c) * 5
	c = uint64(h0) >> 26
	h0 &= 0x3ffffff
	h1 += uint32(c)

	p.h = [5]uint32{h0, h1, h2, h3, h4}
}

func (p *poly1305MAC) Write(data []byte) {
	if p.bufLen > 0 {
		n := copy(p.buf[p.bufLen:], data)
		p.bufLen += n
		data = data[n:]
		if p.bufLen < 16 {
			return
		}
		p.blocks(p.buf[:], 1<<24)
		p.bufLen = 0
	}
	for len(data) >= 16 {
		p.blocks(data[:16], 1<<24)
		data = data[16:]
	}
	if len(data) > 0 {
		p.bufLen = copy(p.buf[:], data)
	}
}

func (p *poly1305MAC) WritePadded(data []byte) {
	p.Write(data)
	if rem := len(data) % 16; rem != 0 {
		var pad [16]byte
		p.Write(pad[:16-rem])
	}
}

func (p *poly1305MAC) Sum() [16]byte {
	if p.bufLen > 0 {
		var block [16]byte
		copy(block[:], p.buf[:p.bufLen])
		block[p.bufLen] = 1
		p.blocks(block[:], 0)
		p.bufLen = 0
	}

	h0, h1, h2, h3, h4 := p.h[0], p.h[1], p.h[2], p.h[3], p.h[4]
	c := h1 >> 26
	h1 &= 0x3ffffff
	h2 += c
	c = h2 >> 26
	h2 &= 0x3ffffff
	h3 += c
	c = h3 >> 26
	h3 &= 0x3ffffff
	h4 += c
	c = h4 >> 26
	h4 &= 0x3ffffff
	h0 += c * 5
	c = h0 >> 26
	h0 &= 0x3ffffff
	h1 += c

	g0 := h0 + 5
	c = g0 >> 26
	g0 &= 0x3ffffff
	g1 := h1 + c
	c = g1 >> 26
	g1 &= 0x3ffffff
	g2 := h2 + c
	c = g2 >> 26
	g2 &= 0x3ffffff
	g3 := h3 + c
	c = g3 >> 26
	g3 &= 0x3ffffff
	g4 := h4 + c - (1 << 26)

	mask := (g4 >> 31) - 1
	g0 &= mask
	g1 &= mask
	g2 &= mask
	g3 &= mask
	g4 &= mask
	mask = ^mask
	h0 = (h0 & mask) | g0
	h1 = (h1 & mask) | g1
	h2 = (h2 & mask) | g2
	h3 = (h3 & mask) | g3
	h4 = (h4 & mask) | g4

	f0 := (uint64(h0) | uint64(h1)<<26) & 0xffffffff
	f1 := (uint64(h1)>>6 | uint64(h2)<<20) & 0xffffffff
	f2 := (uint64(h2)>>12 | uint64(h3)<<14) & 0xffffffff
	f3 := (uint64(h3)>>18 | uint64(h4)<<8) & 0xffffffff

	f0 += uint64(p.pad[0])
	f1 += uint64(p.pad[1]) + (f0 >> 32)
	f0 &= 0xffffffff
	f2 += uint64(p.pad[2]) + (f1 >> 32)
	f1 &= 0xffffffff
	f3 += uint64(p.pad[3]) + (f2 >> 32)
	f2 &= 0xffffffff
	f3 &= 0xffffffff

	var tag [16]byte
	binary.LittleEndian.PutUint32(tag[0:4], uint32(f0))
	binary.LittleEndian.PutUint32(tag[4:8], uint32(f1))
	binary.LittleEndian.PutUint32(tag[8:12], uint32(f2))
	binary.LittleEndian.PutUint32(tag[12:16], uint32(f3))
	return tag
}

func chacha20Poly1305Seal(key *[32]byte, nonce *[12]byte, plaintext, ad []byte) []byte {
	block0 := chacha20Block(key, 0, nonce)
	var polyKey [32]byte
	copy(polyKey[:], block0[:32])

	out := make([]byte, len(plaintext)+poly1305TagSize)
	chacha20XOR(out[:len(plaintext)], plaintext, key, nonce, 1)

	mac := newPoly1305(polyKey[:])
	mac.WritePadded(ad)
	mac.WritePadded(out[:len(plaintext)])
	var lens [16]byte
	binary.LittleEndian.PutUint64(lens[0:8], uint64(len(ad)))
	binary.LittleEndian.PutUint64(lens[8:16], uint64(len(plaintext)))
	mac.Write(lens[:])
	tag := mac.Sum()
	copy(out[len(plaintext):], tag[:])
	return out
}

func chacha20Poly1305Open(key *[32]byte, nonce *[12]byte, ciphertext, ad []byte) ([]byte, error) {
	if len(ciphertext) < poly1305TagSize {
		return nil, errAuth
	}
	body := ciphertext[:len(ciphertext)-poly1305TagSize]
	tag := ciphertext[len(ciphertext)-poly1305TagSize:]

	block0 := chacha20Block(key, 0, nonce)
	var polyKey [32]byte
	copy(polyKey[:], block0[:32])
	mac := newPoly1305(polyKey[:])
	mac.WritePadded(ad)
	mac.WritePadded(body)
	var lens [16]byte
	binary.LittleEndian.PutUint64(lens[0:8], uint64(len(ad)))
	binary.LittleEndian.PutUint64(lens[8:16], uint64(len(body)))
	mac.Write(lens[:])
	expected := mac.Sum()
	if subtle.ConstantTimeCompare(expected[:], tag) != 1 {
		return nil, errAuth
	}

	out := make([]byte, len(body))
	chacha20XOR(out, body, key, nonce, 1)
	return out, nil
}

func xchacha20Poly1305Seal(key *[32]byte, nonce *[24]byte, plaintext, ad []byte) []byte {
	var nonce16 [16]byte
	copy(nonce16[:], nonce[:16])
	subkey := hchacha20(key, &nonce16)
	var shortNonce [12]byte
	copy(shortNonce[4:], nonce[16:24])
	return chacha20Poly1305Seal(&subkey, &shortNonce, plaintext, ad)
}

func xchacha20Poly1305Open(key *[32]byte, nonce *[24]byte, ciphertext, ad []byte) ([]byte, error) {
	var nonce16 [16]byte
	copy(nonce16[:], nonce[:16])
	subkey := hchacha20(key, &nonce16)
	var shortNonce [12]byte
	copy(shortNonce[4:], nonce[16:24])
	return chacha20Poly1305Open(&subkey, &shortNonce, ciphertext, ad)
}
