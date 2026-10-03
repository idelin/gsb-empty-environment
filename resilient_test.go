package huffsync

import (
	"bytes"
	"testing"
)

// sampleMessage builds a deterministic ~4000-byte text payload.
func sampleMessage() []byte {
	base := "The quick brown fox jumps over the lazy dog. " +
		"Pack my box with five dozen liquor jugs. " +
		"How vexingly quick daft zebras jump! " +
		"Bright vixens jump; dozy fowl quack. "
	var buf []byte
	for len(buf) < 4000 {
		buf = append(buf, base...)
	}
	return buf[:4000]
}

func buildCode(msg []byte) *HuffCode {
	var freq [NumSymbols]uint64
	for _, s := range msg {
		freq[s]++
	}
	freq[SymInvalid] = 1 // reserved terminator: rarest symbol, longest code
	return BuildHuffman(freq)
}

func commonPrefix(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// salvageRatio measures how many source symbols were recovered correctly,
// per segment, against the encoder's symbol bookkeeping.
func salvageRatio(t *testing.T, msg []byte, enc *EncodedStream, res *DecodeResult) float64 {
	t.Helper()
	if len(res.Segments) != len(enc.SymbolsPerSegment) {
		t.Fatalf("segment count mismatch: decoded %d, encoded %d (sync word lost or false sync)",
			len(res.Segments), len(enc.SymbolsPerSegment))
	}
	correct, total := 0, 0
	off := 0
	for i, cnt := range enc.SymbolsPerSegment {
		want := msg[off : off+cnt]
		got := res.Segments[i].Symbols
		if i == 0 {
			correct += commonPrefix(got, want) // corrupted segment: only the prefix counts
		} else if bytes.Equal(got, want) {
			correct += cnt
		}
		off += cnt
		total += cnt
	}
	return float64(correct) / float64(total)
}

func TestRoundTripNoCorruption(t *testing.T) {
	msg := sampleMessage()
	code := buildCode(msg)

	if out := DecodePlain(EncodePlain(msg, code), code); !bytes.Equal(out, msg) {
		t.Fatalf("plain round trip mismatch")
	}
	enc := EncodeResilient(msg, code)
	res := DecodeResilient(enc.Bits, code)
	if !bytes.Equal(res.Output, msg) {
		t.Fatalf("resilient round trip mismatch")
	}
	for _, seg := range res.Segments {
		if !seg.CleanEnd {
			t.Fatalf("segment %d did not end at its terminator", seg.Index)
		}
	}
}

func TestSyncWordsLandOnHundredByteGrid(t *testing.T) {
	msg := sampleMessage()
	code := buildCode(msg)
	enc := EncodeResilient(msg, code)

	if enc.Bits.Len() != len(enc.SymbolsPerSegment)*FrameBytes*8 {
		t.Fatalf("stream size %d bits is not a whole number of %d-byte frames",
			enc.Bits.Len(), FrameBytes)
	}
	for i, p := range FindSync(enc.Bits) {
		if p%8 != 0 || p/8 != i*FrameBytes {
			t.Fatalf("sync word %d at bit %d, want byte %d", i, p, i*FrameBytes)
		}
	}
}

// Acceptance test: erase 3 bits in the middle of the stream. An ordinary
// decoder garbles everything after the erasure; the resilient decoder
// re-locks at the sync word located at byte 100 and salvages >= 90%.
func TestResyncAfterThreeBitErasure(t *testing.T) {
	msg := sampleMessage()
	code := buildCode(msg)

	const eraseBit = 50 * 8 // byte 50: inside segment 0 (bytes 4..99)

	// --- Baseline: ordinary decoder, no sync words ---
	plainDamaged := DeleteBits(EncodePlain(msg, code), eraseBit, 3)
	plainOut := DecodePlain(plainDamaged, code)
	prefix := commonPrefix(plainOut, msg)
	// The erasure sits at bit 400 (~symbol 86): the ordinary decoder is
	// correct only up to that point, then loses phase for good.
	if prefix < 50 || prefix > 150 {
		t.Fatalf("ordinary decoder prefix %d: expected to break right at the erasure", prefix)
	}
	tailMatches := 0
	for i := prefix; i < len(plainOut) && i < len(msg); i++ {
		if plainOut[i] == msg[i] {
			tailMatches++
		}
	}
	tailRatio := float64(tailMatches) / float64(len(msg)-prefix)
	t.Logf("ordinary decoder: %d correct symbols then garbage (tail match %.1f%%)",
		prefix, tailRatio*100)
	if tailRatio > 0.20 {
		t.Fatalf("ordinary decoder tail match %.1f%%, expected near-total garbage", tailRatio*100)
	}

	// --- Resilient decoder ---
	enc := EncodeResilient(msg, code)
	damaged := DeleteBits(enc.Bits, eraseBit, 3)
	if damaged.Len() != enc.Bits.Len()-3 {
		t.Fatalf("erasure removed wrong number of bits")
	}
	res := DecodeResilient(damaged, code)

	// Segment 0 absorbed the damage: its decoded symbols must diverge from
	// the source somewhere after the erasure point.
	want0 := msg[:enc.SymbolsPerSegment[0]]
	if p := commonPrefix(res.Segments[0].Symbols, want0); p >= len(want0) {
		t.Fatalf("corrupted segment 0 decoded fully correctly (%d symbols)", p)
	}
	// Every later segment must decode perfectly.
	for i := 1; i < len(res.Segments); i++ {
		if !res.Segments[i].CleanEnd {
			t.Fatalf("segment %d after the sync word failed to decode cleanly", i)
		}
	}

	// The sync word that restores phase sits at byte 100 of the original
	// stream; the 3-bit erasure ahead of it shifts it to bit 797.
	const wantSyncBit = 100*8 - 3
	if got := res.Segments[1].SyncBitPos; got != wantSyncBit {
		t.Fatalf("re-synced at bit %d, want the sync word from byte 100 (bit %d)", got, wantSyncBit)
	}
	t.Logf("re-locked at sync word originally at byte 100 (bit %d of damaged stream)", wantSyncBit)

	ratio := salvageRatio(t, msg, enc, res)
	t.Logf("salvaged %.2f%% of %d symbols", ratio*100, len(msg))
	if ratio < 0.90 {
		t.Fatalf("salvage ratio %.2f%% below the 90%% acceptance bound", ratio*100)
	}
}

// A single-bit flip must be isolated to its segment just like an erasure.
func TestResyncAfterSingleBitFlip(t *testing.T) {
	msg := sampleMessage()
	code := buildCode(msg)
	enc := EncodeResilient(msg, code)

	damaged := FlipBit(enc.Bits, 50*8)
	res := DecodeResilient(damaged, code)

	if got := res.Segments[1].SyncBitPos; got != 100*8 {
		t.Fatalf("re-synced at bit %d, want the sync word at byte 100 (bit %d)", got, 100*8)
	}
	ratio := salvageRatio(t, msg, enc, res)
	t.Logf("bit flip: salvaged %.2f%%", ratio*100)
	if ratio < 0.90 {
		t.Fatalf("salvage ratio %.2f%% below the 90%% acceptance bound", ratio*100)
	}
}
