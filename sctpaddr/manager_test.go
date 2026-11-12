package sctpaddr

import (
	"bytes"
	"net/netip"
	"testing"
)

var (
	addr1 = netip.MustParseAddr("10.0.0.1")
	addr2 = netip.MustParseAddr("10.0.0.2")
	addr3 = netip.MustParseAddr("10.0.0.3")
	addr4 = netip.MustParseAddr("10.0.0.4")

	testKey = []byte("association-shared-secret")
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(Config{
		LocalPort:  5000,
		PeerPort:   6000,
		LocalTag:   0x11111111,
		PeerTag:    0x22222222,
		HMACKey:    testKey,
		HMACID:     HMACSHA256,
		LocalAddrs: []netip.Addr{addr1, addr2},
		Primary:    addr1,
		NextSN:     100,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// asconf builds a validly signed ASCONF datagram as the peer would send it.
func asconf(t *testing.T, serial uint32, op Operation, addr netip.Addr) []byte {
	t.Helper()
	var opType uint16
	switch op {
	case OpAddIP:
		opType = ParamASCONFAddIP
	case OpDeleteIP:
		opType = ParamASCONFDeleteIP
	case OpSetPrimary:
		opType = ParamASCONFSetPrimary
	default:
		t.Fatalf("unknown op %d", op)
	}
	param, err := addressParam(opType, addr)
	if err != nil {
		t.Fatalf("addressParam: %v", err)
	}
	hdr := CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: 0x11111111}
	pkt, err := signPacket(testKey, HMACSHA256, hdr, []Chunk{buildASCONF(serial, param)})
	if err != nil {
		t.Fatalf("signPacket: %v", err)
	}
	return pkt
}

func retag(pkt []byte, tag uint32) []byte {
	out := bytes.Clone(pkt)
	out[4] = byte(tag >> 24)
	out[5] = byte(tag >> 16)
	out[6] = byte(tag >> 8)
	out[7] = byte(tag)
	return out
}

func bytesClone(pkt []byte) []byte { return bytes.Clone(pkt) }

// tamperLastByte flips the last byte of the AUTH HMAC field, never the chunk
// padding that may follow it.
func tamperLastByte(pkt []byte) []byte {
	out := bytes.Clone(pkt)
	_, chunks, err := ParsePacket(out)
	if err != nil || len(chunks) == 0 || chunks[len(chunks)-1].Type != ChunkAUTH {
		panic("packet without trailing AUTH chunk")
	}
	auth := chunks[len(chunks)-1]
	if len(auth.Data) < 3 {
		panic("AUTH chunk too short")
	}
	// Walk the datagram to the AUTH chunk header: 12-byte common header,
	// then each preceding chunk occupies its padded length.
	off := 12
	for _, c := range chunks[:len(chunks)-1] {
		off += pad4(4 + len(c.Data))
	}
	authLen := 4 + len(auth.Data)
	hmacEnd := off + authLen // one past the HMAC, before any chunk padding
	out[hmacEnd-1] ^= 0xFF
	return out
}

func resequence(pkt []byte, serial uint32) []byte {
	out := bytes.Clone(pkt)
	// common header (12) + chunk header (4) then the 4-byte ASCONF serial.
	out[16] = byte(serial >> 24)
	out[17] = byte(serial >> 16)
	out[18] = byte(serial >> 8)
	out[19] = byte(serial)
	return out
}

// parseACK verifies the response is an authenticated ASCONF ACK tagged for the
// peer and returns the serial and success/error indication details.
func parseACK(t *testing.T, m *Manager, raw []byte) (serial uint32, ok bool, cause uint16) {
	t.Helper()
	hdr, chunks, err := ParsePacket(raw)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if hdr.VerificationTag != m.peerTag {
		t.Fatalf("ACK tag = %#x, want %#x", hdr.VerificationTag, m.peerTag)
	}
	if len(chunks) != 2 || chunks[0].Type != ChunkASCONFACK || chunks[1].Type != ChunkAUTH {
		t.Fatalf("ACK must be ASCONF ACK + AUTH, got %+v", chunks)
	}
	if err := verifyAUTH(m.hmacKey, hdr, chunks); err != nil {
		t.Fatalf("ACK AUTH: %v", err)
	}
	if len(chunks[0].Data) < 4 {
		t.Fatalf("short ASCONF ACK")
	}
	serial = uint32(chunks[0].Data[0])<<24 | uint32(chunks[0].Data[1])<<16 |
		uint32(chunks[0].Data[2])<<8 | uint32(chunks[0].Data[3])
	params, err := parseChunkParams(chunks[0].Data[4:])
	if err != nil || len(params) != 1 {
		t.Fatalf("ACK params: %v", err)
	}
	switch params[0].typ {
	case ParamSuccessInd:
		return serial, true, 0
	case ParamErrorCauseInd:
		inner, err := parseChunkParams(params[0].raw[4:])
		if err != nil || len(inner) != 1 {
			t.Fatalf("error cause parse: %v", err)
		}
		cause = uint16(inner[0].raw[0])<<8 | uint16(inner[0].raw[1])
		return serial, false, cause
	default:
		t.Fatalf("unknown indication %#x", params[0].typ)
		return 0, false, 0
	}
}
