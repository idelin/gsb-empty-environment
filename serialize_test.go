package featureneg

import (
	"encoding/binary"
	"testing"
)

func sampleOffer() *Offer {
	return &Offer{
		Party:      "alice",
		Protocol:   Range{1, 4},
		Required:   []string{"base"},
		Optional:   []string{"zip", "telemetry"},
		Preference: []string{"zip", "telemetry"},
		Features: map[string]Feature{
			"base":      feat("base", 1, 3, nil, nil),
			"zip":       feat("zip", 1, 2, []Dep{{Name: "base", MinVersion: 2}}, nil),
			"telemetry": feat("telemetry", 1, 1, nil, []string{"zip"}),
		},
	}
}

func TestOfferSerializationRoundTrip(t *testing.T) {
	o := sampleOffer()
	dec, err := DecodeOffer(EncodeOffer(o))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.Party != o.Party || dec.Protocol != o.Protocol {
		t.Fatalf("header mismatch: %+v", dec)
	}
	if len(dec.Features) != len(o.Features) {
		t.Fatalf("features = %d, want %d", len(dec.Features), len(o.Features))
	}
	zip := dec.Features["zip"]
	if zip.Requires[0] != (Dep{Name: "base", MinVersion: 2}) {
		t.Fatalf("zip requires = %+v", zip.Requires)
	}
	if dec.Features["telemetry"].Conflicts[0] != "zip" {
		t.Fatalf("telemetry conflicts = %v", dec.Features["telemetry"].Conflicts)
	}
	// Deterministic encoding (needed for transcript hashing).
	if string(EncodeOffer(o)) != string(EncodeOffer(dec)) {
		t.Fatal("encoding is not deterministic")
	}
}

func TestDecodeRejectsTruncated(t *testing.T) {
	buf := EncodeOffer(sampleOffer())
	for _, n := range []int{0, 1, 3, 7, len(buf) / 2, len(buf) - 1} {
		if _, err := DecodeOffer(buf[:n]); err == nil {
			t.Fatalf("truncated to %d bytes: expected error", n)
		}
	}
}

// A hostile declared length must be rejected before allocation, quickly.
func TestDecodeRejectsHostileLength(t *testing.T) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, formatVersion)
	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // party string length = 4 GiB
	if _, err := DecodeOffer(buf); err == nil {
		t.Fatal("expected error for hostile string length")
	}
	// Hostile list count: valid header, then feature count = 4 billion.
	o := sampleOffer()
	full := EncodeOffer(o)
	// Find offset of the feature count: version(4)+party(4+5)+proto(8)
	// +required(4+8)+optional(4+... ) — instead just corrupt the last
	// plausible count by rebuilding a minimal buffer.
	_ = full
	min := make([]byte, 0, 64)
	min = binary.BigEndian.AppendUint32(min, formatVersion)
	min = binary.BigEndian.AppendUint32(min, 0)          // party ""
	min = binary.BigEndian.AppendUint32(min, 1)          // proto min
	min = binary.BigEndian.AppendUint32(min, 1)          // proto max
	min = binary.BigEndian.AppendUint32(min, 0)          // required
	min = binary.BigEndian.AppendUint32(min, 0)          // optional
	min = binary.BigEndian.AppendUint32(min, 0)          // preference
	min = binary.BigEndian.AppendUint32(min, 0xFFFFFFFF) // feature count
	if _, err := DecodeOffer(min); err == nil {
		t.Fatal("expected error for hostile feature count")
	}
}

func TestDecodeRejectsTrailingBytes(t *testing.T) {
	buf := append(EncodeOffer(sampleOffer()), 0x00)
	if _, err := DecodeOffer(buf); err == nil {
		t.Fatal("expected trailing-bytes error")
	}
}

func TestCertificateSerializationRoundTrip(t *testing.T) {
	c := &Certificate{
		Transcript:      [32]byte{1, 2, 3},
		ProtocolVersion: 7,
		Selected:        []Selection{{Name: "base", Version: 3}, {Name: "zip", Version: 2}},
	}
	dec, err := DecodeCertificate(EncodeCertificate(c))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.ProtocolVersion != 7 || len(dec.Selected) != 2 || dec.Selected[1].Name != "zip" {
		t.Fatalf("decoded = %+v", dec)
	}
	if dec.Summary() != c.Summary() {
		t.Fatal("summary mismatch after round trip")
	}
}
