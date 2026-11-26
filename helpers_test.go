package quicguard_test

import (
	"bytes"
	"net/netip"
	"sync"
	"testing"
	"time"

	"formal.local/w27-86968a8181"
)

// fakeClock is a manually controlled deterministic clock.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.t = c.t.Add(d)
	return c.t
}

// sequenceReader returns a deterministic, non-repeating byte stream. It is
// safe for concurrent use and never errors.
type sequenceReader struct {
	mu  sync.Mutex
	cur byte
}

func (r *sequenceReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range p {
		r.cur++
		p[i] = r.cur
	}
	return len(p), nil
}

func newTestGuard(t *testing.T) (*quicguard.Guard, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	key := bytes.Repeat([]byte{0x55}, 32)
	hmacKey := bytes.Repeat([]byte{0xaa}, 32)
	g, err := quicguard.NewGuard(quicguard.Config{
		TokenAEADKey:       key,
		ResetHMACKey:       hmacKey,
		TokenTTL:           10 * time.Second,
		MaxReplayEntries:   16,
		MaxQueuedDatagrams: 8,
		Now:                clock.Now,
		Random:             &sequenceReader{},
	})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g, clock
}

func mustAddr(t *testing.T, s string) netip.AddrPort {
	t.Helper()
	addr, err := netip.ParseAddrPort(s)
	if err != nil {
		t.Fatalf("ParseAddrPort(%q): %v", s, err)
	}
	return addr
}

func appendCID(b, cid []byte) []byte {
	b = append(b, byte(len(cid)))
	return append(b, cid...)
}

// buildLongPacket builds an Initial/0-RTT/Handshake packet with a correct
// RFC 9000 Length field. payload is padded to the requested total packet size.
func buildLongPacket(packetType byte, dcid, scid, token []byte, totalSize int) []byte {
	var body bytes.Buffer
	body.WriteByte(0xc0 | (packetType << 4) | 0x40)
	body.Write([]byte{0, 0, 0, 1})
	body.Write(appendCID(nil, dcid))
	body.Write(appendCID(nil, scid))
	if packetType == 0 { // Initial carries a token length-prefixed field
		body.Write(quicguard.EncodeVarInt(nil, uint64(len(token))))
		body.Write(token)
	}
	headerPrefix := body.Bytes()

	out := append([]byte(nil), headerPrefix...)
	// The Length field counts bytes after it, so its encoded varint width must
	// be subtracted when solving for the payload length to hit totalSize.
	remaining := totalSize - len(headerPrefix)
	payloadLen := -1
	for _, width := range []int{1, 2, 4, 8} {
		candidate := remaining - width
		if candidate >= 0 && len(quicguard.EncodeVarInt(nil, uint64(candidate))) == width {
			payloadLen = candidate
			break
		}
	}
	if payloadLen < 0 {
		panic("totalSize too small for header")
	}
	out = quicguard.EncodeVarInt(out, uint64(payloadLen))
	payload := bytes.Repeat([]byte{0x77}, payloadLen)
	out = append(out, payload...)
	return out
}

func initialPacket(dcid, scid, token []byte, totalSize int) []byte {
	return buildLongPacket(0, dcid, scid, token, totalSize)
}

func handshakePacket(dcid, scid []byte, totalSize int) []byte {
	return buildLongPacket(2, dcid, scid, nil, totalSize)
}

func zeroRTTPacket(dcid, scid []byte, totalSize int) []byte {
	return buildLongPacket(1, dcid, scid, nil, totalSize)
}

// shortPacket builds a 1-RTT packet with the fixed bit set and a DCID.
func shortPacket(dcid []byte, totalSize int) []byte {
	out := []byte{0x40}
	out = appendCID(out, dcid)
	out = append(out, bytes.Repeat([]byte{0x99}, totalSize-len(out))...)
	return out
}

// coalescedDatagram concatenates a Handshake and a 1-RTT packet inside one
// datagram to exercise merged byte accounting.
func coalescedDatagram(dcid, scid []byte) []byte {
	first := zeroRTTPacket(dcid, scid, 200)
	second := shortPacket(dcid, 100)
	return append(first, second...)
}
