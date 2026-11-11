package vlan

import (
	"bytes"
	"errors"
	"testing"
)

var (
	dstMAC = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	srcMAC = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
)

// buildFrame assembles an Ethernet frame at the byte level so tests exercise
// exactly what the receive path would see on the wire.
func buildFrame(t *testing.T, tags []Tag, payload []byte) []byte {
	t.Helper()
	f := &Frame{Dst: dstMAC, Src: srcMAC, Tags: tags, Body: payload}
	return f.Marshal()
}

func tag(tpid uint16, pcp uint8, dei bool, vid uint16) Tag {
	return Tag{TPID: tpid, PCP: pcp, DEI: dei, VID: vid}
}

func TestParseFrameClasses(t *testing.T) {
	payload := append([]byte{0x08, 0x00}, bytes.Repeat([]byte{0xAB}, 20)...)

	cases := []struct {
		name  string
		tags  []Tag
		class StackClass
	}{
		{"untagged", nil, StackUntagged},
		{"priority", []Tag{tag(EtherTypeVLAN, 5, true, 0)}, StackPriority},
		{"single", []Tag{tag(EtherTypeVLAN, 0, false, 100)}, StackSingle},
		{"double", []Tag{
			tag(EtherTypeQinQ, 0, false, 200),
			tag(EtherTypeVLAN, 3, false, 100),
		}, StackDouble},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := buildFrame(t, tc.tags, payload)
			f, err := ParseFrame(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if f.Class() != tc.class {
				t.Fatalf("class = %s, want %s", f.Class(), tc.class)
			}
			if len(f.Tags) != len(tc.tags) {
				t.Fatalf("tags = %d, want %d", len(f.Tags), len(tc.tags))
			}
			for i, want := range tc.tags {
				if f.Tags[i] != want {
					t.Fatalf("tag %d = %+v, want %+v", i, f.Tags[i], want)
				}
			}
			if !bytes.Equal(f.Body, payload) {
				t.Fatalf("body mismatch: %x vs %x", f.Body, payload)
			}
			if f.Dst != dstMAC || f.Src != srcMAC {
				t.Fatalf("MAC headers not preserved")
			}
			if rebuilt := f.Marshal(); !bytes.Equal(rebuilt, raw) {
				t.Fatalf("roundtrip mismatch\n got %x\nwant %x", rebuilt, raw)
			}
		})
	}
}

func TestParseFrameTruncation(t *testing.T) {
	full := buildFrame(t, []Tag{
		tag(EtherTypeQinQ, 0, false, 200),
		tag(EtherTypeVLAN, 0, false, 100),
	}, []byte{0x08, 0x00, 0x01})

	// firstOK is the first prefix that contains both tags and the full
	// two-byte terminal ethertype: 12-byte MAC header + 8 tag bytes + 2 = 22.
	const firstOK = 12 + 2*4 + 2
	for n := 0; n < len(full); n++ {
		_, err := ParseFrame(full[:n])
		if n < firstOK && !errors.Is(err, ErrTruncated) {
			t.Fatalf("length %d: err = %v, want ErrTruncated", n, err)
		}
		if n >= firstOK && err != nil {
			t.Fatalf("length %d: err = %v, want success", n, err)
		}
	}
	if _, err := ParseFrame(full); err != nil {
		t.Fatalf("full frame should parse: %v", err)
	}

	// A frame whose terminal ethertype is cut to one byte must be rejected.
	// The payload starts at 12 + 8 = 20, so keep one of its two bytes.
	headerOnly := full[:21]
	if _, err := ParseFrame(headerOnly); !errors.Is(err, ErrTruncated) {
		t.Fatalf("header-only: err = %v, want ErrTruncated", err)
	}

	// A tag TPID with fewer than four bytes remaining.
	tail := make([]byte, 14)
	copy(tail[0:6], dstMAC[:])
	copy(tail[6:12], srcMAC[:])
	putBE16(tail[12:14], EtherTypeVLAN)
	if _, err := ParseFrame(tail); !errors.Is(err, ErrTruncated) {
		t.Fatalf("dangling tag: %v", err)
	}
}

func TestParseFrameRejectsThirdTag(t *testing.T) {
	// Emulate the wire directly so the frame carries three nested 802.1Q
	// headers, which exceeds MaxTags and must be refused on sight.
	wire := make([]byte, 0, 12+3*4+2)
	wire = append(wire, dstMAC[:]...)
	wire = append(wire, srcMAC[:]...)
	for _, tg := range []Tag{
		tag(EtherTypeQinQ, 0, false, 300),
		tag(EtherTypeVLAN, 0, false, 200),
		tag(EtherTypeVLAN, 0, false, 100),
	} {
		buf := make([]byte, 4)
		putBE16(buf[0:2], tg.TPID)
		tci := tg.VID | uint16(tg.PCP)<<13
		putBE16(buf[2:4], tci)
		wire = append(wire, buf...)
	}
	wire = append(wire, 0x08, 0x00)

	if _, err := ParseFrame(wire); !errors.Is(err, ErrTagStackTooDeep) {
		t.Fatalf("err = %v, want ErrTagStackTooDeep", err)
	}
}

func TestMarshalDoesNotAliasInput(t *testing.T) {
	raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 2, true, 77)}, []byte{0x08, 0x00, 0xAA})
	f, err := ParseFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	f.Tags[0].VID = 4095
	out := f.Marshal()
	if bytes.Equal(out, raw) {
		t.Fatal("marshal output aliased the original frame bytes")
	}
	if got := beUint16(raw[14:16]) & 0x0FFF; got != 77 {
		t.Fatalf("input frame mutated: VID = %d", got)
	}
}

func TestPCPAndDEIPreserved(t *testing.T) {
	raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 7, true, 33)}, []byte{0x08, 0x00})
	f, err := ParseFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	if f.Tags[0].PCP != 7 || !f.Tags[0].DEI || f.Tags[0].VID != 33 {
		t.Fatalf("TCI decode wrong: %+v", f.Tags[0])
	}
}
