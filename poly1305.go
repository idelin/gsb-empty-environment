package noise

import "encoding/binary"

// poly1305 is a small implementation of the Poly1305 one-time authenticator.
// It uses 26-bit limbs and follows the arithmetic structure of
// poly1305-donna.
type poly1305 struct {
	r    [5]uint32
	h    [5]uint32
	pad  [4]uint32
	buf  [16]byte
	used int
}

func newPoly1305(key *[32]byte) *poly1305 {
	p := &poly1305{}
	p.r[0] = binary.LittleEndian.Uint32(key[0:]) & 0x3ffffff
	p.r[1] = (binary.LittleEndian.Uint32(key[3:]) >> 2) & 0x3ffff03
	p.r[2] = (binary.LittleEndian.Uint32(key[6:]) >> 4) & 0x3ffc0ff
	p.r[3] = (binary.LittleEndian.Uint32(key[9:]) >> 6) & 0x3f03fff
	p.r[4] = (binary.LittleEndian.Uint32(key[12:]) >> 8) & 0x00fffff

	p.pad[0] = binary.LittleEndian.Uint32(key[16:])
	p.pad[1] = binary.LittleEndian.Uint32(key[20:])
	p.pad[2] = binary.LittleEndian.Uint32(key[24:])
	p.pad[3] = binary.LittleEndian.Uint32(key[28:])
	return p
}

func (p *poly1305) write(msg []byte) {
	if p.used > 0 {
		n := copy(p.buf[p.used:], msg)
		p.used += n
		msg = msg[n:]
		if p.used == len(p.buf) {
			p.process(&p.buf, 1<<24)
			p.used = 0
		}
	}

	for len(msg) >= 16 {
		var block [16]byte
		copy(block[:], msg[:16])
		p.process(&block, 1<<24)
		msg = msg[16:]
	}

	if len(msg) > 0 {
		p.used = copy(p.buf[:], msg)
	}
}

func (p *poly1305) sum() [16]byte {
	if p.used > 0 {
		var block [16]byte
		copy(block[:], p.buf[:p.used])
		block[p.used] = 1
		p.process(&block, 0)
		p.used = 0
	}

	h0, h1, h2, h3, h4 := p.h[0], p.h[1], p.h[2], p.h[3], p.h[4]

	// Fully reduce modulo 2^130-5.
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

	// Compute h + 5 and conditionally select it if h >= 2^130-5.
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
	g4 &= mask
	g0 &= mask
	g1 &= mask
	g2 &= mask
	g3 &= mask
	nmask := ^mask
	h0 = (h0 & nmask) | g0
	h1 = (h1 & nmask) | g1
	h2 = (h2 & nmask) | g2
	h3 = (h3 & nmask) | g3
	h4 = (h4 & nmask) | g4

	// Serialize h and add the second half of the one-time key.
	f0 := uint64(h0 | h1<<26)
	f1 := uint64(h1>>6 | h2<<20)
	f2 := uint64(h2>>12 | h3<<14)
	f3 := uint64(h3>>18 | h4<<8)

	f0 += uint64(p.pad[0])
	f1 += uint64(p.pad[1]) + (f0 >> 32)
	f0 &= 0xffffffff
	f2 += uint64(p.pad[2]) + (f1 >> 32)
	f1 &= 0xffffffff
	f3 += uint64(p.pad[3]) + (f2 >> 32)
	f2 &= 0xffffffff
	f3 &= 0xffffffff

	var tag [16]byte
	binary.LittleEndian.PutUint32(tag[0:], uint32(f0))
	binary.LittleEndian.PutUint32(tag[4:], uint32(f1))
	binary.LittleEndian.PutUint32(tag[8:], uint32(f2))
	binary.LittleEndian.PutUint32(tag[12:], uint32(f3))
	return tag
}

func (p *poly1305) destroy() {
	for i := range p.r {
		p.r[i] = 0
		p.h[i] = 0
	}
	for i := range p.pad {
		p.pad[i] = 0
	}
	zero(p.buf[:])
	p.used = 0
}

func (p *poly1305) process(block *[16]byte, hibit uint32) {
	h0, h1, h2, h3, h4 := p.h[0], p.h[1], p.h[2], p.h[3], p.h[4]
	r0, r1, r2, r3, r4 := p.r[0], p.r[1], p.r[2], p.r[3], p.r[4]
	s1, s2, s3, s4 := r1*5, r2*5, r3*5, r4*5

	h0 += binary.LittleEndian.Uint32(block[0:]) & 0x3ffffff
	h1 += (binary.LittleEndian.Uint32(block[3:]) >> 2) & 0x3ffffff
	h2 += (binary.LittleEndian.Uint32(block[6:]) >> 4) & 0x3ffffff
	h3 += (binary.LittleEndian.Uint32(block[9:]) >> 6) & 0x3ffffff
	h4 += (binary.LittleEndian.Uint32(block[12:]) >> 8) | hibit

	d0 := uint64(h0)*uint64(r0) + uint64(h1)*uint64(s4) +
		uint64(h2)*uint64(s3) + uint64(h3)*uint64(s2) + uint64(h4)*uint64(s1)
	d1 := uint64(h0)*uint64(r1) + uint64(h1)*uint64(r0) +
		uint64(h2)*uint64(s4) + uint64(h3)*uint64(s3) + uint64(h4)*uint64(s2)
	d2 := uint64(h0)*uint64(r2) + uint64(h1)*uint64(r1) +
		uint64(h2)*uint64(r0) + uint64(h3)*uint64(s4) + uint64(h4)*uint64(s3)
	d3 := uint64(h0)*uint64(r3) + uint64(h1)*uint64(r2) +
		uint64(h2)*uint64(r1) + uint64(h3)*uint64(r0) + uint64(h4)*uint64(s4)
	d4 := uint64(h0)*uint64(r4) + uint64(h1)*uint64(r3) +
		uint64(h2)*uint64(r2) + uint64(h3)*uint64(r1) + uint64(h4)*uint64(r0)

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
	c = uint64(h0 >> 26)
	h0 &= 0x3ffffff
	h1 += uint32(c)

	p.h = [5]uint32{h0, h1, h2, h3, h4}
}

func poly1305MAC(key *[32]byte, ad, ciphertext []byte) [16]byte {
	p := newPoly1305(key)
	p.write(ad)
	if rem := len(ad) % 16; rem != 0 {
		p.write(make([]byte, 16-rem))
	}
	p.write(ciphertext)
	if rem := len(ciphertext) % 16; rem != 0 {
		p.write(make([]byte, 16-rem))
	}
	var lengths [16]byte
	binary.LittleEndian.PutUint64(lengths[0:], uint64(len(ad)))
	binary.LittleEndian.PutUint64(lengths[8:], uint64(len(ciphertext)))
	p.write(lengths[:])
	tag := p.sum()
	p.destroy()
	return tag
}
