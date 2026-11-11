package h264bs

import (
	"bytes"
	"testing"
)

func TestConverterUpdatesConfigOnSPSPPSChange(t *testing.T) {
	conv, err := NewConverter(4)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := conv.Config(); cfg != nil {
		t.Fatalf("initial config should be nil, got %x", cfg)
	}

	sps1 := buildHighProfileSPS(highProfileSPSParams{
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       44,
		picHeightInMapUnitsMinus1: 35,
		frameMbsOnly:              true,
		frameCropping:             true,
		cropLeft:                  2, cropRight: 2, cropTop: 1, cropBottom: 1,
		timingPresent: true, numUnitsInTick: 1001, timeScale: 60000,
		fixedFrameRate: true,
	})
	pps1 := buildPPS(0, 0)
	idr := []byte{0x65, 0x88, 0x80, 0x40}

	// First Annex B frame carries SPS+PPS+IDR: config is created.
	stream1 := PackAnnexB([][]byte{sps1, pps1, idr})
	avcc1, changed, err := conv.ConvertAnnexB(stream1)
	if err != nil {
		t.Fatalf("ConvertAnnexB: %v", err)
	}
	if !changed {
		t.Error("first SPS/PPS should report a config change")
	}
	cfg1 := conv.Config()
	if cfg1 == nil {
		t.Fatal("avcC should be available after first SPS")
	}
	parsed1, err := ParseDecoderConfig(cfg1)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed1.SPS) != 1 || len(parsed1.PPS) != 1 {
		t.Fatalf("unexpected parameter set counts: %+v", parsed1)
	}

	// Identical frame: no change, identical avcC bytes.
	avcc1Again, changed, err := conv.ConvertAnnexB(stream1)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("identical parameter sets must not report a change")
	}
	if !bytes.Equal(conv.Config(), cfg1) {
		t.Error("avcC bytes changed without a parameter set change")
	}
	if !bytes.Equal(avcc1Again, avcc1) {
		t.Error("AVCC output should be deterministic")
	}

	// New SPS (different geometry) + new PPS: config rebuilt with both
	// parameter sets retained under their ids.
	sps2 := buildHighProfileSPS(highProfileSPSParams{
		spsID:                     1,
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       19,
		picHeightInMapUnitsMinus1: 11,
		frameMbsOnly:              true,
	})
	pps2 := buildPPS(1, 1)
	stream2 := PackAnnexB([][]byte{sps2, pps2, idr})
	if _, changed, err = conv.ConvertAnnexB(stream2); err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("new SPS/PPS should report a config change")
	}
	parsed2, err := ParseDecoderConfig(conv.Config())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed2.SPS) != 2 || len(parsed2.PPS) != 2 {
		t.Fatalf("expected 2 SPS / 2 PPS, got %d / %d",
			len(parsed2.SPS), len(parsed2.PPS))
	}

	// Update sps1 in place (same id 0): config changes again and still
	// contains exactly two SPS entries.
	sps1Updated := buildHighProfileSPS(highProfileSPSParams{
		spsID:                     0,
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       44,
		picHeightInMapUnitsMinus1: 35,
		frameMbsOnly:              true, // cropping removed
		timingPresent:             true,
		numUnitsInTick:            1,
		timeScale:                 50, // 25 fps
		fixedFrameRate:            true,
	})
	if _, changed, err = conv.ConvertAnnexB(PackAnnexB([][]byte{sps1Updated})); err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("modified SPS with same id should report a change")
	}
	parsed3, err := ParseDecoderConfig(conv.Config())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed3.SPS) != 2 {
		t.Fatalf("expected 2 SPS after in-place update, got %d", len(parsed3.SPS))
	}
	if !bytes.Equal(parsed3.SPS[0], sps1Updated) {
		t.Error("SPS id 0 should hold the updated bytes")
	}

	// Parse the updated SPS through the avcC and verify resolution/fps.
	parsedSPS, err := ParseSPS(parsed3.SPS[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsedSPS.Width != 720 || parsedSPS.Height != 576 {
		t.Errorf("uncropped geometry = %dx%d, want 720x576",
			parsedSPS.Width, parsedSPS.Height)
	}
	if parsedSPS.FrameRate != 25 {
		t.Errorf("FrameRate = %v, want 25", parsedSPS.FrameRate)
	}
}

func TestConverterFullRoundTripAllLengthSizes(t *testing.T) {
	sps := buildHighProfileSPS(highProfileSPSParams{
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       44,
		picHeightInMapUnitsMinus1: 35,
		frameMbsOnly:              true,
		frameCropping:             true,
		cropLeft:                  2, cropRight: 2, cropTop: 1, cropBottom: 1,
		timingPresent: true, numUnitsInTick: 1001, timeScale: 60000,
		fixedFrameRate: true,
	})
	pps := buildPPS(0, 0)
	// IDR payload deliberately contains escaped start-code-like byte
	// sequences (valid EBSP) to prove payload bytes survive untouched.
	idr := []byte{0x65, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x03, 0x01, 0xAA}
	nals := [][]byte{sps, pps, idr}
	original := PackAnnexB(nals)

	for _, lengthSize := range []int{1, 2, 4} {
		conv, err := NewConverter(lengthSize)
		if err != nil {
			t.Fatal(err)
		}

		avcc, changed, err := conv.ConvertAnnexB(original)
		if err != nil {
			t.Fatalf("lengthSize %d: %v", lengthSize, err)
		}
		if !changed || conv.Config() == nil {
			t.Fatalf("lengthSize %d: config not initialized", lengthSize)
		}

		back, changed, err := conv.ConvertAVCC(avcc)
		if err != nil {
			t.Fatalf("lengthSize %d: %v", lengthSize, err)
		}
		if changed {
			t.Fatalf("lengthSize %d: round trip reported spurious change", lengthSize)
		}

		// Canonical Annex B output (4-byte start codes) is byte-identical
		// to the canonical input.
		assertBytes(t, back, original)

		// Every NAL payload survives both directions unchanged.
		splitBack := SplitAnnexB(back)
		if len(splitBack) != len(nals) {
			t.Fatalf("lengthSize %d: got %d NALs, want %d",
				lengthSize, len(splitBack), len(nals))
		}
		for i := range nals {
			assertBytes(t, splitBack[i], nals[i])
		}
	}
}

func TestConverterRejectsBadLengthSize(t *testing.T) {
	if _, err := NewConverter(3); err == nil {
		t.Error("length size 3 should be rejected")
	}
}

func TestConverterLengthSizeAccessor(t *testing.T) {
	for _, size := range []int{1, 2, 4} {
		conv, err := NewConverter(size)
		if err != nil {
			t.Fatal(err)
		}
		if conv.LengthSize() != size {
			t.Errorf("LengthSize = %d, want %d", conv.LengthSize(), size)
		}
	}
}

func TestAVCCPrefixCapacity(t *testing.T) {
	// A 65536-byte NAL fits a 4-byte prefix but not a 2-byte prefix.
	nal := make([][]byte, 1)
	nal[0] = make([]byte, 65536)
	nal[0][0] = 0x65

	out, err := PackAVCC(nal, 4)
	if err != nil {
		t.Fatalf("4-byte prefix should fit: %v", err)
	}
	split, err := SplitAVCC(out, 4)
	if err != nil || len(split) != 1 || len(split[0]) != 65536 {
		t.Fatalf("4-byte prefix round trip failed: %v", err)
	}
	if _, err := PackAVCC(nal, 2); err == nil {
		t.Error("2-byte prefix should reject 65536-byte NAL")
	}
}

func TestSplitAnnexBAdjacentFourByteStartCodes(t *testing.T) {
	// Two 4-byte start codes back to back: the shared zero must be
	// attributed to the second start code, not NAL padding.
	stream := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xBB,
	}
	got := SplitAnnexB(stream)
	if len(got) != 2 {
		t.Fatalf("got %d NALs, want 2: %x", len(got), got)
	}
	assertBytes(t, got[0], []byte{0x67, 0xAA})
	assertBytes(t, got[1], []byte{0x68, 0xBB})
}
