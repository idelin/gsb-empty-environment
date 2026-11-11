package h264bs

import (
	"bytes"
	"testing"
)

func TestBuildAndParseDecoderConfigHighProfile(t *testing.T) {
	sps := buildHighProfileSPS(highProfileSPSParams{
		chromaFormatIDC:           1,
		picWidthInMbsMinus1:       44,
		picHeightInMapUnitsMinus1: 35,
		frameMbsOnly:              true,
		frameCropping:             true,
		cropLeft:                  2,
		cropRight:                 2,
		cropTop:                   1,
		cropBottom:                1,
	})
	pps := buildPPS(0, 0)

	for _, lengthSize := range []int{1, 2, 4} {
		record, err := BuildDecoderConfig([][]byte{sps}, [][]byte{pps}, lengthSize)
		if err != nil {
			t.Fatalf("BuildDecoderConfig(%d): %v", lengthSize, err)
		}

		cfg, err := ParseDecoderConfig(record)
		if err != nil {
			t.Fatalf("ParseDecoderConfig(%d): %v", lengthSize, err)
		}
		if cfg.LengthSize != lengthSize {
			t.Errorf("LengthSize = %d, want %d", cfg.LengthSize, lengthSize)
		}
		if cfg.ProfileIDC != 100 {
			t.Errorf("ProfileIDC = %d, want 100", cfg.ProfileIDC)
		}
		if cfg.LevelIDC != 40 {
			t.Errorf("LevelIDC = %d, want 40", cfg.LevelIDC)
		}
		if !cfg.HighProfileExtended || cfg.ChromaFormatIDC != 1 {
			t.Errorf("High extension missing: %+v", cfg)
		}
		if cfg.BitDepthLumaMinus8 != 0 || cfg.BitDepthChromaMinus8 != 0 {
			t.Errorf("bit depths = %d/%d", cfg.BitDepthLumaMinus8, cfg.BitDepthChromaMinus8)
		}
		if len(cfg.SPS) != 1 || !bytes.Equal(cfg.SPS[0], sps) {
			t.Errorf("SPS round trip failed\n got %x\nwant %x", cfg.SPS, sps)
		}
		if len(cfg.PPS) != 1 || !bytes.Equal(cfg.PPS[0], pps) {
			t.Errorf("PPS round trip failed\n got %x\nwant %x", cfg.PPS, pps)
		}

		// Rebuilding from the parsed record must produce identical bytes.
		rebuilt, err := BuildDecoderConfig(cfg.SPS, cfg.PPS, cfg.LengthSize)
		if err != nil {
			t.Fatal(err)
		}
		assertBytes(t, rebuilt, record)
	}
}

func TestBuildDecoderConfigBaselineOmitsExtension(t *testing.T) {
	sps := buildBaselineSPS(0, 26, 14)
	record, err := BuildDecoderConfig([][]byte{sps}, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseDecoderConfig(record)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HighProfileExtended {
		t.Error("Baseline avcC must not include High-profile extension bytes")
	}
	if len(cfg.PPS) != 0 {
		t.Errorf("expected zero PPS, got %d", len(cfg.PPS))
	}
}

func TestBuildDecoderConfigValidation(t *testing.T) {
	sps := buildBaselineSPS(0, 0, 0)
	if _, err := BuildDecoderConfig([][]byte{sps}, nil, 3); err == nil {
		t.Error("length size 3 should be rejected")
	}
	if _, err := BuildDecoderConfig(nil, nil, 4); err == nil {
		t.Error("missing SPS should be rejected")
	}
}

func TestParseDecoderConfigErrors(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0, 0, 0, 0},
		{2, 66, 0, 30, 0xff, 0xe0}, // wrong configurationVersion
	} {
		if _, err := ParseDecoderConfig(data); err == nil {
			t.Errorf("expected error for %x", data)
		}
	}
}
