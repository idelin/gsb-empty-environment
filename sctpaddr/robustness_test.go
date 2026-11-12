package sctpaddr

import (
	"net/netip"
	"testing"
)

// Truncated or otherwise malformed datagrams are dropped as malformed rather
// than panicking or driving state changes.
func TestMalformedDatagrams(t *testing.T) {
	m := newTestManager(t)
	cases := map[string][]byte{
		"empty":           nil,
		"short header":    make([]byte, 7),
		"header no chunk": make([]byte, 12),
		"truncated chunk": func() []byte {
			pkt := asconf(t, 100, OpAddIP, addr3)
			return pkt[:len(pkt)-8]
		}(),
	}
	for name, pkt := range cases {
		t.Run(name, func(t *testing.T) {
			got := m.Dispatch(pkt, addr1)
			if got.Kind != KindDropped || got.Err != ErrMalformed {
				t.Fatalf("%s: %+v", name, got)
			}
		})
	}
}

// AUTH must be the single trailing chunk: AUTH-before-ASCONF, an extra chunk
// after AUTH, and a second AUTH are all rejected regardless of HMAC validity.
func TestChunkOrderingEnforced(t *testing.T) {
	m := newTestManager(t)
	hdr := CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag}
	op, err := addressParam(ParamASCONFAddIP, addr3)
	if err != nil {
		t.Fatal(err)
	}
	asconfChunk := buildASCONF(100, op)

	build := func(order ...Chunk) []byte {
		pkt, err := signPacket(m.hmacKey, m.hmacID, hdr, order)
		if err != nil {
			t.Fatal(err)
		}
		return pkt
	}

	t.Run("AUTH first", func(t *testing.T) {
		mac, err := computeHMAC(m.hmacKey, m.hmacID, hdr, nil)
		if err != nil {
			t.Fatal(err)
		}
		pkt := marshalPacket(hdr, buildAUTHChunk(m.hmacID, mac), asconfChunk)
		if got := m.Dispatch(pkt, addr1); got.Kind != KindDropped {
			t.Fatalf("AUTH-first: %+v", got)
		}
	})

	t.Run("extra chunk after AUTH", func(t *testing.T) {
		// Sign ASCONF so the MAC is genuine, then append an unauthenticated
		// ASCONF after the AUTH chunk.
		signed := build(asconfChunk)
		rogue, err := addressParam(ParamASCONFAddIP, addr4)
		if err != nil {
			t.Fatal(err)
		}
		rogueChunk := buildASCONF(999, rogue)
		rogueWire := marshalChunk(rogueChunk)
		pkt := append(bytesClone(signed), make([]byte, pad4(len(rogueWire)))...)
		copy(pkt[len(signed):], rogueWire)
		got := m.Dispatch(pkt, addr1)
		if got.Kind != KindDropped {
			t.Fatalf("trailing chunk: %+v", got)
		}
		if m.HasAddr(addr4) {
			t.Fatal("unauthenticated trailing ASCONF changed state")
		}
	})

	if m.HasAddr(addr3) {
		t.Fatal("ordering attacks must not add addr3")
	}
}

// An ASCONF carrying an unknown operation or a broken address TLV gets an
// explicit ASCONF ACK error rather than a state mutation.
func TestUnknownOperationAndBadTLV(t *testing.T) {
	m := newTestManager(t)
	hdr := CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag}

	badOp := tlv{typ: 0xCAFE, raw: buildParam(0xCAFE, []byte{1, 2, 3, 4})}
	pkt, err := signPacket(m.hmacKey, m.hmacID, hdr, []Chunk{buildASCONF(100, badOp)})
	if err != nil {
		t.Fatal(err)
	}
	got := m.Dispatch(pkt, addr1)
	if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseUnrecognizedParam {
		t.Fatalf("unknown op: %+v", got)
	}

	// Serial 101 with a malformed nested address.
	badAddr := tlv{typ: ParamASCONFAddIP, raw: buildParam(ParamASCONFAddIP, []byte{0x05, 0x00, 0x00, 0x04, 0x0a})}
	pkt2, err := signPacket(m.hmacKey, m.hmacID, hdr, []Chunk{buildASCONF(101, badAddr)})
	if err != nil {
		t.Fatal(err)
	}
	got = m.Dispatch(pkt2, addr1)
	if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseUnrecognizedParam {
		t.Fatalf("bad TLV: %+v", got)
	}
	if m.expected != 102 {
		t.Fatalf("expected SN = %d, want 102", m.expected)
	}
}

// Probe rejects an address that is not part of the association.
func TestProbeRejectsUnknownAddress(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Probe(netip.MustParseAddr("192.0.2.9")); err == nil {
		t.Fatal("Probe accepted an unknown address")
	}
}

// New rejects configurations that cannot represent a multihomed association.
func TestNewValidatesConfig(t *testing.T) {
	base := Config{
		LocalPort:  5000,
		PeerPort:   6000,
		LocalTag:   1,
		PeerTag:    2,
		HMACKey:    testKey,
		LocalAddrs: []netip.Addr{addr1},
		Primary:    addr1,
	}
	noKey := base
	noKey.HMACKey = nil
	if _, err := New(noKey); err == nil {
		t.Fatal("New accepted empty HMAC key")
	}
	badPrimary := base
	badPrimary.Primary = addr4
	if _, err := New(badPrimary); err == nil {
		t.Fatal("New accepted a primary outside LocalAddrs")
	}
	badHMAC := base
	badHMAC.HMACID = 99
	if _, err := New(badHMAC); err == nil {
		t.Fatal("New accepted an unknown HMAC identifier")
	}
}
