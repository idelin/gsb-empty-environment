package netsec

import (
	"encoding/binary"
	"testing"
)

// nameWire encodes a dotted name without compression.
func nameWire(name string) []byte {
	if name == "." {
		return []byte{0}
	}
	var out []byte
	for _, label := range splitLabels(name) {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func splitLabels(name string) []string {
	var labels []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			if i > start {
				labels = append(labels, name[start:i])
			}
			start = i + 1
		}
	}
	if start < len(name) {
		labels = append(labels, name[start:])
	}
	return labels
}

func rrWire(owner []byte, typ uint16, ttl uint32, rdata []byte) []byte {
	out := append([]byte{}, owner...)
	var hdr [10]byte
	binary.BigEndian.PutUint16(hdr[0:], typ)
	binary.BigEndian.PutUint16(hdr[2:], ClassIN)
	binary.BigEndian.PutUint32(hdr[4:], ttl)
	binary.BigEndian.PutUint16(hdr[8:], uint16(len(rdata)))
	return append(append(out, hdr[:]...), rdata...)
}

// 压缩指针与大小写: the same name spelled literally, via a compression
// pointer, and in different case must canonicalize identically, so
// duplicate records collapse into one cached RRset.
func TestParseMessageCompressionAndCase(t *testing.T) {
	var msg []byte
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[0:], 0x1234)
	binary.BigEndian.PutUint16(hdr[2:], 0x8400) // QR + AA
	binary.BigEndian.PutUint16(hdr[4:], 1)      // QDCOUNT
	binary.BigEndian.PutUint16(hdr[6:], 2)      // ANCOUNT
	msg = append(msg, hdr...)

	// Question at offset 12: www.EXAMPLE.com
	msg = append(msg, nameWire("www.EXAMPLE.com")...)
	var q [4]byte
	binary.BigEndian.PutUint16(q[0:], TypeA)
	binary.BigEndian.PutUint16(q[2:], ClassIN)
	msg = append(msg, q[:]...)

	// Answer 1: owner via compression pointer to offset 12.
	msg = append(msg, rrWire([]byte{0xC0, 0x0C}, TypeA, 300,
		[]byte{192, 0, 2, 1})...)
	// Answer 2: same owner spelled literally, different case.
	msg = append(msg, rrWire(nameWire("WWW.example.COM"), TypeA, 300,
		[]byte{192, 0, 2, 1})...)

	resp, err := ParseMessage(msg)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if resp.Question.Name != "www.example.com." {
		t.Fatalf("question name not canonicalized: %q", resp.Question.Name)
	}
	if !resp.Authoritative {
		t.Fatal("AA bit not parsed")
	}
	if len(resp.Answer) != 2 {
		t.Fatalf("expected 2 answers, got %d", len(resp.Answer))
	}
	for i, rr := range resp.Answer {
		if rr.Owner != "www.example.com." {
			t.Fatalf("answer %d owner = %q, want www.example.com.", i, rr.Owner)
		}
	}

	// Both records are the same after canonicalization: one cached RRset
	// with a single record.
	c := NewCache()
	res := c.Admit(resp, "example.com")
	if len(res.Hints) != 0 {
		t.Fatalf("unexpected hints: %+v", res.Hints)
	}
	recs := mustCache(t, c, "www.example.com", TypeA)
	if len(recs) != 1 {
		t.Fatalf("compression/case duplicates not merged: %+v", recs)
	}
}

// A compressed CNAME target inside RDATA is decompressed and
// canonicalized too.
func TestParseCompressedRDataName(t *testing.T) {
	var msg []byte
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:], 0x8400)
	binary.BigEndian.PutUint16(hdr[4:], 1)
	binary.BigEndian.PutUint16(hdr[6:], 1)
	msg = append(msg, hdr...)
	msg = append(msg, nameWire("WWW.Attacker.COM")...) // offset 12
	var q [4]byte
	binary.BigEndian.PutUint16(q[0:], TypeCNAME)
	binary.BigEndian.PutUint16(q[2:], ClassIN)
	msg = append(msg, q[:]...)
	// Answer: owner pointer to 12, CNAME rdata = literal "cdn." + pointer
	// to the "Attacker.COM" suffix at offset 16.
	rdata := append(nameWire("cdn")[:len(nameWire("cdn"))-1], 0xC0, 0x10)
	msg = append(msg, rrWire([]byte{0xC0, 0x0C}, TypeCNAME, 300, rdata)...)

	resp, err := ParseMessage(msg)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if got := resp.Answer[0].RData; got != "cdn.attacker.com." {
		t.Fatalf("compressed CNAME target = %q, want cdn.attacker.com.", got)
	}
}

func TestParseRejectsPointerLoop(t *testing.T) {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[4:], 1)
	msg = append(msg, 0xC0, 0x0C) // question name points at itself
	msg = append(msg, 0, 1, 0, 1)
	if _, err := ParseMessage(msg); err == nil {
		t.Fatal("expected pointer loop to be rejected")
	}
}
