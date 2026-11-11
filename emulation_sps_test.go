package h264bs

import (
	"bytes"
	"testing"
)

// TestSPSWithEmulationPreventionBytes builds a High-profile SPS whose
// VUI carries two zero 32-bit timing words. Back-to-back zero bits form
// 00 00 00/00 00 01 byte runs in the raw RBSP, so escaping must insert
// prevention bytes. The parser removes them and recovers every field.
func TestSPSWithEmulationPreventionBytes(t *testing.T) {
	nal := buildHighProfileSPS(highProfileSPSParams{
		spsID:                     0,
		chromaFormatIDC:           1,
		picOrderCntType:           2,
		picWidthInMbsMinus1:       0, // 16 px
		picHeightInMapUnitsMinus1: 0, // 16 px
		frameMbsOnly:              true,
		timingPresent:             true,
		numUnitsInTick:            0, // zero timing words -> prevention bytes
		timeScale:                 0,
	})

	rbsp, err := UnescapeRBSP(nal[1:])
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	if len(rbsp) == len(nal)-1 {
		t.Fatalf("fixture did not trigger prevention bytes, nal=%x", nal)
	}
	if bytes.Index(nal[1:], []byte{0x00, 0x00, 0x01}) >= 0 {
		t.Fatalf("EBSP still contains a raw start-code sequence: %x", nal)
	}

	// Escape/unescape round trip preserves exact NAL bytes.
	assertBytes(t, append([]byte{nal[0]}, EscapeRBSP(rbsp)...), nal)

	sps, err := ParseSPS(nal)
	if err != nil {
		t.Fatalf("ParseSPS on escaped NAL: %v", err)
	}
	if sps.Width != 16 || sps.Height != 16 {
		t.Errorf("geometry = %dx%d, want 16x16", sps.Width, sps.Height)
	}
	if sps.ProfileIDC != 100 || sps.ChromaFormatIDC != 1 {
		t.Errorf("profile/chroma = %d/%d, want 100/1", sps.ProfileIDC, sps.ChromaFormatIDC)
	}
	if sps.VUI == nil || sps.VUI.NumUnitsInTick != 0 || sps.VUI.TimeScale != 0 {
		t.Errorf("VUI timing words not recovered: %+v", sps.VUI)
	}
}
