package netsec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// validMaxPacket returns a fully valid packet with maximum headers plus a
// payload; individual tests mutate it before calling the rewriter.
func validMaxPacket(t *testing.T) []byte {
	t.Helper()
	ipOpts := bytes.Repeat([]byte{0x88, 0x04, 0x11, 0x22}, 10)
	tcpOpts := []byte{
		0x01,
		0x02, 0x04, 0x05, 0xb4,
		0x03, 0x03, 0x07,
		0x04, 0x02,
		0x08, 0x0a, 0, 0, 0, 1, 0, 0, 0, 2,
		0x05, 0x0a, 1, 2, 3, 4, 5, 6, 7, 8,
		0x13, 0x02,
		0x1c, 0x08, 1, 2, 3, 4, 5, 6,
	}
	tcpOpts = padEOL(tcpOpts)
	if len(tcpOpts) != 40 {
		t.Fatalf("test setup: tcpOpts = %d", len(tcpOpts))
	}
	return buildPacket(t, ipOpts, tcpOpts, []byte{0xa, 0xb, 0xc})
}

func TestRejectsAndLeavesInputUntouched(t *testing.T) {
	base := validMaxPacket(t)
	allowed := NewOptionSet(2, 3, 4, 8)

	cases := []struct {
		name     string
		want     error
		mut      func([]byte)
		replacer func() []byte
	}{
		{
			name: "too short for IPv4 header",
			want: ErrShortPacket,
			replacer: func() []byte {
				return base[:10]
			},
		},
		{
			name: "not IPv4",
			want: ErrNotIPv4,
			mut:  func(b []byte) { b[0] = 0x60 | (b[0] & 0x0f) },
		},
		{
			name: "IHL below minimum",
			want: ErrBadHeader,
			mut:  func(b []byte) { b[0] = (b[0] & 0xf0) | 0x04 },
		},
		{
			name: "IHL points beyond packet",
			want: ErrBadHeader,
			replacer: func() []byte {
				p := buildPacket(t, nil, padEOL([]byte{0x02, 0x04, 0x05, 0xb4}), []byte{1})
				p[0] = (p[0] & 0xf0) | 0x0f
				return p
			},
		},
		{
			name: "total length too small",
			want: ErrBadHeader,
			mut:  func(b []byte) { b[3] = 10 },
		},
		{
			name: "total length beyond packet",
			want: ErrBadHeader,
			mut:  func(b []byte) { b[3] = 0xff },
		},
		{
			name: "not TCP",
			want: ErrNotTCP,
			mut:  func(b []byte) { b[9] = 17 },
		},
		{
			name: "TCP data offset below minimum",
			want: ErrBadHeader,
			mut:  func(b []byte) { b[60+12] = (b[60+12] & 0x0f) | (4 << 4) },
		},
		{
			name: "TCP header beyond datagram",
			want: ErrBadHeader,
			replacer: func() []byte {
				p := buildPacket(t, nil, padEOL([]byte{0x02, 0x04, 0x05, 0xb4}), []byte{1, 2})
				p[20+12] = (p[20+12] & 0x0f) | (15 << 4)
				return p
			},
		},
		{
			name: "option length one",
			want: ErrBadOption,
			mut:  func(b []byte) { b[60+20+2] = 1 }, // MSS length byte
		},
		{
			name: "option length zero",
			want: ErrBadOption,
			mut:  func(b []byte) { b[60+20+2] = 0 },
		},
		{
			name: "option length overruns area",
			want: ErrBadOption,
			mut:  func(b []byte) { b[60+20+2] = 40 },
		},
		{
			name: "missing length byte at area end",
			want: ErrBadOption,
			replacer: func() []byte {
				// The bare kind sits on the option area's final byte, so no
				// room remains even for its length byte.
				tcpOpts := make([]byte, 40)
				tcpOpts[0] = 0x02
				tcpOpts[1] = 0x04
				tcpOpts[2] = 0x05
				tcpOpts[3] = 0xb4
				for i := 4; i < 40; i++ {
					tcpOpts[i] = 0x13
				}
				return buildPacket(t, nil, tcpOpts, []byte{1})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in []byte
			if tc.replacer != nil {
				in = tc.replacer()
			} else {
				in = bytes.Clone(base)
				tc.mut(in)
			}
			snapshot := bytes.Clone(in)

			n, err := RewriteIPv4TCPPacket(in, allowed)
			if err == nil {
				t.Fatalf("expected error, got length %d", n)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want wrapping %v", err, tc.want)
			}
			if n != 0 {
				t.Errorf("returned length = %d, want 0 on failure", n)
			}
			if !bytes.Equal(in, snapshot) {
				for i := range in {
					if in[i] != snapshot[i] {
						t.Fatalf("input modified at byte %d: 0x%02x -> 0x%02x", i, snapshot[i], in[i])
					}
				}
			}
		})
	}
}

// TestChecksumCrossCheck fuzzes random valid packets and verifies both
// checksums with the independent byte-at-a-time implementation.
func TestChecksumCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	allowed := NewOptionSet(2, 3, 4, 8)

	// Each inner slice is one option module (kind, length, payload). The
	// modules are always structurally valid, so randomisation only touches
	// payload bytes, never kind/length.
	modules := [][]byte{
		{0x02, 0x04, 0x05, 0xb4},
		{0x03, 0x03, 0x07},
		{0x04, 0x02},
		{0x08, 0x0a, 0, 0, 0, 1, 0, 0, 0, 2},
		{0x05, 0x06, 1, 2, 3, 4},
		{0x13, 0x02},
		{0x1c, 0x08, 0, 0, 0, 0, 0, 0},
	}

	for iter := 0; iter < 64; iter++ {
		var raw []byte
		nopCount := rng.Intn(4)
		for j := 0; j < nopCount; j++ {
			raw = append(raw, 0x01)
		}
		moduleCount := 1 + rng.Intn(4)
		for j := 0; j < moduleCount; j++ {
			mod := bytes.Clone(modules[rng.Intn(len(modules))])
			for k := 2; k < len(mod); k++ {
				mod[k] = byte(rng.Intn(256))
			}
			raw = append(raw, mod...)
			if len(raw) > 36 {
				break
			}
		}
		tcpOpts := padEOL(raw)
		payloadLen := rng.Intn(33) // includes zero and odd lengths
		payload := make([]byte, payloadLen)
		rng.Read(payload)

		var ipOpts []byte
		if rng.Intn(2) == 1 {
			ipOpts = bytes.Repeat([]byte{0x88, 0x04, 0xab, 0xcd}, 10)
		}

		packet := buildPacket(t, ipOpts, tcpOpts, payload)
		ipHeaderLen := int(packet[0]&0x0f) * 4

		n, err := RewriteIPv4TCPPacket(packet, allowed)
		if err != nil {
			t.Fatalf("iter %d: rewrite failed: %v", iter, err)
		}
		if refChecksum(packet[:ipHeaderLen]) != 0 {
			t.Fatalf("iter %d: bad IPv4 checksum", iter)
		}
		segment := packet[ipHeaderLen:n]
		if refChecksum(refPseudoHeader(packet[12:16], packet[16:20], len(segment)), segment) != 0 {
			t.Fatalf("iter %d: bad TCP checksum (payloadLen=%d)", iter, payloadLen)
		}
	}
}

// TestIdempotentRewrite applies the rewrite twice; the second call must not
// change anything because all remaining options are already allowed.
func TestIdempotentRewrite(t *testing.T) {
	packet := buildPacket(t,
		nil,
		[]byte{0x01, 0x02, 0x04, 0x05, 0xb4, 0x13, 0x02, 0x00},
		[]byte{1, 2, 3},
	)
	allowed := NewOptionSet(2)

	n1, err := RewriteIPv4TCPPacket(packet, allowed)
	if err != nil {
		t.Fatalf("first rewrite: %v", err)
	}
	once := bytes.Clone(packet[:n1])
	n2, err := RewriteIPv4TCPPacket(packet[:n1], allowed)
	if err != nil {
		t.Fatalf("second rewrite: %v", err)
	}
	if n1 != n2 || !bytes.Equal(packet[:n2], once) {
		t.Fatalf("second rewrite changed the packet: %d vs %d", n2, n1)
	}
}
