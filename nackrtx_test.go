package nackrtx

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

// buildNACK builds an RTCP generic NACK packet from PID/BLP pairs.
func buildNACK(mediaSSRC uint32, fci ...[2]uint16) []byte {
	n := 3 + len(fci) // 32-bit words: header + 2 SSRC + FCI
	pkt := make([]byte, n*4)
	pkt[0] = 0x80 | rtcpFMTNACK
	pkt[1] = rtcpPTNACK
	binary.BigEndian.PutUint16(pkt[2:], uint16(n-1))
	binary.BigEndian.PutUint32(pkt[4:], 0x11223344)
	binary.BigEndian.PutUint32(pkt[8:], mediaSSRC)
	for i, f := range fci {
		binary.BigEndian.PutUint16(pkt[12+i*4:], f[0])
		binary.BigEndian.PutUint16(pkt[14+i*4:], f[1])
	}
	return pkt
}

type rtpInfo struct {
	pt      uint8
	seq     uint16
	ts      uint32
	ssrc    uint32
	payload []byte
}

func parseRTP(t *testing.T, pkt []byte) rtpInfo {
	t.Helper()
	if len(pkt) < rtpHeaderSize {
		t.Fatalf("RTP packet too short: %d bytes", len(pkt))
	}
	return rtpInfo{
		pt:      pkt[1] & 0x7f,
		seq:     binary.BigEndian.Uint16(pkt[2:]),
		ts:      binary.BigEndian.Uint32(pkt[4:]),
		ssrc:    binary.BigEndian.Uint32(pkt[8:]),
		payload: pkt[rtpHeaderSize:],
	}
}

func TestParseNACK(t *testing.T) {
	// PID=100, BLP bits 0 and 2 set -> 101 and 103 also lost.
	pkt := buildNACK(1, [2]uint16{100, 0b1010_0000_0000_0000})
	lost, err := ParseNACK(pkt)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{100, 101, 103}
	if fmt.Sprint(lost) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", lost, want)
	}

	// BLP wraps past 65535: PID=65534, bit 1 -> 65535+1 = 0.
	pkt = buildNACK(1, [2]uint16{65534, 0b0100_0000_0000_0000})
	lost, err = ParseNACK(pkt)
	if err != nil {
		t.Fatal(err)
	}
	want = []uint16{65534, 0}
	if fmt.Sprint(lost) != fmt.Sprint(want) {
		t.Fatalf("wrap: got %v, want %v", lost, want)
	}

	// Multiple FCI entries.
	pkt = buildNACK(1, [2]uint16{10, 0}, [2]uint16{20, 0x8000})
	lost, err = ParseNACK(pkt)
	if err != nil {
		t.Fatal(err)
	}
	want = []uint16{10, 20, 21}
	if fmt.Sprint(lost) != fmt.Sprint(want) {
		t.Fatalf("multi FCI: got %v, want %v", lost, want)
	}

	if _, err = ParseNACK([]byte{0x80}); err == nil {
		t.Fatal("expected error for short packet")
	}
	if _, err = ParseNACK([]byte{0x80, 200, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected error for non-NACK packet")
	}
}

// newTestSender returns a Sender with a controllable clock.
func newTestSender(maxAge time.Duration, maxBytes int, rtt time.Duration) (*Sender, *time.Time) {
	now := time.Now()
	buf := NewBuffer(maxAge, maxBytes, rtt)
	buf.now = func() time.Time { return now }
	return NewSender(buf, 1000, 2000, 96, 97, 3000), &now
}

func TestRetransmitOrderAndPayload(t *testing.T) {
	s, _ := newTestSender(time.Second, 0, 50*time.Millisecond)

	// Send seq 0..9; "lose" 3, 5, 6 (simulated by simply NACKing them).
	payloads := make([][]byte, 10)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("frame-%d", i))
		pkt := parseRTP(t, s.Send(payloads[i]))
		if pkt.seq != uint16(i) || pkt.ssrc != 1000 || pkt.pt != 96 {
			t.Fatalf("bad main packet %+v", pkt)
		}
	}

	// NACK: PID=3; PID=5 with BLP bit 0 -> 5 and 6.
	nack := buildNACK(1000, [2]uint16{3, 0}, [2]uint16{5, 0x8000})
	rtx, err := s.HandleRTCP(nack)
	if err != nil {
		t.Fatal(err)
	}
	if len(rtx) != 3 {
		t.Fatalf("got %d RTX packets, want 3", len(rtx))
	}
	for i, lostSeq := range []uint16{3, 5, 6} {
		p := parseRTP(t, rtx[i])
		if p.ssrc != 2000 || p.pt != 97 {
			t.Fatalf("RTX %d: wrong stream %+v", i, p)
		}
		if p.seq != uint16(i) {
			t.Fatalf("RTX %d: seq=%d, want independent %d", i, p.seq, i)
		}
		if p.ts != uint32(lostSeq)*3000 {
			t.Fatalf("RTX %d: ts=%d, want original %d", i, p.ts, uint32(lostSeq)*3000)
		}
		if got := binary.BigEndian.Uint16(p.payload); got != lostSeq {
			t.Fatalf("RTX %d: orig seq=%d, want %d", i, got, lostSeq)
		}
		if got := string(p.payload[2:]); got != string(payloads[lostSeq]) {
			t.Fatalf("RTX %d: payload=%q, want %q", i, got, payloads[lostSeq])
		}
	}
}

func TestNoRetransmitWithinRTT(t *testing.T) {
	s, now := newTestSender(time.Second, 0, 50*time.Millisecond)
	s.Send([]byte("a"))
	s.Send([]byte("b"))

	nack := buildNACK(1000, [2]uint16{0, 0})
	rtx, err := s.HandleRTCP(nack)
	if err != nil || len(rtx) != 1 {
		t.Fatalf("first NACK: rtx=%d err=%v", len(rtx), err)
	}
	// Same NACK again within one RTT: suppressed.
	rtx, err = s.HandleRTCP(nack)
	if err != nil || len(rtx) != 0 {
		t.Fatalf("within RTT: rtx=%d err=%v, want 0", len(rtx), err)
	}
	// After one RTT the receiver may NACK again and we retransmit.
	*now = now.Add(60 * time.Millisecond)
	rtx, err = s.HandleRTCP(nack)
	if err != nil || len(rtx) != 1 {
		t.Fatalf("after RTT: rtx=%d err=%v, want 1", len(rtx), err)
	}
	if got := parseRTP(t, rtx[0]).seq; got != 1 {
		t.Fatalf("second RTX seq=%d, want 1 (independent space)", got)
	}
}

func TestEvictionByAge(t *testing.T) {
	s, now := newTestSender(100*time.Millisecond, 0, time.Millisecond)
	s.Send([]byte("old"))
	*now = now.Add(200 * time.Millisecond)
	s.Send([]byte("new"))

	rtx, err := s.HandleRTCP(buildNACK(1000, [2]uint16{0, 0x8000})) // 0 and 1
	if err != nil {
		t.Fatal(err)
	}
	if len(rtx) != 1 {
		t.Fatalf("got %d RTX, want 1 (seq 0 expired)", len(rtx))
	}
	if got := binary.BigEndian.Uint16(parseRTP(t, rtx[0]).payload); got != 1 {
		t.Fatalf("retransmitted seq %d, want 1", got)
	}
}

func TestEvictionByBytes(t *testing.T) {
	// Each packet accounts 12 + 10 = 22 bytes; cap holds only the last two.
	s, _ := newTestSender(time.Minute, 44, time.Millisecond)
	for i := 0; i < 4; i++ {
		s.Send([]byte(fmt.Sprintf("payload-%d", i)))
	}
	rtx, err := s.HandleRTCP(buildNACK(1000, [2]uint16{0, 0xE000})) // 0,1,2,3
	if err != nil {
		t.Fatal(err)
	}
	if len(rtx) != 2 {
		t.Fatalf("got %d RTX, want 2 (oldest evicted)", len(rtx))
	}
	for i, want := range []uint16{2, 3} {
		if got := binary.BigEndian.Uint16(parseRTP(t, rtx[i]).payload); got != want {
			t.Fatalf("RTX %d: orig seq %d, want %d", i, got, want)
		}
	}
}

func TestSequenceWraparound(t *testing.T) {
	s, _ := newTestSender(time.Minute, 0, time.Millisecond)
	s.seq = 65534 // start near the wrap
	s.Send([]byte("wrap-65534"))
	s.Send([]byte("wrap-65535"))
	s.Send([]byte("wrap-0"))
	s.Send([]byte("wrap-1"))

	// NACK 65535 and (via BLP) 0.
	rtx, err := s.HandleRTCP(buildNACK(1000, [2]uint16{65535, 0x8000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rtx) != 2 {
		t.Fatalf("got %d RTX, want 2", len(rtx))
	}
	for i, want := range []struct {
		seq     uint16
		payload string
	}{
		{65535, "wrap-65535"},
		{0, "wrap-0"},
	} {
		p := parseRTP(t, rtx[i])
		if got := binary.BigEndian.Uint16(p.payload); got != want.seq {
			t.Fatalf("RTX %d: orig seq %d, want %d", i, got, want.seq)
		}
		if got := string(p.payload[2:]); got != want.payload {
			t.Fatalf("RTX %d: payload %q, want %q", i, got, want.payload)
		}
	}
}

func TestWraparoundNoStaleConfusion(t *testing.T) {
	// Small byte cap: old generation of seq 5 must be evicted before the
	// wrapped seq 5 is stored, so a NACK never returns the stale payload.
	s, _ := newTestSender(time.Minute, 2*(rtpHeaderSize+4), time.Millisecond)
	s.seq = 5
	s.Send([]byte("old!"))
	s.seq = 0 // simulate a full cycle: send 0..5 again
	for i := 0; i <= 5; i++ {
		s.Send([]byte("new!"))
	}
	rtx, err := s.HandleRTCP(buildNACK(1000, [2]uint16{5, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rtx) != 1 {
		t.Fatalf("got %d RTX, want 1", len(rtx))
	}
	if got := string(parseRTP(t, rtx[0]).payload[2:]); got != "new!" {
		t.Fatalf("stale packet returned: payload %q, want %q", got, "new!")
	}
}
