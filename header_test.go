package quicguard_test

import (
	"errors"
	"testing"

	"formal.local/w27-86968a8181"
)

func TestParseAllPacketTypes(t *testing.T) {
	dcid := []byte{1, 2, 3, 4}
	scid := []byte{5, 6, 7, 8}

	cases := []struct {
		name string
		raw  []byte
		want quicguard.PacketType
	}{
		{"Initial", initialPacket(dcid, scid, []byte{0xab, 0xcd}, 1200), quicguard.TypeInitial},
		{"0-RTT", zeroRTTPacket(dcid, scid, 300), quicguard.Type0RTT},
		{"Handshake", handshakePacket(dcid, scid, 300), quicguard.TypeHandshake},
		{"1-RTT", shortPacket(dcid, 100), quicguard.Type1RTT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets, err := quicguard.SplitDatagram(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			h := packets[0]
			if h.Type != tc.want {
				t.Fatalf("type = %s, want %s", h.Type, tc.want)
			}
			if string(h.DCID) != string(dcid) {
				t.Fatalf("DCID parse mismatch: %x", h.DCID)
			}
			if tc.want == quicguard.Type1RTT {
				if len(h.SCID) != 0 {
					t.Fatalf("short header must not carry an SCID: %x", h.SCID)
				}
			} else if string(h.SCID) != string(scid) {
				t.Fatalf("SCID parse mismatch: %x", h.SCID)
			}
			if len(h.Raw) != len(tc.raw) {
				t.Fatalf("raw length = %d, want %d", len(h.Raw), len(tc.raw))
			}
		})
	}
}

func TestSplitCoalescedDatagram(t *testing.T) {
	dcid := []byte{9, 9, 9}
	scid := []byte{8, 8, 8}
	first := handshakePacket(dcid, scid, 120)
	second := zeroRTTPacket(dcid, scid, 80)
	dgram := append(first, second...)

	packets, err := quicguard.SplitDatagram(dgram)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 2 {
		t.Fatalf("coalesced count = %d, want 2", len(packets))
	}
	if packets[0].Type != quicguard.TypeHandshake || packets[1].Type != quicguard.Type0RTT {
		t.Fatalf("coalesced types = %s,%s", packets[0].Type, packets[1].Type)
	}
	if packets[1].Offset != len(first) {
		t.Fatalf("second offset = %d, want %d", packets[1].Offset, len(first))
	}

	// A short-header packet terminates coalescing and consumes the remainder.
	three := append(dgram, shortPacket(dcid, 50)...)
	packets, err = quicguard.SplitDatagram(three)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 3 || packets[2].Type != quicguard.Type1RTT {
		t.Fatalf("three-packet split wrong: %+v", packets)
	}
}

func TestMalformedPackets(t *testing.T) {
	cases := map[string][]byte{
		"empty":          {},
		"truncated-long": {0xc0, 0, 0, 0, 1, 8, 1, 2},
		"fixed-bit-off":  {0x80, 0, 0, 0, 1, 0, 0},
		"bad-cid-length": append([]byte{0xc0, 0, 0, 0, 1, 21}, make([]byte, 30)...),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := quicguard.SplitDatagram(raw); err == nil {
				t.Fatal("expected parse error")
			} else if errors.Is(err, quicguard.ErrUnsupportedVersion) {
				t.Fatalf("unexpected version error: %v", err)
			}
		})
	}

	// Unsupported version is reported distinctly.
	versionNeg := []byte{0xc0, 0xff, 0, 0, 0, 0, 0}
	if _, err := quicguard.SplitDatagram(versionNeg); !errors.Is(err, quicguard.ErrUnsupportedVersion) {
		t.Fatalf("want unsupported version, got %v", err)
	}
}
