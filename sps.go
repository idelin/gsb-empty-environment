package h264bs

import "fmt"

// HRDParameters holds the subset of Hypothetical Reference Decoder
// fields that appear inside VUI.
type HRDParameters struct {
	CPBCntMinus1                 uint
	BitRateScale                 uint
	CPBSizeScale                 uint
	InitialCPBRemovalDelayLength uint
	CPBRemovalDelayLength        uint
	DPBOutputDelayLength         uint
	TimeOffsetLength             uint
}

// VUI holds the Video Usability Information parsed from an SPS.
type VUI struct {
	AspectRatioInfoPresent bool
	AspectRatioIDC         uint
	SARWidth               uint
	SARHeight              uint

	VideoSignalTypePresent  bool
	VideoFormat             uint
	VideoFullRange          bool
	ColourDescription       bool
	ColourPrimaries         uint
	TransferCharacteristics uint
	MatrixCoefficients      uint

	ChromaLocInfoPresent  bool
	ChromaSampleLocTop    uint
	ChromaSampleLocBottom uint

	TimingInfoPresent    bool
	NumUnitsInTick       uint32
	TimeScale            uint32
	FixedFrameRate       bool
	NALHRDParameters     *HRDParameters
	VCLHRDParameters     *HRDParameters
	LowDelayHRD          bool
	PicStructPresent     bool
	BitstreamRestriction bool
}

// SPS is a parsed sequence parameter set with derived geometry.
type SPS struct {
	ProfileIDC                  uint8
	ConstraintSet0Flag          bool
	ConstraintSet1Flag          bool
	ConstraintSet2Flag          bool
	ConstraintSet3Flag          bool
	ConstraintSet4Flag          bool
	ConstraintSet5Flag          bool
	LevelIDC                    uint8
	SPSID                       uint
	ChromaFormatIDC             uint
	SeparateColourPlaneFlag     bool
	BitDepthLumaMinus8          uint
	BitDepthChromaMinus8        uint
	QPPrimeYZeroTransformBypass bool
	SeqScalingMatrixPresentFlag bool

	Log2MaxFrameNumMinus4       uint
	PicOrderCntType             uint
	Log2MaxPicOrderCntLSBMinus4 uint
	MaxNumRefFrames             uint
	GapsInFrameNumValueAllowed  bool
	PicWidthInMbsMinus1         uint
	PicHeightInMapUnitsMinus1   uint
	FrameMbsOnlyFlag            bool
	MBAdaptiveFrameFieldFlag    bool
	Direct8x8InferenceFlag      bool

	FrameCroppingFlag     bool
	FrameCropLeftOffset   uint
	FrameCropRightOffset  uint
	FrameCropTopOffset    uint
	FrameCropBottomOffset uint

	VUI *VUI

	Width     int
	Height    int
	FrameRate float64
}

// ParseSPS parses a SPS NAL unit (EBSP, including the NAL header byte).
func ParseSPS(nal []byte) (*SPS, error) {
	nalType, err := NALType(nal)
	if err != nil {
		return nil, err
	}
	if nalType != NALTypeSPS {
		return nil, fmt.Errorf("h264bs: expected SPS NAL (type 7), got type %d", nalType)
	}
	rbsp, err := UnescapeRBSP(nal[1:])
	if err != nil {
		return nil, fmt.Errorf("h264bs: SPS unescape: %w", err)
	}
	br := newBitReader(rbsp)

	sps := &SPS{}
	profileIDC, err := br.readBits(8)
	if err != nil {
		return nil, err
	}
	sps.ProfileIDC = uint8(profileIDC)

	constraintFlags, err := br.readBits(8)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 6; i++ {
		set := constraintFlags&(1<<(7-uint(i))) != 0
		switch i {
		case 0:
			sps.ConstraintSet0Flag = set
		case 1:
			sps.ConstraintSet1Flag = set
		case 2:
			sps.ConstraintSet2Flag = set
		case 3:
			sps.ConstraintSet3Flag = set
		case 4:
			sps.ConstraintSet4Flag = set
		case 5:
			sps.ConstraintSet5Flag = set
		}
	}

	levelIDC, err := br.readBits(8)
	if err != nil {
		return nil, err
	}
	sps.LevelIDC = uint8(levelIDC)

	if sps.SPSID, err = br.readUE(); err != nil {
		return nil, err
	}

	if highProfileIDC(sps.ProfileIDC) {
		if err := parseHighProfileExtension(br, sps); err != nil {
			return nil, err
		}
	}

	if sps.Log2MaxFrameNumMinus4, err = br.readUE(); err != nil {
		return nil, err
	}
	if sps.PicOrderCntType, err = br.readUE(); err != nil {
		return nil, err
	}
	switch sps.PicOrderCntType {
	case 0:
		if sps.Log2MaxPicOrderCntLSBMinus4, err = br.readUE(); err != nil {
			return nil, err
		}
	case 1:
		if _, err = br.readBit(); err != nil { // delta_pic_order_always_zero_flag
			return nil, err
		}
		if _, err = br.readSE(); err != nil { // offset_for_non_ref_pic
			return nil, err
		}
		if _, err = br.readSE(); err != nil { // offset_for_top_to_bottom_field
			return nil, err
		}
		numRefFramesInPOCCycle, err := br.readUE()
		if err != nil {
			return nil, err
		}
		for i := uint(0); i < numRefFramesInPOCCycle; i++ {
			if _, err = br.readSE(); err != nil {
				return nil, err
			}
		}
	}

	if sps.MaxNumRefFrames, err = br.readUE(); err != nil {
		return nil, err
	}
	gaps, err := br.readBit()
	if err != nil {
		return nil, err
	}
	sps.GapsInFrameNumValueAllowed = gaps == 1

	if sps.PicWidthInMbsMinus1, err = br.readUE(); err != nil {
		return nil, err
	}
	if sps.PicHeightInMapUnitsMinus1, err = br.readUE(); err != nil {
		return nil, err
	}
	frameMbsOnly, err := br.readBit()
	if err != nil {
		return nil, err
	}
	sps.FrameMbsOnlyFlag = frameMbsOnly == 1
	if !sps.FrameMbsOnlyFlag {
		mbAff, err := br.readBit()
		if err != nil {
			return nil, err
		}
		sps.MBAdaptiveFrameFieldFlag = mbAff == 1
	}
	direct8x8, err := br.readBit()
	if err != nil {
		return nil, err
	}
	sps.Direct8x8InferenceFlag = direct8x8 == 1

	cropping, err := br.readBit()
	if err != nil {
		return nil, err
	}
	sps.FrameCroppingFlag = cropping == 1
	if sps.FrameCroppingFlag {
		if sps.FrameCropLeftOffset, err = br.readUE(); err != nil {
			return nil, err
		}
		if sps.FrameCropRightOffset, err = br.readUE(); err != nil {
			return nil, err
		}
		if sps.FrameCropTopOffset, err = br.readUE(); err != nil {
			return nil, err
		}
		if sps.FrameCropBottomOffset, err = br.readUE(); err != nil {
			return nil, err
		}
	}

	vuiPresent, err := br.readBit()
	if err != nil {
		return nil, err
	}
	if vuiPresent == 1 {
		if sps.VUI, err = parseVUI(br); err != nil {
			return nil, err
		}
	}

	if br.moreRBSPData() {
		if err := br.readTrailingBits(); err != nil {
			return nil, err
		}
	}

	sps.deriveGeometry()
	sps.deriveFrameRate()
	return sps, nil
}

func parseHighProfileExtension(br *bitReader, sps *SPS) error {
	var err error
	if sps.ChromaFormatIDC, err = br.readUE(); err != nil {
		return err
	}
	if sps.ChromaFormatIDC == 3 {
		separate, err := br.readBit()
		if err != nil {
			return err
		}
		sps.SeparateColourPlaneFlag = separate == 1
	}
	if sps.BitDepthLumaMinus8, err = br.readUE(); err != nil {
		return err
	}
	if sps.BitDepthChromaMinus8, err = br.readUE(); err != nil {
		return err
	}
	qpprime, err := br.readBit()
	if err != nil {
		return err
	}
	sps.QPPrimeYZeroTransformBypass = qpprime == 1

	scalingPresent, err := br.readBit()
	if err != nil {
		return err
	}
	sps.SeqScalingMatrixPresentFlag = scalingPresent == 1
	if !sps.SeqScalingMatrixPresentFlag {
		return nil
	}

	limit := 8
	if sps.ChromaFormatIDC == 3 {
		limit = 12
	}
	for i := 0; i < limit; i++ {
		present, err := br.readBit()
		if err != nil {
			return err
		}
		if present == 1 {
			sizeOfScalingList := 16
			if i >= 6 {
				sizeOfScalingList = 64
			}
			if err := parseScalingList(br, sizeOfScalingList); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseScalingList consumes one seq_scaling_list; the gateway does not
// need the actual scaling values, only correct bit advancement.
func parseScalingList(br *bitReader, sizeOfScalingList int) error {
	lastScale := 8
	nextScale := 8
	for j := 0; j < sizeOfScalingList; j++ {
		if nextScale != 0 {
			deltaScale, err := br.readSE()
			if err != nil {
				return err
			}
			nextScale = (lastScale + deltaScale + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		} else {
			lastScale = 8
		}
	}
	return nil
}

func parseVUI(br *bitReader) (*VUI, error) {
	vui := &VUI{}

	if b, err := br.readBit(); err != nil {
		return nil, err
	} else if b == 1 { // aspect_ratio_info_present_flag
		vui.AspectRatioInfoPresent = true
		ar, err := br.readBits(8)
		if err != nil {
			return nil, err
		}
		vui.AspectRatioIDC = ar
		if ar == 255 { // Extended_SAR
			if vui.SARWidth, err = br.readBits(16); err != nil {
				return nil, err
			}
			if vui.SARHeight, err = br.readBits(16); err != nil {
				return nil, err
			}
		}
	}

	if b, err := br.readBit(); err != nil {
		return nil, err
	} else if b == 1 { // overscan_info_present_flag
		if _, err := br.readBit(); err != nil { // overscan_appropriate_flag
			return nil, err
		}
	}

	if b, err := br.readBit(); err != nil {
		return nil, err
	} else if b == 1 { // video_signal_type_present_flag
		vui.VideoSignalTypePresent = true
		if vui.VideoFormat, err = br.readBits(3); err != nil {
			return nil, err
		}
		fullRange, err := br.readBit()
		if err != nil {
			return nil, err
		}
		vui.VideoFullRange = fullRange == 1
		colourDesc, err := br.readBit()
		if err != nil {
			return nil, err
		}
		if colourDesc == 1 {
			vui.ColourDescription = true
			if vui.ColourPrimaries, err = br.readBits(8); err != nil {
				return nil, err
			}
			if vui.TransferCharacteristics, err = br.readBits(8); err != nil {
				return nil, err
			}
			if vui.MatrixCoefficients, err = br.readBits(8); err != nil {
				return nil, err
			}
		}
	}

	if b, err := br.readBit(); err != nil {
		return nil, err
	} else if b == 1 { // chroma_loc_info_present_flag
		vui.ChromaLocInfoPresent = true
		if vui.ChromaSampleLocTop, err = br.readUE(); err != nil {
			return nil, err
		}
		if vui.ChromaSampleLocBottom, err = br.readUE(); err != nil {
			return nil, err
		}
	}

	if b, err := br.readBit(); err != nil {
		return nil, err
	} else if b == 1 { // vui_timing_info_present_flag
		vui.TimingInfoPresent = true
		units, err := br.readBits(32)
		if err != nil {
			return nil, err
		}
		scale, err := br.readBits(32)
		if err != nil {
			return nil, err
		}
		vui.NumUnitsInTick = uint32(units)
		vui.TimeScale = uint32(scale)
		fixed, err := br.readBit()
		if err != nil {
			return nil, err
		}
		vui.FixedFrameRate = fixed == 1

		if b, err := br.readBit(); err != nil { // nal_hrd_parameters_present_flag
			return nil, err
		} else if b == 1 {
			hrd, err := parseHRD(br)
			if err != nil {
				return nil, err
			}
			vui.NALHRDParameters = hrd
		}
		if b, err := br.readBit(); err != nil { // vcl_hrd_parameters_present_flag
			return nil, err
		} else if b == 1 {
			hrd, err := parseHRD(br)
			if err != nil {
				return nil, err
			}
			vui.VCLHRDParameters = hrd
		}
		if vui.NALHRDParameters != nil || vui.VCLHRDParameters != nil {
			lowDelay, err := br.readBit()
			if err != nil {
				return nil, err
			}
			vui.LowDelayHRD = lowDelay == 1
		}
	}

	if b, err := br.readBit(); err != nil { // pic_struct_present_flag
		return nil, err
	} else if b == 1 {
		vui.PicStructPresent = true
	}

	if b, err := br.readBit(); err != nil { // bitstream_restriction_flag
		return nil, err
	} else if b == 1 {
		vui.BitstreamRestriction = true
		if _, err := br.readBit(); err != nil { // motion_vectors_over_pic_boundaries_flag
			return nil, err
		}
		for i := 0; i < 3; i++ {
			if _, err := br.readUE(); err != nil {
				return nil, err
			}
		}
	}

	return vui, nil
}

func parseHRD(br *bitReader) (*HRDParameters, error) {
	hrd := &HRDParameters{}
	var err error
	if hrd.CPBCntMinus1, err = br.readUE(); err != nil {
		return nil, err
	}
	if hrd.BitRateScale, err = br.readBits(4); err != nil {
		return nil, err
	}
	if hrd.CPBSizeScale, err = br.readBits(4); err != nil {
		return nil, err
	}
	for i := uint(0); i <= hrd.CPBCntMinus1; i++ {
		if _, err = br.readUE(); err != nil { // bit_rate_value_minus1
			return nil, err
		}
		if _, err = br.readUE(); err != nil { // cpb_size_value_minus1
			return nil, err
		}
		if _, err = br.readBit(); err != nil { // cbr_flag
			return nil, err
		}
	}
	if hrd.InitialCPBRemovalDelayLength, err = br.readBits(5); err != nil {
		return nil, err
	}
	if hrd.CPBRemovalDelayLength, err = br.readBits(5); err != nil {
		return nil, err
	}
	if hrd.DPBOutputDelayLength, err = br.readBits(5); err != nil {
		return nil, err
	}
	if hrd.TimeOffsetLength, err = br.readBits(5); err != nil {
		return nil, err
	}
	return hrd, nil
}

// deriveGeometry computes the visible frame dimensions, applying frame
// cropping with the chroma sample crop units of H.264 7.4.2.1.1.
func (s *SPS) deriveGeometry() {
	width := int((s.PicWidthInMbsMinus1 + 1) * 16)
	frameMbsOnlyFactor := 1
	if !s.FrameMbsOnlyFlag {
		// In field-coding streams each map unit represents two
		// macroblock rows (one per field).
		frameMbsOnlyFactor = 2
	}
	height := frameMbsOnlyFactor * int(s.PicHeightInMapUnitsMinus1+1) * 16

	subWidthC, subHeightC := 2, 2
	switch s.ChromaFormatIDC {
	case 1: // 4:2:0
		subWidthC, subHeightC = 2, 2
	case 2: // 4:2:2
		subWidthC, subHeightC = 2, 1
	case 3: // 4:4:4
		subWidthC, subHeightC = 1, 1
	}

	if s.FrameCroppingFlag {
		width -= int(s.FrameCropLeftOffset+s.FrameCropRightOffset) * subWidthC
		height -= int(s.FrameCropTopOffset+s.FrameCropBottomOffset) * subHeightC
	}
	s.Width = width
	s.Height = height
}

// deriveFrameRate computes the frame rate from VUI timing information.
// A coded frame occupies two clock ticks, so fps = timeScale / (2*numUnitsInTick).
func (s *SPS) deriveFrameRate() {
	if s.VUI == nil || !s.VUI.TimingInfoPresent || s.VUI.NumUnitsInTick == 0 {
		s.FrameRate = 0
		return
	}
	s.FrameRate = float64(s.VUI.TimeScale) / (2 * float64(s.VUI.NumUnitsInTick))
}

// ParsePPSID extracts the pic_parameter_set_id from a PPS NAL unit.
func ParsePPSID(nal []byte) (uint, error) {
	nalType, err := NALType(nal)
	if err != nil {
		return 0, err
	}
	if nalType != NALTypePPS {
		return 0, fmt.Errorf("h264bs: expected PPS NAL (type 8), got type %d", nalType)
	}
	rbsp, err := UnescapeRBSP(nal[1:])
	if err != nil {
		return 0, fmt.Errorf("h264bs: PPS unescape: %w", err)
	}
	br := newBitReader(rbsp)
	ppsID, err := br.readUE()
	if err != nil {
		return 0, err
	}
	return ppsID, nil
}
