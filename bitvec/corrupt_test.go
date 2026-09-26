package bitvec

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

func sampleBitmap(t *testing.T) (naive, []byte) {
	t.Helper()
	rng := rand.New(rand.NewSource(99))
	bits := make(naive, 5000)
	for i := range bits {
		bits[i] = rng.Intn(3) == 0
	}
	buf := Encode(bits)
	if _, err := Map(buf); err != nil {
		t.Fatalf("baseline does not load: %v", err)
	}
	return bits, buf
}

// TestSingleByteMutations flips every byte of the serialized form, one
// at a time, with several deltas. The loader must reject the corruption
// (never panic); if a mutation ever slips past validation, queries must
// still stay in bounds.
func TestSingleByteMutations(t *testing.T) {
	bits, buf := sampleBitmap(t)
	ones := bits.ones()
	rejected := 0
	for i := 0; i < len(buf); i++ {
		for _, delta := range []byte{0x01, 0x80, 0xFF} {
			mut := make([]byte, len(buf))
			copy(mut, buf)
			mut[i] ^= delta
			m, err := Map(mut)
			if err != nil {
				rejected++
				continue
			}
			// Not rejected: queries must not panic and must stay in bounds.
			for q := 0; q <= len(bits); q += 97 {
				if r := m.Rank1(uint64(q), nil); r > uint64(len(bits)) {
					t.Fatalf("byte %d delta %#x: Rank1(%d)=%d out of range", i, delta, q, r)
				}
			}
			for k := 0; k < ones+2; k += 7 {
				if pos, ok := m.Select1(uint64(k), nil); ok && pos >= uint64(len(bits)) {
					t.Fatalf("byte %d delta %#x: Select1(%d)=%d out of range", i, delta, k, pos)
				}
			}
		}
	}
	total := len(buf) * 3
	if rejected != total {
		t.Fatalf("only %d/%d single-byte mutations rejected", rejected, total)
	}
}

// TestTruncations: every truncated prefix must fail to load.
func TestTruncations(t *testing.T) {
	_, buf := sampleBitmap(t)
	for _, n := range []int{0, 1, 4, 63, headerSize - 1, headerSize, headerSize + 5, len(buf) / 2, len(buf) - 1} {
		if _, err := Map(buf[:n]); err == nil {
			t.Fatalf("truncated to %d bytes loaded without error", n)
		}
	}
}

// TestTailPollution: padding bits beyond bitLen in the final raw block
// are flipped (with checksums repaired so the file still loads).
// Queries must ignore the polluted tail entirely.
func TestTailPollution(t *testing.T) {
	// Length chosen so the final block is partial and raw (mixed bits).
	bits := make(naive, 1000)
	for i := 0; i < 1000; i += 3 {
		bits[i] = true
	}
	buf := Encode(bits)
	m, err := Map(buf)
	if err != nil {
		t.Fatal(err)
	}
	// Locate the final block's payload through the directory.
	last := m.numBlocks - 1
	e := buf[m.dirOff+uint64(last)*dirEntrySize:]
	if e[0] != blkRaw {
		t.Fatalf("final block type = %d, want raw for this test", e[0])
	}
	off := m.dataOff + uint64(binary.LittleEndian.Uint32(e[4:8]))
	// Pollute every padding bit (block-local bits 488..511).
	for i := 1000; i < 1024; i++ {
		local := uint(i) - uint(last)*BlockBits
		buf[off+uint64(local/8)] |= 1 << (local % 8)
	}
	// Repair checksums so the loader accepts the polluted file.
	h := buf[:headerSize]
	binary.LittleEndian.PutUint32(h[108:112], crc32.ChecksumIEEE(buf[m.dataOff:]))
	binary.LittleEndian.PutUint32(h[112:116], crc32.ChecksumIEEE(h[:112]))

	pm, err := Map(buf)
	if err != nil {
		t.Fatalf("polluted file rejected: %v", err)
	}
	checkAll(t, bits, pm)
}

// TestCorruptIndexAfterLoad bypasses the loader and damages the rank
// superblock index, the select samples, and directory offsets inside
// the mapping. Queries must never panic or return out-of-range
// positions; select may simply report not-found.
func TestCorruptIndexAfterLoad(t *testing.T) {
	bits, buf := sampleBitmap(t)
	m, err := Map(buf)
	if err != nil {
		t.Fatal(err)
	}
	// Damage superblock ranks, select samples, and some data offsets.
	for s := uint32(0); s < m.numSupers; s++ {
		binary.LittleEndian.PutUint64(buf[m.superOff+uint64(s)*superEntrySize:], ^uint64(0)-uint64(s))
	}
	for s := uint32(0); s < m.numSamples; s++ {
		binary.LittleEndian.PutUint32(buf[m.selOff+uint64(s)*sampleEntrySize:], 0xDEADBEEF)
	}
	for b := uint32(0); b < m.numBlocks; b += 3 {
		binary.LittleEndian.PutUint32(buf[m.dirOff+uint64(b)*dirEntrySize+4:], 0xFFFFFF00)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("query on corrupted index panicked: %v", r)
		}
	}()
	for i := 0; i <= len(bits); i += 13 {
		if r := m.Rank1(uint64(i), nil); r > uint64(len(bits)) {
			t.Fatalf("corrupted Rank1(%d)=%d out of range", i, r)
		}
	}
	for k := 0; k < bits.ones()+2; k++ {
		if pos, ok := m.Select1(uint64(k), nil); ok && pos >= uint64(len(bits)) {
			t.Fatalf("corrupted Select1(%d)=%d out of range", k, pos)
		}
	}
}

// TestTamperedIndexDetected: tampering with index sections (without
// repairing checksums) must be caught at load time.
func TestTamperedIndexDetected(t *testing.T) {
	_, buf := sampleBitmap(t)
	m, err := Map(buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []uint64{
		m.superOff, m.superOff + 8,
		m.selOff, m.selOff + 8,
		m.dirOff + 1, m.dirOff + 4,
	} {
		mut := make([]byte, len(buf))
		copy(mut, buf)
		mut[off] ^= 0x40
		if _, err := Map(mut); err == nil {
			t.Fatalf("tampered index at offset %d loaded without error", off)
		}
	}
}
