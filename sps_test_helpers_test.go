package h264bs

// bitWriter serializes big-endian bits and unsigned/signed Exp-Golomb
// codes, mirroring bitReader for test fixtures.
type bitWriter struct {
	bits []byte
	n    int
}

func (w *bitWriter) writeBit(b uint) {
	if w.n&7 == 0 {
		w.bits = append(w.bits, 0)
	}
	if b != 0 {
		w.bits[w.n>>3] |= 1 << (7 - uint(w.n&7))
	}
	w.n++
}

func (w *bitWriter) writeBits(v uint, n int) {
	for i := n - 1; i >= 0; i-- {
		w.writeBit((v >> uint(i)) & 1)
	}
}

// writeUE encodes an unsigned Exp-Golomb code (ue(v)).
func (w *bitWriter) writeUE(v uint) {
	codeNum := v + 1
	length := bitLen(codeNum)
	for i := 0; i < length-1; i++ {
		w.writeBit(0)
	}
	w.writeBits(codeNum, length)
}

// writeSE encodes a signed Exp-Golomb code (se(v)).
func (w *bitWriter) writeSE(v int) {
	var codeNum uint
	if v > 0 {
		codeNum = uint(2*v - 1)
	} else {
		codeNum = uint(-2 * v)
	}
	w.writeUE(codeNum)
}

func (w *bitWriter) writeTrailingBits() {
	w.writeBit(1) // rbsp_stop_one_bit
	for w.n&7 != 0 {
		w.writeBit(0) // rbsp_alignment_zero_bit
	}
}

func bitLen(v uint) int {
	n := 0
	for x := v; x > 0; x >>= 1 {
		n++
	}
	return n
}

// writeTestHRD writes a minimal hrd_parameters block (one CPB, CBR).
func writeTestHRD(w *bitWriter) {
	w.writeUE(0)       // cpb_cnt_minus1
	w.writeBits(0, 4)  // bit_rate_scale
	w.writeBits(0, 4)  // cpb_size_scale
	w.writeUE(0)       // bit_rate_value_minus1
	w.writeUE(0)       // cpb_size_value_minus1
	w.writeBit(1)      // cbr_flag
	w.writeBits(15, 5) // initial_cpb_removal_delay_length_minus1
	w.writeBits(15, 5) // cpb_removal_delay_length_minus1
	w.writeBits(15, 5) // dpb_output_delay_length_minus1
	w.writeBits(0, 5)  // time_offset_length
}

// highProfileSPSParams describes a hand-built High-profile SPS.
type highProfileSPSParams struct {
	spsID                     uint
	chromaFormatIDC           uint
	separateColourPlane       bool
	seqScalingMatrixPresent   bool
	scalingListPresent        []bool // per-list present flags
	log2MaxFrameNumMinus4     uint
	picOrderCntType           uint
	maxNumRefFrames           uint
	picWidthInMbsMinus1       uint
	picHeightInMapUnitsMinus1 uint
	frameMbsOnly              bool
	frameCropping             bool
	cropLeft, cropRight       uint
	cropTop, cropBottom       uint
	timingPresent             bool
	numUnitsInTick, timeScale uint32
	fixedFrameRate            bool
	nalHRDPresent             bool
	picStructPresent          bool
	bitstreamRestriction      bool
	fullVUI                   bool
}

// buildHighProfileSPS constructs a High-profile (profile_idc=100) SPS
// NAL unit by hand, including the High-profile extension, frame
// cropping and VUI timing information.
func buildHighProfileSPS(p highProfileSPSParams) []byte {
	w := &bitWriter{}
	w.writeBits(100, 8) // profile_idc = High
	w.writeBits(0, 8)   // constraint flags + reserved zero bits
	w.writeBits(40, 8)  // level_idc = 4.0
	w.writeUE(p.spsID)

	// High-profile extension.
	w.writeUE(p.chromaFormatIDC) // chroma_format_idc
	if p.chromaFormatIDC == 3 {
		if p.separateColourPlane {
			w.writeBit(1)
		} else {
			w.writeBit(0)
		}
	}
	w.writeUE(0)  // bit_depth_luma_minus8
	w.writeUE(0)  // bit_depth_chroma_minus8
	w.writeBit(0) // qpprime_y_zero_transform_bypass_flag
	if p.seqScalingMatrixPresent {
		w.writeBit(1)
		limit := 8
		if p.chromaFormatIDC == 3 {
			limit = 12
		}
		for i := 0; i < limit; i++ {
			present := i < len(p.scalingListPresent) && p.scalingListPresent[i]
			if present {
				w.writeBit(1)
				size := 16
				if i >= 6 {
					size = 64
				}
				// delta_scale=0 keeps each list flat (fallback rule).
				for j := 0; j < size; j++ {
					w.writeSE(0)
				}
			} else {
				w.writeBit(0) // seq_scaling_list_present_flag
			}
		}
	} else {
		w.writeBit(0) // seq_scaling_matrix_present_flag
	}

	w.writeUE(p.log2MaxFrameNumMinus4)
	w.writeUE(p.picOrderCntType)
	if p.picOrderCntType == 0 {
		w.writeUE(0) // log2_max_pic_order_cnt_lsb_minus4
	} else if p.picOrderCntType == 1 {
		w.writeBit(0) // delta_pic_order_always_zero_flag
		w.writeSE(-2) // offset_for_non_ref_pic
		w.writeSE(4)  // offset_for_top_to_bottom_field
		w.writeUE(2)  // num_ref_frames_in_pic_order_cnt_cycle
		w.writeSE(1)
		w.writeSE(-1)
	} else if p.picOrderCntType == 2 {
		// nothing extra for type 2
	}
	w.writeUE(p.maxNumRefFrames)
	w.writeBit(0) // gaps_in_frame_num_value_allowed_flag
	w.writeUE(p.picWidthInMbsMinus1)
	w.writeUE(p.picHeightInMapUnitsMinus1)
	if p.frameMbsOnly {
		w.writeBit(1) // frame_mbs_only_flag
	} else {
		w.writeBit(0)
		w.writeBit(0) // mb_adaptive_frame_field_flag
	}
	w.writeBit(1) // direct_8x8_inference_flag

	if p.frameCropping {
		w.writeBit(1)
		w.writeUE(p.cropLeft)
		w.writeUE(p.cropRight)
		w.writeUE(p.cropTop)
		w.writeUE(p.cropBottom)
	} else {
		w.writeBit(0)
	}

	if p.timingPresent {
		w.writeBit(1) // vui_parameters_present_flag
		if p.fullVUI {
			w.writeBit(1)       // aspect_ratio_info_present_flag
			w.writeBits(255, 8) // Extended_SAR
			w.writeBits(1, 16)  // sar_width
			w.writeBits(1, 16)  // sar_height
			w.writeBit(1)       // overscan_info_present_flag
			w.writeBit(1)       // overscan_appropriate_flag
			w.writeBit(1)       // video_signal_type_present_flag
			w.writeBits(1, 3)   // video_format
			w.writeBit(1)       // video_full_range_flag
			w.writeBit(1)       // colour_description_present_flag
			w.writeBits(1, 8)   // colour_primaries
			w.writeBits(1, 8)   // transfer_characteristics
			w.writeBits(1, 8)   // matrix_coefficients
			w.writeBit(1)       // chroma_loc_info_present_flag
			w.writeUE(0)        // chroma_sample_loc_type_top_field
			w.writeUE(1)        // chroma_sample_loc_type_bottom_field
		} else {
			w.writeBit(0) // aspect_ratio_info_present_flag
			w.writeBit(0) // overscan_info_present_flag
			w.writeBit(0) // video_signal_type_present_flag
			w.writeBit(0) // chroma_loc_info_present_flag
		}
		w.writeBit(1) // vui_timing_info_present_flag
		w.writeBits(uint(p.numUnitsInTick), 32)
		w.writeBits(uint(p.timeScale), 32)
		if p.fixedFrameRate {
			w.writeBit(1)
		} else {
			w.writeBit(0)
		}
		if p.nalHRDPresent {
			w.writeBit(1) // nal_hrd_parameters_present_flag
			writeTestHRD(w)
		} else {
			w.writeBit(0)
		}
		w.writeBit(0) // vcl_hrd_parameters_present_flag
		if p.picStructPresent {
			w.writeBit(1)
		} else {
			w.writeBit(0)
		}
		if p.bitstreamRestriction {
			w.writeBit(1)
			w.writeBit(1) // motion_vectors_over_pic_boundaries_flag
			w.writeUE(0)  // max_bytes_per_pic_denom
			w.writeUE(0)  // max_bits_per_mb_denom
			w.writeUE(0)  // log2_max_mv_length_vertical
			// log2_max_mv_length_horizontal
			w.writeUE(0)
		} else {
			w.writeBit(0) // bitstream_restriction_flag
		}
	} else {
		w.writeBit(0) // vui_parameters_present_flag
	}

	w.writeTrailingBits()

	nalHeader := byte(0x67) // nal_ref_idc=3, nal_unit_type=7 (SPS)
	return append([]byte{nalHeader}, EscapeRBSP(w.bits)...)
}

// buildBaselineSPS constructs a Baseline-profile (profile_idc=66) SPS
// without High-profile extension fields or cropping.
func buildBaselineSPS(spsID, widthInMbsMinus1, heightInMapUnitsMinus1 uint) []byte {
	w := &bitWriter{}
	w.writeBits(66, 8) // profile_idc = Baseline
	w.writeBits(0, 8)  // constraint flags
	w.writeBits(30, 8) // level_idc = 3.0
	w.writeUE(spsID)
	w.writeUE(0)  // log2_max_frame_num_minus4
	w.writeUE(0)  // pic_order_cnt_type = 0
	w.writeUE(0)  // log2_max_pic_order_cnt_lsb_minus4
	w.writeUE(1)  // max_num_ref_frames
	w.writeBit(0) // gaps_in_frame_num_value_allowed_flag
	w.writeUE(widthInMbsMinus1)
	w.writeUE(heightInMapUnitsMinus1)
	w.writeBit(1) // frame_mbs_only_flag
	w.writeBit(1) // direct_8x8_inference_flag
	w.writeBit(0) // frame_cropping_flag
	w.writeBit(0) // vui_parameters_present_flag
	w.writeTrailingBits()

	return append([]byte{0x67}, EscapeRBSP(w.bits)...)
}

// buildPPS constructs a minimal PPS NAL unit referencing spsID.
func buildPPS(ppsID, spsID uint) []byte {
	w := &bitWriter{}
	w.writeUE(ppsID)
	w.writeUE(spsID)
	w.writeBit(0)     // entropy_coding_mode_flag (CAVLC)
	w.writeBit(0)     // bottom_field_pic_order_in_frame_present_flag
	w.writeUE(0)      // num_slice_groups_minus1
	w.writeUE(0)      // num_ref_idx_l0_default_active_minus1
	w.writeUE(0)      // num_ref_idx_l1_default_active_minus1
	w.writeBit(0)     // weighted_pred_flag
	w.writeBits(0, 2) // weighted_bipred_idc
	w.writeSE(0)      // pic_init_qp_minus26
	w.writeSE(0)      // pic_init_qs_minus26
	w.writeSE(0)      // chroma_qp_index_offset
	w.writeBit(1)     // deblocking_filter_control_present_flag
	w.writeBit(0)     // constrained_intra_pred_flag
	w.writeBit(0)     // redundant_pic_cnt_present_flag
	w.writeTrailingBits()

	return append([]byte{0x68}, EscapeRBSP(w.bits)...) // type 8 = PPS
}
