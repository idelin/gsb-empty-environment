package dod

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

// This file holds an independent reference implementation, written directly
// from the format specification and sharing no code with the encoder, used
// to cross-verify integer sequences end to end.

// refEncode is a simple independent encoder: no optimization, one block,
// all dods at fixed 64-bit width.
func refEncode(ts, vs []int64) []byte {
	out := binary.AppendUvarint(nil, uint64(len(ts)))
	var b [8]byte
	put := func(x int64) {
		binary.LittleEndian.PutUint64(b[:], uint64(x))
		out = append(out, b[:]...)
	}
	put(ts[0])
	put(vs[0])
	if len(ts) >= 2 {
		put(ts[1] - ts[0])
		put(vs[1] - vs[0])
	}
	for i := 2; i < len(ts); i++ {
		put((ts[i] - ts[i-1]) - (ts[i-1] - ts[i-2]))
		put((vs[i] - vs[i-1]) - (vs[i-1] - vs[i-2]))
	}
	return out
}

func refDecode(buf []byte) ([]int64, []int64) {
	n, k := binary.Uvarint(buf)
	rest := buf[k:]
	get := func() int64 {
		v := int64(binary.LittleEndian.Uint64(rest))
		rest = rest[8:]
		return v
	}
	ts := make([]int64, n)
	vs := make([]int64, n)
	ts[0], vs[0] = get(), get()
	var dt, dv int64
	if n >= 2 {
		dt, dv = get(), get()
		ts[1], vs[1] = ts[0]+dt, vs[0]+dv
	}
	for i := 2; i < int(n); i++ {
		dt += get()
		dv += get()
		ts[i] = ts[i-1] + dt
		vs[i] = vs[i-1] + dv
	}
	return ts, vs
}

// refDecodeBlock parses the production block format from scratch.
func refDecodeBlock(t *testing.T, buf []byte) (uint64, []int64, []int64) {
	t.Helper()
	total, k := binary.Uvarint(buf)
	if k <= 0 {
		t.Fatal("bad length")
	}
	payload := buf[k : k+int(total)]
	body, crcB := payload[:len(payload)-4], payload[len(payload)-4:]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(crcB) {
		t.Fatal("crc")
	}
	if body[0] != 1 {
		t.Fatal("version")
	}
	r := body[2:]
	uvar := func() uint64 {
		v, n := binary.Uvarint(r)
		if n <= 0 {
			t.Fatal("uvarint")
		}
		r = r[n:]
		return v
	}
	svar := func() int64 {
		v, n := binary.Varint(r)
		if n <= 0 {
			t.Fatal("svarint")
		}
		r = r[n:]
		return v
	}
	epoch := uvar()
	n := int(uvar())
	ts := make([]int64, n)
	vs := make([]int64, n)
	ts[0], vs[0] = svar(), svar()
	var dt, dv int64
	if n >= 2 {
		dt, dv = svar(), svar()
		ts[1], vs[1] = ts[0]+dt, vs[0]+dv
	}
	if n >= 3 {
		readStream := func() []int64 {
			ln := int(uvar())
			data := r[:ln]
			r = r[ln:]
			nseg, k2 := binary.Uvarint(data)
			p := data[k2:]
			type sg struct {
				w uint
				c int
			}
			var segs []sg
			totalBits, totalCnt := 0, 0
			for i := uint64(0); i < nseg; i++ {
				w := uint(p[0])
				p = p[1:]
				c, k3 := binary.Uvarint(p)
				p = p[k3:]
				segs = append(segs, sg{w, int(c)})
				totalBits += int(w) * int(c)
				totalCnt += int(c)
			}
			payload := p[:(totalBits+7)/8]
			out := make([]int64, 0, totalCnt)
			bytePos, bitPos := 0, uint(0)
			read := func(w uint) uint64 {
				var v uint64
				var got uint
				for w > 0 {
					take := 8 - bitPos
					if take > w {
						take = w
					}
					mask := uint64(1)<<take - 1
					v |= (uint64(payload[bytePos]) >> bitPos & mask) << got
					got += take
					bitPos += take
					w -= take
					if bitPos == 8 {
						bytePos++
						bitPos = 0
					}
				}
				return v
			}
			for _, s := range segs {
				for c := 0; c < s.c; c++ {
					u := read(s.w)
					out = append(out, int64(u>>1)^-int64(u&1))
				}
			}
			return out
		}
		td := readStream()
		vd := readStream()
		for i := 2; i < n; i++ {
			dt += td[i-2]
			dv += vd[i-2]
			ts[i] = ts[i-1] + dt
			vs[i] = vs[i-1] + dv
		}
	}
	return epoch, ts, vs
}

// Cross-verify: random integer sequences encoded by the optimized encoder
// must decode identically under the independent reference decoder, and the
// reference encoder must round-trip the same sequences.
func TestReferenceCrossVerification(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(300)
		ts := make([]int64, n)
		vs := make([]int64, n)
		ts[0] = rng.Int63n(1 << 50)
		vs[0] = rng.Int63n(1<<50) - 1<<49
		for i := 1; i < n; i++ {
			ts[i] = ts[i-1] + rng.Int63n(10000)
			vs[i] = vs[i-1] + rng.Int63n(1<<20) - 1<<19
		}
		// Reference round trip.
		rtT, rtV := refDecode(refEncode(ts, vs))
		if !eqI64(rtT, ts) || !eqI64(rtV, vs) {
			t.Fatalf("trial %d: reference round trip failed", trial)
		}
		// Production encode -> independent reference decode.
		epoch := uint64(rng.Intn(8))
		blk, err := encodeBlock(epoch, ts, vs)
		if err != nil {
			t.Fatal(err)
		}
		gEpoch, gT, gV := refDecodeBlock(t, blk)
		if gEpoch != epoch || !eqI64(gT, ts) || !eqI64(gV, vs) {
			t.Fatalf("trial %d: reference decode mismatch", trial)
		}
		// Production encode must never exceed the fixed-width reference size.
		if len(blk) > len(refEncode(ts, vs))+16 {
			t.Fatalf("trial %d: %d bytes exceeds reference %d", trial, len(blk), len(refEncode(ts, vs)))
		}
	}
}
