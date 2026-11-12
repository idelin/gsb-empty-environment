package netsec

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedPacket struct {
	payload []byte
	addr    net.Addr
}

type recordingPacketConn struct {
	mu      sync.Mutex
	packets []capturedPacket
}

func (c *recordingPacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.packets = append(c.packets, capturedPacket{payload: append([]byte(nil), payload...), addr: addr})
	return len(payload), nil
}

func (c *recordingPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("not readable")
}
func (c *recordingPacketConn) Close() error                     { return nil }
func (c *recordingPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *recordingPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingPacketConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingPacketConn) snapshot() []capturedPacket {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedPacket(nil), c.packets...)
}

func TestSetupRejectsDestinationAndClientPortRewrites(t *testing.T) {
	m := NewManager()
	source := "192.0.2.10:40000"

	cases := []string{
		"RTP/AVP;unicast;destination=198.51.100.24;client_port=5000-5001",
		"RTP/AVP;unicast;destination=example.com;client_port=5000-5001",
		"RTP/AVP;unicast;client_port=5001-5002",
		"RTP/AVP;unicast;client_port=5000-5002",
		"RTP/AVP;unicast;client_port=5000",
	}
	for _, transport := range cases {
		response := m.Setup("alice", source, 1, transport)
		if response.StatusCode != StatusBadRequest {
			t.Fatalf("transport %q: got %d, want %d", transport, response.StatusCode, StatusBadRequest)
		}
		if response.SessionID != "" || response.Target != nil {
			t.Fatalf("rejected setup returned a session or target: %+v", response)
		}
	}
}

func TestSetupAcceptsSourceBoundEvenPortPair(t *testing.T) {
	m := NewManager()
	response := m.Setup("alice", "192.0.2.10:40000", 1, "RTP/AVP;unicast;destination=192.0.2.10;client_port=5000-5001")
	if !response.OK() {
		t.Fatalf("setup failed: %d %s", response.StatusCode, response.Reason)
	}
	if len(response.SessionID) != 32 {
		t.Fatalf("session ID length = %d, want 32 hex characters", len(response.SessionID))
	}
	if _, err := hex.DecodeString(response.SessionID); err != nil {
		t.Fatalf("session ID is not hex: %v", err)
	}
	if response.Target == nil || response.Target.RTPPort != 5000 || response.Target.RTCPPort != 5001 {
		t.Fatalf("unexpected target: %+v", response.Target)
	}
	if response.Target.Destination.String() != "192.0.2.10" {
		t.Fatalf("target destination = %s, want source address", response.Target.Destination)
	}
}

func TestSessionIDsAre128BitAndUnique(t *testing.T) {
	m := NewManager()
	seen := make(map[string]bool)
	for i := 0; i < 32; i++ {
		response := m.Setup("alice", "192.0.2.10:40000", uint32(i), "RTP/AVP;unicast;client_port=5000-5001")
		if !response.OK() {
			t.Fatalf("setup %d failed: %d", i, response.StatusCode)
		}
		raw, err := hex.DecodeString(response.SessionID)
		if err != nil || len(raw) != 16 {
			t.Fatalf("session ID %q is not 128 bits: %v", response.SessionID, err)
		}
		if seen[response.SessionID] {
			t.Fatalf("duplicate session ID %q", response.SessionID)
		}
		seen[response.SessionID] = true
	}
}

func TestOtherPrincipalCannotPlayOrTeardown(t *testing.T) {
	m := NewManager()
	media := &recordingPacketConn{}
	setup := m.Setup("alice", "192.0.2.10:40000", 1, "RTP/AVP;unicast;client_port=5000-5001")
	if !setup.OK() {
		t.Fatalf("setup failed: %d", setup.StatusCode)
	}
	play := m.Play("alice", "192.0.2.10:40000", setup.SessionID, 2)
	if !play.OK() {
		t.Fatalf("creator play failed: %d", play.StatusCode)
	}

	if response := m.Play("mallory", "198.51.100.24:40000", setup.SessionID, 3); response.StatusCode != StatusSessionNotFound {
		t.Fatalf("cross-principal PLAY = %d, want %d", response.StatusCode, StatusSessionNotFound)
	}
	if response := m.Teardown("mallory", "198.51.100.24:40000", setup.SessionID, 4); response.StatusCode != StatusSessionNotFound {
		t.Fatalf("cross-principal TEARDOWN = %d, want %d", response.StatusCode, StatusSessionNotFound)
	}
	if err := m.WriteRTP(media, "mallory", setup.SessionID, []byte("evil")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-principal media write error = %v, want ErrSessionNotFound", err)
	}

	if response := m.Teardown("alice", "192.0.2.10:40000", setup.SessionID, 3); !response.OK() {
		t.Fatalf("creator teardown after attacker attempts = %d, want 200", response.StatusCode)
	}
	if packets := media.snapshot(); len(packets) != 0 {
		t.Fatalf("media writer received %d packet(s): %+v", len(packets), packets)
	}
}

func TestCSeqMustStrictlyIncrease(t *testing.T) {
	m := NewManager()
	media := &recordingPacketConn{}
	setup := m.Setup("alice", "192.0.2.10:40000", 10, "RTP/AVP;unicast;client_port=5000-5001")
	if !setup.OK() {
		t.Fatalf("setup failed: %d", setup.StatusCode)
	}

	for _, cseq := range []uint32{9, 10} {
		if response := m.Play("alice", "192.0.2.10:40000", setup.SessionID, cseq); response.StatusCode != StatusBadRequest {
			t.Fatalf("PLAY with regressed CSeq %d = %d, want 400", cseq, response.StatusCode)
		}
	}
	if response := m.Play("alice", "192.0.2.10:40000", setup.SessionID, 11); !response.OK() {
		t.Fatalf("PLAY with CSeq 11 failed: %d", response.StatusCode)
	}
	if response := m.Play("alice", "192.0.2.10:40000", setup.SessionID, 11); response.StatusCode != StatusBadRequest {
		t.Fatalf("duplicate CSeq = %d, want 400", response.StatusCode)
	}

	if response := m.Play("mallory", "198.51.100.24:40000", setup.SessionID, 99); response.StatusCode != StatusSessionNotFound {
		t.Fatalf("cross-principal PLAY = %d, want 454", response.StatusCode)
	}
	if response := m.Play("alice", "192.0.2.10:40000", setup.SessionID, 12); !response.OK() {
		t.Fatalf("attacker CSeq advanced the session: got %d, want 200", response.StatusCode)
	}
	if err := m.WriteRTP(media, "alice", setup.SessionID, []byte("rtp")); err != nil {
		t.Fatalf("legitimate media write failed: %v", err)
	}
	if packets := media.snapshot(); len(packets) != 1 {
		t.Fatalf("legitimate media write produced %d packets, want 1", len(packets))
	}
}

func TestMediaGoesOnlyToValidatedSourceAndClientPort(t *testing.T) {
	m := NewManager()
	media := &recordingPacketConn{}
	setup := m.Setup("alice", "192.0.2.10:40000", 1, "RTP/AVP;unicast;client_port=5000-5001")
	if !setup.OK() {
		t.Fatalf("setup failed: %d", setup.StatusCode)
	}
	if err := m.WriteRTP(media, "alice", setup.SessionID, []byte("early")); !errors.Is(err, ErrNotPlaying) {
		t.Fatalf("pre-PLAY media write error = %v, want ErrNotPlaying", err)
	}
	if response := m.Play("alice", "192.0.2.10:40000", setup.SessionID, 2); !response.OK() {
		t.Fatalf("play failed: %d", response.StatusCode)
	}
	if err := m.WriteRTP(media, "alice", setup.SessionID, []byte("rtp")); err != nil {
		t.Fatalf("media write failed: %v", err)
	}
	packets := media.snapshot()
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1", len(packets))
	}
	got, ok := packets[0].addr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("packet address type = %T", packets[0].addr)
	}
	if got.IP.String() != "192.0.2.10" || got.Port != 5000 {
		t.Fatalf("packet sent to %s:%d, want 192.0.2.10:5000", got.IP, got.Port)
	}
}

func TestLegitimateVODDeliversOnlyToClientPair(t *testing.T) {
	rtp, rtcp, ports := bindEvenUDPPair(t)
	defer rtp.Close()
	defer rtcp.Close()

	attacker, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skipf("UDP sockets are unavailable in this environment: %v", err)
	}
	defer attacker.Close()

	m := NewManager()
	transport := fmt.Sprintf("RTP/AVP;unicast;client_port=%d-%d", ports.RTP, ports.RTCP)
	setup := m.Setup("alice", rtp.LocalAddr().String(), 1, transport)
	if !setup.OK() {
		t.Fatalf("setup failed: %d", setup.StatusCode)
	}
	if response := m.Play("alice", rtp.LocalAddr().String(), setup.SessionID, 2); !response.OK() {
		t.Fatalf("play failed: %d", response.StatusCode)
	}

	if err := m.WriteRTP(rtp, "alice", setup.SessionID, []byte("rtp-packet")); err != nil {
		t.Fatalf("legitimate media write failed: %v", err)
	}
	if err := rtp.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set legitimate deadline: %v", err)
	}
	buf := make([]byte, 64)
	n, addr, err := rtp.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("legitimate client did not receive media: %v", err)
	}
	if string(buf[:n]) != "rtp-packet" || addr.Port == 0 {
		t.Fatalf("unexpected payload %q from %s", buf[:n], addr)
	}

	if err := attacker.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set attacker deadline: %v", err)
	}
	if _, _, err := attacker.ReadFromUDP(buf); err == nil {
		t.Fatal("attacker receiver unexpectedly got a media packet")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("attacker read error = %v, want timeout", err)
	}
}

func bindEvenUDPPair(t *testing.T) (*net.UDPConn, *net.UDPConn, PortPair) {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		rtp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Skipf("UDP sockets are unavailable in this environment: %v", err)
		}
		port := rtp.LocalAddr().(*net.UDPAddr).Port
		if port%2 != 0 || port == 65535 {
			rtp.Close()
			continue
		}
		rtcp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port + 1})
		if err != nil {
			rtp.Close()
			continue
		}
		return rtp, rtcp, PortPair{RTP: uint16(port), RTCP: uint16(port + 1)}
	}
	t.Fatalf("could not bind an even adjacent UDP port pair")
	return nil, nil, PortPair{}
}

func TestRawRequestParsingAndHandling(t *testing.T) {
	m := NewManager()
	raw := strings.Join([]string{
		"SETUP rtsp://example.test/vod RTSP/1.0",
		"CSeq: 7",
		"Transport: RTP/AVP;unicast;client_port=5000-5001",
		"",
		"",
	}, "\r\n")
	response := m.HandleRaw("alice", "192.0.2.10:40000", raw)
	if !response.OK() {
		t.Fatalf("raw setup failed: %d", response.StatusCode)
	}

	raw = strings.Join([]string{
		"SETUP rtsp://example.test/vod RTSP/1.0",
		"Transport: RTP/AVP;unicast;client_port=5000-5001",
		"",
		"",
	}, "\r\n")
	if response := m.HandleRaw("alice", "192.0.2.10:40000", raw); response.StatusCode != StatusBadRequest {
		t.Fatalf("missing CSeq = %d, want 400", response.StatusCode)
	}

	raw = strings.Join([]string{
		"PLAY rtsp://example.test/vod RTSP/1.0",
		"CSeq: 8",
		"Session: " + m.Setup("alice", "192.0.2.10:40000", 1, "RTP/AVP;unicast;client_port=5000-5001").SessionID,
		"",
		"",
	}, "\r\n")
	if response := m.HandleRaw("alice", "192.0.2.10:40000", raw); !response.OK() {
		t.Fatalf("raw play failed: %d", response.StatusCode)
	}
}
