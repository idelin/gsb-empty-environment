package netsec

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"math/bits"
)

const (
	chachaBlockSize = 64
	aeadTagSize     = 16
)

func rotl32(v uint32, n uint) uint32 {
	return bits.RotateLeft32(v, int(n))
}

func quarterRound(a, b, c, d uint32) (uint32, uint32, uint32, uint32) {
	a += b
	d ^= a
	d = rotl32(d, 16)
	c += d
	b ^= c
	b = rotl32(b, 12)
	a += b
	d ^= a
	d = rotl32(d, 8)
	c += d
	b ^= c
	b = rotl32(b, 7)
	return a, b, c, d
}

func chacha20Block(key []byte, counter uint32, nonce []byte) [chachaBlockSize]byte {
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

	working := state
	for i := 0; i < 10; i++ {
		working[0], working[4], working[8], working[12] =
			quarterRound(working[0], working[4], working[8], working[12])
		working[1], working[5], working[9], working[13] =
			quarterRound(working[1], working[5], working[9], working[13])
		working[2], working[6], working[10], working[14] =
			quarterRound(working[2], working[6], working[10], working[14])
		working[3], working[7], working[11], working[15] =
			quarterRound(working[3], working[7], working[11], working[15])
		working[0], working[5], working[10], working[15] =
			quarterRound(working[0], working[5], working[10], working[15])
		working[1], working[6], working[11], working[12] =
			quarterRound(working[1], working[6], working[11], working[12])
		working[2], working[7], working[8], working[13] =
			quarterRound(working[2], working[7], working[8], working[13])
		working[3], working[4], working[9], working[14] =
			quarterRound(working[3], working[4], working[9], working[14])
	}

	var out [chachaBlockSize]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], working[i]+state[i])
	}
	return out
}

func chacha20XOR(key, nonce, in []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: ChaCha20 key has length %d", ErrInvalidKey, len(key))
	}
	if len(nonce) != 12 {
		return nil, fmt.Errorf("%w: ChaCha20 nonce has length %d", ErrInvalidKey, len(nonce))
	}
	out := make([]byte, len(in))
	counter := uint32(1)
	for offset := 0; offset < len(in); offset += chachaBlockSize {
		block := chacha20Block(key, counter, nonce)
		counter++
		n := len(in) - offset
		if n > chachaBlockSize {
			n = chachaBlockSize
		}
		for i := 0; i < n; i++ {
			out[offset+i] = in[offset+i] ^ block[i]
		}
	}
	return out, nil
}

func load32(in []byte) uint32 {
	return uint32(in[0]) | uint32(in[1])<<8 | uint32(in[2])<<16 | uint32(in[3])<<24
}

const polyMask = uint32(0x3ffffff)

func poly1305Block(h *[5]uint32, r *[5]uint32, block []byte, hibit uint32) {
	t0 := load32(block[0:4])
	t1 := load32(block[4:8])
	t2 := load32(block[8:12])
	t3 := load32(block[12:16])

	h0 := h[0] + (t0 & polyMask)
	h1 := h[1] + (((t0 >> 26) | (t1 << 6)) & polyMask)
	h2 := h[2] + (((t1 >> 20) | (t2 << 12)) & polyMask)
	h3 := h[3] + (((t2 >> 14) | (t3 << 18)) & polyMask)
	h4 := h[4] + ((t3 >> 8) | hibit)

	r0, r1, r2, r3, r4 := r[0], r[1], r[2], r[3], r[4]
	s1, s2, s3, s4 := r1*5, r2*5, r3*5, r4*5

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
	h[0] = uint32(d0) & polyMask
	d1 += c
	c = d1 >> 26
	h[1] = uint32(d1) & polyMask
	d2 += c
	c = d2 >> 26
	h[2] = uint32(d2) & polyMask
	d3 += c
	c = d3 >> 26
	h[3] = uint32(d3) & polyMask
	d4 += c
	c = d4 >> 26
	h[4] = uint32(d4) & polyMask
	h[0] += uint32(c) * 5
	c = uint64(h[0]) >> 26
	h[0] &= polyMask
	h[1] += uint32(c)
}

func poly1305Sum(key, data []byte) [aeadTagSize]byte {
	var r [5]uint32
	var h [5]uint32

	r[0] = load32(key[0:4]) & polyMask
	r[1] = (load32(key[3:7]) >> 2) & 0x3ffff03
	r[2] = (load32(key[6:10]) >> 4) & 0x3ffc0ff
	r[3] = (load32(key[9:13]) >> 6) & 0x3f03fff
	r[4] = (load32(key[12:16]) >> 8) & 0x00fffff

	for len(data) >= 16 {
		poly1305Block(&h, &r, data[:16], 1<<24)
		data = data[16:]
	}
	if len(data) > 0 {
		var block [16]byte
		copy(block[:], data)
		block[len(data)] = 1
		poly1305Block(&h, &r, block[:], 0)
	}

	// Carry the accumulator, then conditionally subtract 2^130-5.
	c := h[1] >> 26
	h[1] &= polyMask
	h[2] += c
	c = h[2] >> 26
	h[2] &= polyMask
	h[3] += c
	c = h[3] >> 26
	h[3] &= polyMask
	h[4] += c
	c = h[4] >> 26
	h[4] &= polyMask
	h[0] += c * 5
	c = h[0] >> 26
	h[0] &= polyMask
	h[1] += c

	g0 := h[0] + 5
	c = g0 >> 26
	g0 &= polyMask
	g1 := h[1] + c
	c = g1 >> 26
	g1 &= polyMask
	g2 := h[2] + c
	c = g2 >> 26
	g2 &= polyMask
	g3 := h[3] + c
	c = g3 >> 26
	g3 &= polyMask
	g4 := h[4] + c - (1 << 26)

	selectMask := (g4 >> 31) - 1
	h[0] = (h[0] &^ selectMask) | (g0 & selectMask)
	h[1] = (h[1] &^ selectMask) | (g1 & selectMask)
	h[2] = (h[2] &^ selectMask) | (g2 & selectMask)
	h[3] = (h[3] &^ selectMask) | (g3 & selectMask)
	h[4] = (h[4] &^ selectMask) | (g4 & selectMask)

	pad0 := load32(key[16:20]) & polyMask
	pad1 := (load32(key[19:23]) >> 2) & polyMask
	pad2 := (load32(key[22:26]) >> 4) & polyMask
	pad3 := (load32(key[25:29]) >> 6) & polyMask
	pad4 := (load32(key[28:32]) >> 8) & polyMask

	c = h[0] + pad0
	h[0] = c & polyMask
	c = h[1] + pad1 + c>>26
	h[1] = c & polyMask
	c = h[2] + pad2 + c>>26
	h[2] = c & polyMask
	c = h[3] + pad3 + c>>26
	h[3] = c & polyMask
	c = h[4] + pad4 + c>>26
	h[4] = c & polyMask

	t0 := h[0] | h[1]<<26
	t1 := h[1]>>6 | h[2]<<20
	t2 := h[2]>>12 | h[3]<<14
	t3 := h[3]>>18 | h[4]<<8

	var tag [aeadTagSize]byte
	binary.LittleEndian.PutUint32(tag[0:4], t0)
	binary.LittleEndian.PutUint32(tag[4:8], t1)
	binary.LittleEndian.PutUint32(tag[8:12], t2)
	binary.LittleEndian.PutUint32(tag[12:16], t3)
	return tag
}

func aeadMACData(ad, ciphertext []byte) []byte {
	capacity := len(ad) + len(ciphertext) + 2*16 + 16
	data := make([]byte, 0, capacity)
	data = append(data, ad...)
	for len(data)%16 != 0 {
		data = append(data, 0)
	}
	ctStart := len(data)
	data = append(data, ciphertext...)
	for (len(data)-ctStart)%16 != 0 {
		data = append(data, 0)
	}
	data = appendUint64(data, uint64(len(ad)))
	data = appendUint64(data, uint64(len(ciphertext)))
	return data
}

func appendUint64(dst []byte, value uint64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], value)
	return append(dst, buf[:]...)
}

func chacha20poly1305Seal(key, nonce, ad, plaintext []byte) ([]byte, error) {
	if len(key) != 32 || len(nonce) != 12 {
		return nil, ErrInvalidKey
	}
	ciphertext, err := chacha20XOR(key, nonce, plaintext)
	if err != nil {
		return nil, err
	}
	polyKey := chacha20Block(key, 0, nonce)
	tag := poly1305Sum(polyKey[:32], aeadMACData(ad, ciphertext))
	out := make([]byte, 0, len(ciphertext)+len(tag))
	out = append(out, ciphertext...)
	out = append(out, tag[:]...)
	return out, nil
}

func chacha20poly1305Open(key, nonce, ad, ciphertextAndTag []byte) ([]byte, error) {
	if len(ciphertextAndTag) < aeadTagSize {
		return nil, ErrInvalidHandshakeMessage
	}
	ciphertext := ciphertextAndTag[:len(ciphertextAndTag)-aeadTagSize]
	tag := ciphertextAndTag[len(ciphertextAndTag)-aeadTagSize:]
	polyKey := chacha20Block(key, 0, nonce)
	expected := poly1305Sum(polyKey[:32], aeadMACData(ad, ciphertext))
	if subtle.ConstantTimeCompare(expected[:], tag) != 1 {
		return nil, fmt.Errorf("%w: authentication tag mismatch", ErrInvalidHandshakeMessage)
	}
	return chacha20XOR(key, nonce, ciphertext)
}
