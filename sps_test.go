package h264bs

import (
	"math"
	"testing"
)

// TestHandBuiltHighProfileSPS builds a High-profile SPS with frame
// cropping and VUI timing by hand, parses it back and checks every
// field that the media gateway relies on.
func TestHandBuiltHighProfileSPS(t *testing.T) {
	// 720x576 frame (45 macroblocks wide, 36 map units high), 4:2:0,
	// cropped by (2,2,1,1) chroma-sample units to 712x572.
	params := highProfileSPSParams{
		spsID:                     0,
		chromaFormatIDC:           1,
		seqScalingMatrixPresent:   false,
		log2MaxFrameNumMinus4:     0,
		picOrderCntType:           0,
		maxNumRefFrames:           4,
		picWidthInMbsMinus1:       44,
		picHeightInMapUnitsMinus1: 35,
		frameMbsOnly:              true,
		frameCropping:             true,
		cropLeft:                  2,
		cropRight:                 2,
		cropTop:                   1,
		cropBottom:                1,
		timingPresent:             true,
		numUnitsInTick:            1001,
		timeScale:                 60000, // 60000 / (2*1001) ~= 29.97 fps
		fixedFrameRate:            true,
	}
	nal := buildHighProfileSPS(params)

	sps, err := ParseSPS(nal)
	if err != nil {
		t.Fatalf("ParseSPS: %v", err)
	}

	if sps.ProfileIDC != 100 {
		t.Errorf("ProfileIDC = %d, want 100", sps.ProfileIDC)
	}
	if sps.LevelIDC != 40 {
		t.Errorf("LevelIDC = %d, want 40", sps.LevelIDC)
	}
	if sps.SPSID != 0 {
		t.Errorf("SPSID = %d, want 0", sps.SPSID)
	}
	if sps.ChromaFormatIDC != 1 {
		t.Errorf("ChromaFormatIDC = %d, want 1 (4:2:0)", sps.ChromaFormatIDC)
	}
	if sps.BitDepthLumaMinus8 != 0 || sps.BitDepthChromaMinus8 != 0 {
		t.Errorf("bit depths = %d/%d, want 0/0",
			sps.BitDepthLumaMinus8, sps.BitDepthChromaMinus8)
	}
	if sps.SeqScalingMatrixPresentFlag {
		t.Error("SeqScalingMatrixPresentFlag should be false")
	}
	if sps.MaxNumRefFrames != 4 {
		t.Errorf("MaxNumRefFrames = %d, want 4", sps.MaxNumRefFrames)
	}
	if !sps.FrameMbsOnlyFlag {
		t.Error("FrameMbsOnlyFlag should be true")
	}
	if !sps.Direct8x8InferenceFlag {
		t.Error("Direct8x8InferenceFlag should be true")
	}

	if !sps.FrameCroppingFlag {
		t.Fatal("FrameCroppingFlag should be true")
	}
	if sps.FrameCropLeftOffset != 2 || sps.FrameCropRightOffset != 2 ||
		sps.FrameCropTopOffset != 1 || sps.FrameCropBottomOffset != 1 {
		t.Errorf("crop offsets = (%d,%d,%d,%d), want (2,2,1,1)",
			sps.FrameCropLeftOffset, sps.FrameCropRightOffset,
			sps.FrameCropTopOffset, sps.FrameCropBottomOffset)
	}

	// Uncropped macroblock geometry: 45*16 = 720 wide, 36*16 = 576 high.
	// 4:2:0 crop units: x unit = 2 luma samples, y unit = 2 luma samples.
	if sps.Width != 720-(2+2)*2 {
		t.Errorf("Width = %d, want %d", sps.Width, 720-(2+2)*2)
	}
	if sps.Height != 576-(1+1)*2 {
		t.Errorf("Height = %d, want %d", sps.Height, 576-(1+1)*2)
	}

	if sps.VUI == nil || !sps.VUI.TimingInfoPresent {
		t.Fatal("VUI timing info missing")
	}
	if sps.VUI.NumUnitsInTick != 1001 || sps.VUI.TimeScale != 60000 {
		t.Errorf("timing = %d/%d", sps.VUI.NumUnitsInTick, sps.VUI.TimeScale)
	}
	if !sps.VUI.FixedFrameRate {
		t.Error("FixedFrameRate should be true")
	}
	wantFPS := 60000.0 / (2 * 1001)
	if math.Abs(sps.FrameRate-wantFPS) > 1e-9 {
		t.Errorf("FrameRate = %v, want %v", sps.FrameRate, wantFPS)
	}

	// Re-serializing the parsed structure is impossible by design, but
	// escape/unescape must preserve the exact NAL payload bytes.
	rbsp, err := UnescapeRBSP(nal[1:])
	if err != nil {
		t.Fatal(err)
	}
	reEscaped := append([]byte{nal[0]}, EscapeRBSP(rbsp)...)
	assertBytes(t, reEscaped, nal)
}

func TestHighProfileSPSWithScalingMatrices(t *testing.T) {
	params := highProfileSPSParams{
		spsID:                     3,
		chromaFormatIDC:           1,
		seqScalingMatrixPresent:   true,
		log2MaxFrameNumMinus4:     2,
		picOrderCntType:           2,
		maxNumRefFrames:           1,
		picWidthInMbsMinus1:       19, // 320 px
		picHeightInMapUnitsMinus1: 11, // 192 px
		frameMbsOnly:              true,
		frameCropping:             false,
		timingPresent:             false,
	}
	sps, err := ParseSPS(buildHighProfileSPS(params))
	if err != nil {
		t.Fatalf("ParseSPS: %v", err)
	}
	if !sps.SeqScalingMatrixPresentFlag {
		t.Error("SeqScalingMatrixPresentFlag should be true")
	}
	if sps.PicOrderCntType != 2 {
		t.Errorf("PicOrderCntType = %d, want 2", sps.PicOrderCntType)
	}
	if sps.SPSID != 3 {
		t.Errorf("SPSID = %d, want 3", sps.SPSID)
	}
	if sps.Width != 320 || sps.Height != 192 {
		t.Errorf("geometry = %dx%d, want 320x192", sps.Width, sps.Height)
	}
	if sps.FrameRate != 0 {
		t.Errorf("FrameRate = %v, want 0 without timing info", sps.FrameRate)
	}
}

// TestHighProfileSPSFullOptionPaths exercises the optional High-profile
// and VUI branches that a minimal SPS does not reach: 4:4:4 chroma with
// separate colour planes, populated scaling lists, POC type 1, HRD
// parameters, picture structure info and bitstream restrictions.
func TestHighProfileSPSFullOptionPaths(t *testing.T) {
	params := highProfileSPSParams{
		spsID:                   7,
		chromaFormatIDC:         3, // 4:4:4
		seqScalingMatrixPresent: true,
		scalingListPresent: []bool{
			true, false, true, false, true, false, // 16-entry lists
			true, false, true, false, true, false, // 64-entry lists
		},
		picOrderCntType:           1,
		maxNumRefFrames:           2,
		picWidthInMbsMinus1:       9, // 160 px
		picHeightInMapUnitsMinus1: 8, // 144 px
		frameMbsOnly:              true,
		timingPresent:             true,
		numUnitsInTick:            1,
		timeScale:                 50, // 25 fps
		fixedFrameRate:            true,
		nalHRDPresent:             true,
		picStructPresent:          true,
		bitstreamRestriction:      true,
	}
	sps, err := ParseSPS(buildHighProfileSPS(params))
	if err != nil {
		t.Fatalf("ParseSPS: %v", err)
	}
	if sps.ChromaFormatIDC != 3 {
		t.Errorf("ChromaFormatIDC = %d, want 3", sps.ChromaFormatIDC)
	}
	if sps.SeparateColourPlaneFlag {
		t.Error("fixture writes separate_colour_plane_flag=0")
	}
	if !sps.SeqScalingMatrixPresentFlag {
		t.Error("SeqScalingMatrixPresentFlag should be true")
	}
	if sps.PicOrderCntType != 1 {
		t.Errorf("PicOrderCntType = %d, want 1", sps.PicOrderCntType)
	}
	if sps.Width != 160 || sps.Height != 144 {
		t.Errorf("geometry = %dx%d, want 160x144", sps.Width, sps.Height)
	}
	if sps.FrameRate != 25 {
		t.Errorf("FrameRate = %v, want 25", sps.FrameRate)
	}
	if sps.VUI == nil || sps.VUI.NALHRDParameters == nil {
		t.Fatal("NAL HRD parameters missing")
	}
	if sps.VUI.NALHRDParameters.CPBCntMinus1 != 0 {
		t.Errorf("CPBCntMinus1 = %d, want 0", sps.VUI.NALHRDParameters.CPBCntMinus1)
	}
	if !sps.VUI.PicStructPresent || !sps.VUI.BitstreamRestriction {
		t.Errorf("VUI flags missing: %+v", sps.VUI)
	}
}

// TestVUIOptionalFields covers aspect ratio, video signal type,
// colour description and chroma location parsing inside VUI.
func TestVUIOptionalFields(t *testing.T) {
	sps, err := ParseSPS(buildHighProfileSPS(highProfileSPSParams{
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       9,
		picHeightInMapUnitsMinus1: 8,
		frameMbsOnly:              true,
		timingPresent:             true,
		fullVUI:                   true,
		numUnitsInTick:            1,
		timeScale:                 60, // 30 fps
	}))
	if err != nil {
		t.Fatal(err)
	}
	if sps.VUI == nil {
		t.Fatal("VUI missing")
	}
	if !sps.VUI.AspectRatioInfoPresent || sps.VUI.AspectRatioIDC != 255 ||
		sps.VUI.SARWidth != 1 || sps.VUI.SARHeight != 1 {
		t.Errorf("aspect ratio fields wrong: %+v", sps.VUI)
	}
	if !sps.VUI.VideoSignalTypePresent || sps.VUI.VideoFormat != 1 ||
		!sps.VUI.VideoFullRange {
		t.Errorf("video signal fields wrong: %+v", sps.VUI)
	}
	if !sps.VUI.ColourDescription || sps.VUI.ColourPrimaries != 1 ||
		sps.VUI.TransferCharacteristics != 1 || sps.VUI.MatrixCoefficients != 1 {
		t.Errorf("colour description wrong: %+v", sps.VUI)
	}
	if !sps.VUI.ChromaLocInfoPresent ||
		sps.VUI.ChromaSampleLocTop != 0 || sps.VUI.ChromaSampleLocBottom != 1 {
		t.Errorf("chroma location wrong: %+v", sps.VUI)
	}
	if sps.FrameRate != 30 {
		t.Errorf("FrameRate = %v, want 30", sps.FrameRate)
	}
}

// TestSeparateColourPlaneFlag covers the separate_colour_plane_flag
// branch unique to 4:4:4 High-profile streams.
func TestSeparateColourPlaneFlag(t *testing.T) {
	sps, err := ParseSPS(buildHighProfileSPS(highProfileSPSParams{
		chromaFormatIDC:           3,
		separateColourPlane:       true,
		picWidthInMbsMinus1:       0,
		picHeightInMapUnitsMinus1: 0,
		frameMbsOnly:              true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !sps.SeparateColourPlaneFlag {
		t.Error("SeparateColourPlaneFlag should be true")
	}
	if sps.Width != 16 || sps.Height != 16 {
		t.Errorf("geometry = %dx%d, want 16x16", sps.Width, sps.Height)
	}
}

// TestChromaCropUnits verifies crop offsets use the chroma sample unit
// size of each chroma format (H.264 7.4.2.1.1).
func TestChromaCropUnits(t *testing.T) {
	for _, tc := range []struct {
		name            string
		chromaFormatIDC uint
		cropLR, cropTB  uint
		wantWidth       int
		wantHeight      int
	}{
		{"420", 1, 2, 2, 320 - 4*2, 240 - 4*2},
		{"422", 2, 2, 2, 320 - 4*2, 240 - 4*1},
		{"444", 3, 2, 2, 320 - 4*1, 240 - 4*1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sps, err := ParseSPS(buildHighProfileSPS(highProfileSPSParams{
				chromaFormatIDC:           tc.chromaFormatIDC,
				picWidthInMbsMinus1:       19, // 320
				picHeightInMapUnitsMinus1: 14, // 240
				frameMbsOnly:              true,
				frameCropping:             true,
				cropLeft:                  tc.cropLR,
				cropRight:                 tc.cropLR,
				cropTop:                   tc.cropTB,
				cropBottom:                tc.cropTB,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if sps.Width != tc.wantWidth || sps.Height != tc.wantHeight {
				t.Errorf("%s geometry = %dx%d, want %dx%d",
					tc.name, sps.Width, sps.Height, tc.wantWidth, tc.wantHeight)
			}
		})
	}
}

func TestBaselineSPSNoHighExtension(t *testing.T) {
	nal := buildBaselineSPS(0, 26, 14) // 432x240
	sps, err := ParseSPS(nal)
	if err != nil {
		t.Fatalf("ParseSPS: %v", err)
	}
	if sps.ProfileIDC != 66 {
		t.Errorf("ProfileIDC = %d, want 66", sps.ProfileIDC)
	}
	if highProfileIDC(sps.ProfileIDC) {
		t.Error("Baseline profile must not carry High extension")
	}
	if sps.Width != 432 || sps.Height != 240 {
		t.Errorf("geometry = %dx%d, want 432x240", sps.Width, sps.Height)
	}
	if sps.FrameCroppingFlag {
		t.Error("unexpected cropping flag")
	}
}

func TestParseSPSRejectsWrongNALType(t *testing.T) {
	if _, err := ParseSPS([]byte{0x68}); err == nil {
		t.Error("PPS NAL should be rejected by ParseSPS")
	}
	if _, err := ParseSPS(nil); err == nil {
		t.Error("empty NAL should be rejected")
	}
}

func TestParsePPSID(t *testing.T) {
	nal := buildPPS(2, 0)
	id, err := ParsePPSID(nal)
	if err != nil {
		t.Fatalf("ParsePPSID: %v", err)
	}
	if id != 2 {
		t.Errorf("PPS ID = %d, want 2", id)
	}
	if _, err := ParsePPSID([]byte{0x67}); err == nil {
		t.Error("SPS NAL should be rejected by ParsePPSID")
	}
}

func assertBytes(t *testing.T, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("byte length mismatch: got %d, want %d\n got %x\nwant %x",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d mismatch: got %#x, want %#x\n full got %x\nfull want %x",
				i, got[i], want[i], got, want)
		}
	}
}
