package sshcert

import (
	"errors"
	"testing"
)

// TestKRLMalformedVectors walks through the parser's strict rejection paths.
// Every vector is hand-built at the byte level so no trusted serializer is
// involved in validating the defenses.
func TestKRLMalformedVectors(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)

	header := func() []byte {
		var w wireWriter
		w.bytes([]byte(krlMagic))
		w.uint32(krlVersionMajor)
		w.uint64(1)
		w.string(nil)
		return w.buf
	}

	t.Run("bad magic", func(t *testing.T) {
		raw := header()
		copy(raw, "SSHXXX")
		if _, err := ParseKRL(raw); !errors.Is(err, ErrKRLMagic) {
			t.Fatalf("want ErrKRLMagic, got %v", err)
		}
	})

	t.Run("truncated header", func(t *testing.T) {
		raw := header()
		if _, err := ParseKRL(raw[:len(raw)-2]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		var w wireWriter
		w.bytes([]byte(krlMagic))
		w.uint32(99)
		w.uint64(1)
		w.string(nil)
		if _, err := ParseKRL(w.buf); !errors.Is(err, ErrKRLVersion) {
			t.Fatalf("want ErrKRLVersion, got %v", err)
		}
	})

	t.Run("section length overruns buffer", func(t *testing.T) {
		raw := header()
		raw = append(raw, krlSectionExplicitKey)
		var w wireWriter
		w.uint32(0x7fffffff)
		raw = append(raw, w.buf...)
		if _, err := ParseKRL(raw); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("truncated section header", func(t *testing.T) {
		raw := append(header(), krlSectionExplicitKey, 0x00, 0x00)
		if _, err := ParseKRL(raw); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("unknown top level section", func(t *testing.T) {
		var w wireWriter
		w.bytes(header())
		w.byte(99)
		w.uint32(0)
		if _, err := ParseKRL(w.buf); !errors.Is(err, ErrKRLUnknownSection) {
			t.Fatalf("want ErrKRLUnknownSection, got %v", err)
		}
	})

	t.Run("duplicate top level section", func(t *testing.T) {
		b := newKRLBuilder("dup")
		b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))
		b.section(krlSectionExplicitKey, explicitKeyPayload(ca.sshBlob))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLSectionOrder) {
			t.Fatalf("want ErrKRLSectionOrder, got %v", err)
		}
	})

	t.Run("out of order top level sections", func(t *testing.T) {
		b := newKRLBuilder("order")
		// Fingerprints (3) before explicit keys (2).
		b.section(krlSectionFingerprintSHA256, fingerprintPayload(make([]byte, 32)))
		b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLSectionOrder) {
			t.Fatalf("want ErrKRLSectionOrder, got %v", err)
		}
	})

	t.Run("null section with payload", func(t *testing.T) {
		var w wireWriter
		w.bytes(header())
		w.byte(krlSectionNull)
		w.uint32(1)
		w.byte(0)
		if _, err := ParseKRL(w.buf); !errors.Is(err, ErrKRLSectionOrder) {
			t.Fatalf("want ErrKRLSectionOrder, got %v", err)
		}
	})

	t.Run("no terminating null is tolerated", func(t *testing.T) {
		b := newKRLBuilder("nonull")
		b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))
		if _, err := ParseKRL(b.bytes(false)); err != nil {
			t.Fatalf("KRL without NULL terminator should still parse: %v", err)
		}
	})

	t.Run("trailing bytes after null ignored", func(t *testing.T) {
		b := newKRLBuilder("trailing-null")
		b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))
		raw := b.bytes(true)
		raw = append(raw, 0xDE, 0xAD, 0xBE, 0xEF)
		if _, err := ParseKRL(raw); err != nil {
			t.Fatalf("bytes after NULL terminator should be ignored: %v", err)
		}
	})
}

func TestKRLMalformedCertificateSections(t *testing.T) {
	ca, other := generateTestKey(t), generateTestKey(t)

	t.Run("ca keys out of order", func(t *testing.T) {
		hi, lo := ca.sshBlob, other.sshBlob
		if bytesLess(hi, lo) {
			hi, lo = lo, hi
		}
		b := newKRLBuilder("ca-order")
		b.certSection(hi, krlCertSerialList, serialListPayload(1))
		b.certSection(lo, krlCertSerialList, serialListPayload(2))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLSectionOrder) {
			t.Fatalf("want ErrKRLSectionOrder, got %v", err)
		}
	})

	t.Run("subsection types out of order", func(t *testing.T) {
		b := newKRLBuilder("sub-order")
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1, 2))
		b.certSection(ca.sshBlob, krlCertSerialList, serialListPayload(3))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLSectionOrder) {
			t.Fatalf("want ErrKRLSectionOrder, got %v", err)
		}
	})

	t.Run("unknown subsection type", func(t *testing.T) {
		b := newKRLBuilder("sub-unknown")
		b.certSection(ca.sshBlob, 99, nil)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLUnknownSection) {
			t.Fatalf("want ErrKRLUnknownSection, got %v", err)
		}
	})

	t.Run("truncated subsection length", func(t *testing.T) {
		var rec wireWriter
		rec.string(ca.sshBlob)
		rec.string(nil)
		rec.byte(krlCertSerialList)
		rec.uint32(50) // subsection string claims 50 bytes, provides none
		b := newKRLBuilder("trunc-sub")
		b.section(krlSectionCertificates, rec.buf)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("cert record missing reserved string", func(t *testing.T) {
		var rec wireWriter
		rec.string(ca.sshBlob)
		// reserved string intentionally omitted
		rec.byte(krlCertSerialList)
		rec.string(serialListPayload(1))
		b := newKRLBuilder("no-reserved")
		b.section(krlSectionCertificates, rec.buf)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadCAKey) {
			t.Fatalf("want ErrKRLBadCAKey, got %v", err)
		}
	})

	t.Run("serial list ragged by construction", func(t *testing.T) {
		var p wireWriter
		p.uint32(7) // dangling four bytes inside the list string
		p.uint64(1)
		b := newKRLBuilder("reserved")
		b.certSection(ca.sshBlob, krlCertSerialList, p.buf)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("serial list unsorted", func(t *testing.T) {
		b := newKRLBuilder("list-sort")
		b.certSection(ca.sshBlob, krlCertSerialList, serialListPayload(2, 1))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadSerialList) {
			t.Fatalf("want ErrKRLBadSerialList, got %v", err)
		}
	})

	t.Run("serial list ragged tail", func(t *testing.T) {
		p := serialListPayload(1, 2)
		p = append(p, 0x00, 0x00, 0x00) // 3 dangling bytes
		b := newKRLBuilder("ragged")
		b.certSection(ca.sshBlob, krlCertSerialList, p)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrTruncated) {
			t.Fatalf("want ErrTruncated, got %v", err)
		}
	})

	t.Run("serial range inverted", func(t *testing.T) {
		b := newKRLBuilder("range-inv")
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(10, 5))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadSerialRange) {
			t.Fatalf("want ErrKRLBadSerialRange, got %v", err)
		}
	})

	t.Run("serial range overlapping", func(t *testing.T) {
		b := newKRLBuilder("range-overlap")
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1, 10))
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(5, 20))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadSerialRange) {
			t.Fatalf("want ErrKRLBadSerialRange, got %v", err)
		}
	})

	t.Run("open range followed by another range", func(t *testing.T) {
		b := newKRLBuilder("open-tail")
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1, 0))
		b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(2, 3))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadSerialRange) {
			t.Fatalf("want ErrKRLBadSerialRange, got %v", err)
		}
	})

	t.Run("bitmap offsets out of order", func(t *testing.T) {
		b := newKRLBuilder("bitmap-order")
		b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(64, []byte{0x01}))
		b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(64, []byte{0x02}))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadBitmap) {
			t.Fatalf("want ErrKRLBadBitmap, got %v", err)
		}
	})

	t.Run("empty bitmap rejected", func(t *testing.T) {
		b := newKRLBuilder("bitmap-empty")
		b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(0, nil))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadBitmap) {
			t.Fatalf("want ErrKRLBadBitmap, got %v", err)
		}
	})

	t.Run("negative mpint bitmap rejected", func(t *testing.T) {
		// High bit set on the first magnitude byte marks a negative mpint.
		negative := []byte{0x80}
		b := newKRLBuilder("bitmap-neg")
		b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(0, negative))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadBitmap) {
			t.Fatalf("want ErrKRLBadBitmap, got %v", err)
		}
	})

	t.Run("non canonical mpint bitmap rejected", func(t *testing.T) {
		// Leading zero without a following high bit is non-minimal.
		nonCanonical := []byte{0x00, 0x01}
		b := newKRLBuilder("bitmap-canon")
		b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(0, nonCanonical))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadBitmap) {
			t.Fatalf("want ErrKRLBadBitmap, got %v", err)
		}
	})

	t.Run("trailing bytes after bitmap rejected", func(t *testing.T) {
		var p wireWriter
		p.uint64(0)
		p.string([]byte{0x01})
		p.byte(0xAA) // dangling byte inside the subsection string
		b := newKRLBuilder("bitmap-trail")
		b.certSection(ca.sshBlob, krlCertSerialBitmap, p.buf)
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadBitmap) {
			t.Fatalf("want ErrKRLBadBitmap, got %v", err)
		}
	})

	t.Run("key id unsorted", func(t *testing.T) {
		b := newKRLBuilder("keyid-sort")
		b.certSection(ca.sshBlob, krlCertKeyID, keyIDPayload("b", "a"))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadKeyID) {
			t.Fatalf("want ErrKRLBadKeyID, got %v", err)
		}
	})

	t.Run("wildcard key id with siblings", func(t *testing.T) {
		b := newKRLBuilder("keyid-wild")
		b.certSection(ca.sshBlob, krlCertKeyID, keyIDPayload("", "a"))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadKeyID) {
			t.Fatalf("want ErrKRLBadKeyID, got %v", err)
		}
	})

	t.Run("explicit key unsorted", func(t *testing.T) {
		hi, lo := ca.sshBlob, other.sshBlob
		if bytesLess(hi, lo) {
			hi, lo = lo, hi
		}
		b := newKRLBuilder("explicit-sort")
		b.section(krlSectionExplicitKey, explicitKeyPayload(hi, lo))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadExplicitKey) {
			t.Fatalf("want ErrKRLBadExplicitKey, got %v", err)
		}
	})

	t.Run("fingerprint wrong width", func(t *testing.T) {
		b := newKRLBuilder("fp-width")
		b.section(krlSectionFingerprintSHA256, fingerprintPayload([]byte{1, 2, 3}))
		if _, err := ParseKRL(b.bytes(true)); !errors.Is(err, ErrKRLBadFingerprint) {
			t.Fatalf("want ErrKRLBadFingerprint, got %v", err)
		}
	})
}
