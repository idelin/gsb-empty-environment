package zipguard

import (
	"bytes"
	"hash/crc32"
	"testing"
)

// buildZip64 constructs a minimal valid zip64 archive with one stored entry
// whose 32-bit size/offset fields carry the 0xFFFFFFFF sentinels.
func buildZip64(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var local bytes.Buffer

	lfh := make([]byte, lfhFixedSize)
	put32(lfh[0:], sigLFH)
	put16(lfh[4:], 45)
	put16(lfh[8:], methodStored)
	put32(lfh[14:], crc32.ChecksumIEEE(content))
	put32(lfh[18:], u32Sentinel)
	put32(lfh[22:], u32Sentinel)
	put16(lfh[26:], uint16(len(name)))
	put16(lfh[28:], 20) // zip64 extra field: 2*8 sizes
	local.Write(lfh)
	local.WriteString(name)
	z64lfhExtra := make([]byte, 20)
	put16(z64lfhExtra[0:], 0x0001)
	put16(z64lfhExtra[2:], 16)
	put64(z64lfhExtra[4:], uint64(len(content)))
	put64(z64lfhExtra[12:], uint64(len(content)))
	local.Write(z64lfhExtra)
	local.Write(content)

	cdOffset := uint64(local.Len())

	var cd bytes.Buffer
	cdh := make([]byte, cdhFixedSize)
	put32(cdh[0:], sigCDH)
	put16(cdh[4:], 3<<8|45)
	put16(cdh[6:], 45)
	put16(cdh[10:], methodStored)
	put32(cdh[16:], crc32.ChecksumIEEE(content))
	put32(cdh[20:], u32Sentinel)
	put32(cdh[24:], u32Sentinel)
	put16(cdh[28:], uint16(len(name)))
	put16(cdh[30:], 28) // extra: uncomp, comp, offset
	put32(cdh[38:], 0o644<<16)
	put32(cdh[42:], u32Sentinel)
	cd.Write(cdh)
	cd.WriteString(name)
	z64cdExtra := make([]byte, 28)
	put16(z64cdExtra[0:], 0x0001)
	put16(z64cdExtra[2:], 24)
	put64(z64cdExtra[4:], uint64(len(content)))
	put64(z64cdExtra[12:], uint64(len(content)))
	put64(z64cdExtra[20:], 0)
	cd.Write(z64cdExtra)

	var arc bytes.Buffer
	arc.Write(local.Bytes())
	arc.Write(cd.Bytes())

	z64EOCDOff := uint64(arc.Len())
	z64EOCD := make([]byte, z64eocdFixed)
	put32(z64EOCD[0:], sigZ64)
	put64(z64EOCD[4:], 44) // size of record after this field
	put16(z64EOCD[12:], 45)
	put16(z64EOCD[14:], 45)
	put32(z64EOCD[16:], 0)
	put32(z64EOCD[20:], 0)
	put64(z64EOCD[24:], 1) // entries on disk
	put64(z64EOCD[32:], 1) // total entries
	put64(z64EOCD[40:], uint64(cd.Len()))
	put64(z64EOCD[48:], cdOffset)
	arc.Write(z64EOCD)

	locator := make([]byte, z64locFixed)
	put32(locator[0:], sigZ64L)
	put32(locator[4:], 0)
	put64(locator[8:], z64EOCDOff)
	put32(locator[16:], 1)
	arc.Write(locator)

	eocd := make([]byte, eocdFixedSize)
	put32(eocd[0:], sigEOCD)
	put16(eocd[8:], u16Sentinel)
	put16(eocd[10:], u16Sentinel)
	put32(eocd[12:], u32Sentinel)
	put32(eocd[16:], u32Sentinel)
	arc.Write(eocd)

	return arc.Bytes()
}

func put64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

func TestZip64ArchiveAccepted(t *testing.T) {
	content := []byte("zip64 stored payload")
	arc := buildZip64(t, "z64.txt", content)
	rep, err := VerifyBytes(arc, testPolicy())
	if err != nil {
		t.Fatalf("zip64 archive rejected: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "z64.txt" {
		t.Fatalf("unexpected report: %+v", rep.Entries)
	}
	if rep.Entries[0].UncompressedSize != uint64(len(content)) {
		t.Fatalf("size = %d", rep.Entries[0].UncompressedSize)
	}
}

func TestZip64CorruptedPayload(t *testing.T) {
	arc := buildZip64(t, "z64.txt", []byte("zip64 stored payload"))
	arc[30+len("z64.txt")+20] ^= 0xff
	_, err := VerifyBytes(arc, testPolicy())
	if err == nil {
		t.Fatal("corrupted zip64 payload accepted")
	}
}
