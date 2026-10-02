package main

import (
	"bytes"
	"testing"
)

func testStream(t *testing.T) ([]byte, *EncodedStream) {
	data := []byte(buildSampleText())
	codes := buildCodes(data)
	return data, EncodeFrames(data, codes)
}

func TestRoundTrip(t *testing.T) {
	data, enc := testStream(t)
	ft, events := DecodeFaultTolerant(enc.Data, enc.Codes)
	if !bytes.Equal(ft, data) {
		t.Fatalf("fault-tolerant round trip mismatch (%d vs %d bytes)", len(ft), len(data))
	}
	if len(events) != 0 {
		t.Fatalf("unexpected events on clean stream: %v", events)
	}
	if naive := DecodeNaive(enc.Data, enc.Codes); !bytes.Equal(naive, data) {
		t.Fatalf("naive round trip mismatch")
	}
}

func TestSyncWordPlacement(t *testing.T) {
	_, enc := testStream(t)
	if len(enc.Data)%FrameBytes != 0 {
		t.Fatalf("stream length %d is not a multiple of the %d-byte frame", len(enc.Data), FrameBytes)
	}
	for off := 0; off+len(SyncPattern) <= len(enc.Data); off += FrameBytes {
		if !bytes.Equal(enc.Data[off:off+len(SyncPattern)], SyncPattern) {
			t.Fatalf("no sync word at byte %d", off)
		}
	}
	if len(enc.Data) <= 104 || !bytes.Equal(enc.Data[100:104], SyncPattern) {
		t.Fatalf("expected a sync word at byte 100")
	}
}

func TestNaiveDecoderGarbledAfterDeletion(t *testing.T) {
	data, enc := testStream(t)
	corrupted := DeleteBits(enc.Data, 60*8, 3)
	naive := DecodeNaive(corrupted, enc.Codes)
	start := enc.FrameStartSym[1]
	if r := positionMatch(naive[start:], data[start:]); r > 0.2 {
		t.Fatalf("naive decoder unexpectedly recovered %.1f%% of post-corruption content", 100*r)
	}
	if bytes.Contains(naive[start:], data[enc.FrameStartSym[2]:]) {
		t.Fatalf("naive decoder unexpectedly recovered the post-resync tail")
	}
}

func TestFaultTolerantResyncAfterDeletion(t *testing.T) {
	data, enc := testStream(t)
	corrupted := DeleteBits(enc.Data, 60*8, 3) // erase 3 bits at byte 60
	ft, events := DecodeFaultTolerant(corrupted, enc.Codes)

	resync := -1
	for _, e := range events {
		if e.Kind == EventResync {
			resync = e.BitPos
		}
	}
	// The sync word written at byte 100 shifts to bit 800-3 = 797.
	if want := 100*8 - 3; resync != want {
		t.Fatalf("resync at bit %d, want bit %d (the sync word of byte 100)", resync, want)
	}

	tail := data[enc.FrameStartSym[2]:]
	if !bytes.HasSuffix(ft, tail) {
		t.Fatalf("content after the byte-100 sync word was not fully recovered")
	}
	if !bytes.HasPrefix(ft, data[:enc.FrameStartSym[1]]) {
		t.Fatalf("content before the corruption point was damaged")
	}

	salvage := float64(enc.SymbolsFromFrame(2)) / float64(enc.SymbolsFromFrame(1))
	t.Logf("salvaged %.1f%% of post-corruption content", 100*salvage)
	if salvage < 0.90 {
		t.Fatalf("salvage ratio %.1f%% below the 90%% acceptance bound", 100*salvage)
	}
}

func TestSingleBitFlipContained(t *testing.T) {
	data, enc := testStream(t)
	corrupted := append([]byte(nil), enc.Data...)
	corrupted[60] ^= 0x10 // flip one bit inside frame #1's payload

	ft, events := DecodeFaultTolerant(corrupted, enc.Codes)
	// A bit flip does not shift the phase of a prefix code: the damage
	// must stay confined to a handful of symbols around the flip.
	if len(ft) != len(data) {
		t.Fatalf("output length %d, want %d (phase lost)", len(ft), len(data))
	}
	mismatch := 0
	for i := range data {
		if ft[i] != data[i] {
			mismatch++
		}
	}
	t.Logf("bit flip corrupted %d of %d symbols", mismatch, len(data))
	if mismatch > 8 {
		t.Fatalf("bit flip damaged %d symbols; error diffusion not contained", mismatch)
	}
	if !bytes.HasPrefix(ft, data[:enc.FrameStartSym[1]]) {
		t.Fatalf("frames before the flip were damaged")
	}
	if !bytes.HasSuffix(ft, data[enc.FrameStartSym[2]:]) {
		t.Fatalf("frames after the flip were not recovered")
	}
	_ = events
}
