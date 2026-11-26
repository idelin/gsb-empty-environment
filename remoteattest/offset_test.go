package remoteattest

import (
	"crypto/sha256"
	"testing"
)

func hasFinding(r *Report, code string) bool {
	if r == nil {
		return false
	}
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// entryHeaderOffset returns the byte offset of entry index i's PCR field.
func entryHeaderOffset(t *testing.T, log []byte, i int) int {
	t.Helper()
	pos := 0
	for e := 0; e <= i; e++ {
		if e == i {
			return pos
		}
		pos = nextEntryOffset(t, log, pos)
	}
	t.Fatal("entry not found")
	return -1
}

// digestFieldOffset returns the offset of entry i's raw d-ng digest bytes.
func digestFieldOffset(t *testing.T, log []byte, i int) int {
	t.Helper()
	pos := entryHeaderOffset(t, log, i)
	pos += 4 + 20
	name, pos := scanNUL(t, log, pos)
	size := int(log[pos])<<8 | int(log[pos+1])
	pos += 2
	// d-ng content = "sha256\0" (7 bytes) then raw digest.
	if size <= 7 {
		t.Fatalf("unexpected d-ng size %d", size)
	}
	if name != TemplateIMANG && name != TemplateIMASIG {
		t.Fatalf("unexpected template name %q", name)
	}
	return pos + 7
}

func nextEntryOffset(t *testing.T, log []byte, pos int) int {
	t.Helper()
	pos += 4 + 20
	name, pos := scanNUL(t, log, pos)
	dngSize := int(log[pos])<<8 | int(log[pos+1])
	pos += 2 + dngSize
	if name == TemplateIMASIG {
		sigSize := int(log[pos])<<8 | int(log[pos+1])
		pos += 2 + sigSize
	}
	_, pos = scanNUL(t, log, pos)
	return pos
}

func scanNUL(t *testing.T, log []byte, pos int) (string, int) {
	t.Helper()
	start := pos
	for pos < len(log) && log[pos] != 0 {
		pos++
	}
	if pos >= len(log) {
		t.Fatal("unterminated NUL string in fixture")
	}
	return string(log[start:pos]), pos + 1
}

func extendAll(pcr, entryDigest []byte) []byte {
	buf := make([]byte, 0, len(pcr)+len(entryDigest))
	buf = append(buf, pcr...)
	buf = append(buf, entryDigest...)
	sum := sha256.Sum256(buf)
	return sum[:]
}

func entryDigestFor(t *testing.T, log []byte, i int) []byte {
	t.Helper()
	entries, err := ParseIMALog(log)
	if err != nil {
		t.Fatal(err)
	}
	return entryDigest(entries[i])
}

// mustPCRDigest is the test-side wrapper that fails on incomplete banks.
func mustPCRDigest(t *testing.T, pcrs map[uint32][]byte, sel PCRSelection) []byte {
	t.Helper()
	d, err := PCRDigest(pcrs, sel)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// fullBank builds a 24-register SHA-256 bank: all zero except the PCRs
// reconstructed from the IMA replay.
func fullBank(t *testing.T, replay *ReplayResult) map[uint32][]byte {
	if t != nil {
		t.Helper()
	}
	bank := make(map[uint32][]byte)
	for i := uint32(0); i < 24; i++ {
		bank[i] = make([]byte, sha256.Size)
	}
	for idx, v := range replay.PCRs {
		bank[idx] = v
	}
	return bank
}
