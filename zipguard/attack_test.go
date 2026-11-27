package zipguard

import (
	"bytes"
	"errors"
	"hash/crc32"
	"testing"
)

func storedHeader(name string, crc uint32, compSize, uncompSize uint32) []byte {
	h := make([]byte, lfhFixedSize)
	put32(h[0:], sigLFH)
	put16(h[4:], 20)
	put16(h[8:], methodStored)
	put32(h[14:], crc)
	put32(h[18:], compSize)
	put32(h[22:], uncompSize)
	put16(h[26:], uint16(len(name)))
	return h
}

func centralHeader(name string, crc uint32, compSize, uncompSize, localOff uint32, externalAttrs uint32) []byte {
	h := make([]byte, cdhFixedSize)
	put32(h[0:], sigCDH)
	put16(h[4:], 3<<8|20)
	put16(h[6:], 20)
	put16(h[10:], methodStored)
	put32(h[16:], crc)
	put32(h[20:], compSize)
	put32(h[24:], uncompSize)
	put16(h[28:], uint16(len(name)))
	put32(h[38:], externalAttrs)
	put32(h[42:], localOff)
	return h
}

func eocdRecord(entries uint16, cdSize, cdOffset uint32) []byte {
	h := make([]byte, eocdFixedSize)
	put32(h[0:], sigEOCD)
	put16(h[8:], entries)
	put16(h[10:], entries)
	put32(h[12:], cdSize)
	put32(h[16:], cdOffset)
	return h
}

// TestOverlappingOffsets builds two stored entries where the second LFH sits
// inside the first entry's declared payload region.
func TestOverlappingOffsets(t *testing.T) {
	first := bytes.Repeat([]byte("A"), 16)
	secondName := "b"
	second := []byte("BBBBBBBB")

	var local bytes.Buffer

	h1 := storedHeader("a", crc32.ChecksumIEEE(first),
		uint32(len(first))+uint32(lfhFixedSize+len(secondName)+len(second)),
		uint32(len(first)))
	local.Write(h1)
	local.WriteString("a")
	local.Write(first)

	off2 := uint32(local.Len())
	h2 := storedHeader(secondName, crc32.ChecksumIEEE(second),
		uint32(len(second)), uint32(len(second)))
	local.Write(h2)
	local.WriteString(secondName)
	local.Write(second)

	var cd bytes.Buffer
	cd.Write(centralHeader("a", crc32.ChecksumIEEE(first),
		uint32(len(first))+uint32(lfhFixedSize+len(secondName)+len(second)),
		uint32(len(first)), 0, 0o644<<16))
	cd.WriteString("a")
	cd.Write(centralHeader(secondName, crc32.ChecksumIEEE(second),
		uint32(len(second)), uint32(len(second)), off2, 0o644<<16))
	cd.WriteString(secondName)

	var arc bytes.Buffer
	arc.Write(local.Bytes())
	cdOffset := uint32(arc.Len())
	arc.Write(cd.Bytes())
	arc.Write(eocdRecord(2, uint32(cd.Len()), cdOffset))

	_, err := VerifyBytes(arc.Bytes(), testPolicy())
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("err = %v, want ErrOverlap", err)
	}
}

// TestHiddenGap inserts unreferenced bytes between two valid entries and
// rewires the second CDH offset to point past them.
func TestHiddenGap(t *testing.T) {
	specs := []specEntry{
		{name: "a", content: []byte("first"), method: methodStored, hostSystem: 3},
		{name: "b", content: []byte("second"), method: methodStored, hostSystem: 3},
	}
	arc := buildArchive(specs, nil)

	firstEnd := lfhFixedSize + 1 + len("first")
	hidden := []byte("HIDDEN-PAYLOAD-NOT-REFERENCED")
	withGap := make([]byte, 0, len(arc)+len(hidden))
	withGap = append(withGap, arc[:firstEnd]...)
	gapAt := len(withGap)
	withGap = append(withGap, hidden...)
	withGap = append(withGap, arc[firstEnd:]...)

	// The second LFH now starts gap-len bytes later; patch its CDH offset.
	secondLFH := indexSignature(withGap, sigLFH, firstEnd+len(hidden))
	if secondLFH < 0 {
		t.Fatal("second LFH not found")
	}
	secondCDH := indexSignature(withGap, sigCDH, 0)
	secondCDH = indexSignature(withGap, sigCDH, secondCDH+1)
	patch32(withGap, secondCDH+42, uint32(secondLFH))
	// Central directory and EOCD cdOffset also shifted by the gap size.
	eocdOff := indexSignature(withGap, sigEOCD, 0)
	patch32(withGap, eocdOff+16, uint32(eocdOff-cdhFixedSize-1-(cdhFixedSize+1)))

	_, err := VerifyBytes(withGap, testPolicy())
	if !errors.Is(err, ErrGap) {
		t.Fatalf("err = %v, want ErrGap (gap at %d)", err, gapAt)
	}
}

// TestJanusStyleShift appends a hidden payload at the front by shifting all
// real structures forward while leaving CDH offsets untouched: coverage must
// reject either the gap or the bad LFH signature.
func TestJanusStylePrefix(t *testing.T) {
	arc := buildArchive([]specEntry{regularSpec("a", []byte("janus"))}, nil)
	prefix := []byte("META-INF/CERT.RSA fake signing blob prefix")
	evil := append(append([]byte(nil), prefix...), arc...)
	_, err := VerifyBytes(evil, testPolicy())
	if err == nil {
		t.Fatal("prefixed Janus-style archive accepted")
	}
	if !errors.Is(err, ErrGap) && !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrTrailingData) {
		t.Fatalf("err = %v, want gap/signature rejection", err)
	}
}

// TestZipBombRatio constructs a highly compressible deflate entry whose real
// ratio exceeds the policy threshold.
func TestZipBombRatio(t *testing.T) {
	content := make([]byte, 2<<20) // 2 MiB of zeroes
	specs := []specEntry{{
		name: "bomb", content: content,
		method: methodDeflate, hostSystem: 3,
	}}
	arc := buildArchive(specs, nil)

	p := testPolicy()
	p.MaxCompressionRatio = 100
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrCompressionBomb) {
		t.Fatalf("err = %v, want ErrCompressionBomb", err)
	}

	// A relaxed ratio policy accepts the same entry.
	p.MaxCompressionRatio = 100000
	if _, err := VerifyBytes(arc, p); err != nil {
		t.Fatalf("relaxed ratio policy rejected bomb: %v", err)
	}
}

// TestEntryQuotaExceeded streams an entry whose actual output crosses the
// per-entry quota even though the archive itself is small on disk.
func TestEntryQuotaExceeded(t *testing.T) {
	content := make([]byte, 64*1024)
	specs := []specEntry{{
		name: "big", content: content,
		method: methodDeflate, hostSystem: 3,
	}}
	arc := buildArchive(specs, nil)
	p := testPolicy()
	p.MaxEntryUncompressed = 1024
	p.MaxCompressionRatio = 1 << 30
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("err = %v, want ErrEntryTooLarge", err)
	}
}

func TestTotalQuotaExceeded(t *testing.T) {
	specs := []specEntry{
		{name: "a", content: make([]byte, 4096), method: methodStored, hostSystem: 3},
		{name: "b", content: make([]byte, 4096), method: methodStored, hostSystem: 3},
	}
	arc := buildArchive(specs, nil)
	p := testPolicy()
	p.MaxTotalUncompressed = 5000
	p.MaxCompressionRatio = 1 << 30
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrTotalTooLarge) {
		t.Fatalf("err = %v, want ErrTotalTooLarge", err)
	}
}

func TestTooManyEntries(t *testing.T) {
	specs := []specEntry{
		{name: "a", content: []byte("1"), method: methodStored, hostSystem: 3},
		{name: "b", content: []byte("2"), method: methodStored, hostSystem: 3},
	}
	arc := buildArchive(specs, nil)
	p := testPolicy()
	p.MaxEntries = 1
	_, err := VerifyBytes(arc, p)
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("err = %v, want ErrTooManyEntries", err)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	s := regularSpec("a", []byte("x"))
	s.overrideMethod = u16p(9) // deflate64
	_, err := VerifyBytes(buildArchive([]specEntry{s}, nil), testPolicy())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestTruncatedDeflateStream(t *testing.T) {
	good := deflateBytes([]byte("a perfectly fine payload that will be cut"))
	s := specEntry{
		name: "a", hostSystem: 3, method: methodDeflate,
		content:          []byte("a perfectly fine payload that will be cut"),
		rawPayload:       good[:len(good)-3],
		overrideCompSize: u32p(uint32(len(good) - 3)),
	}
	_, err := VerifyBytes(buildArchive([]specEntry{s}, nil), testPolicy())
	if err == nil {
		t.Fatal("truncated deflate stream accepted")
	}
}
