package main

import (
	"math/big"
)

// FrameEvidence 单轨单检查点的偏移证据。
type FrameEvidence struct {
	Point          string `json:"point"`
	OrigTime       Rat    `json:"orig_time"`
	OrigFramePos   Rat    `json:"orig_frame_pos"`
	OrigFrame      string `json:"orig_frame"`
	MappedTime     Rat    `json:"mapped_time"`
	MappedFramePos Rat    `json:"mapped_frame_pos"`
	MappedFrame    string `json:"mapped_frame"`
	FrameShift     string `json:"frame_shift"`
}

// TrackReport 单轨映射结果与逐检查点偏移证据。
type TrackReport struct {
	ID          string          `json:"id"`
	Start       Rat             `json:"start"`
	Interval    Rat             `json:"interval"`
	MappedStart Rat             `json:"mapped_start"`
	Evidence    []FrameEvidence `json:"evidence"`
}

// framePos 精确帧位置（有理数）：(t - start) / interval。
func framePos(t, start, interval *big.Rat) *big.Rat {
	return rquo(rsub(t, start), interval)
}

// buildTrackReports 对每个检查点计算原帧位置、映射后帧位置，
// 帧序号统一按半偶舍入取整，并给出帧偏移量。
func buildTrackReports(spec *Spec, pm *PiecewiseMap, checkpoints []*big.Rat) []TrackReport {
	reports := []TrackReport{}
	for _, tr := range spec.Tracks {
		mStart := pm.Forward(tr.Start.R)
		rep := TrackReport{
			ID: tr.ID, Start: tr.Start, Interval: tr.Interval,
			MappedStart: Rat{mStart},
			Evidence:    []FrameEvidence{},
		}
		for _, cp := range checkpoints {
			m := pm.Forward(cp)
			op := framePos(cp, tr.Start.R, tr.Interval.R)
			mp := framePos(m, mStart, tr.Interval.R)
			of := roundHalfEven(op)
			mf := roundHalfEven(mp)
			rep.Evidence = append(rep.Evidence, FrameEvidence{
				Point:          "t=" + cp.RatString(),
				OrigTime:       Rat{new(big.Rat).Set(cp)},
				OrigFramePos:   Rat{op},
				OrigFrame:      of.String(),
				MappedTime:     Rat{m},
				MappedFramePos: Rat{mp},
				MappedFrame:    mf.String(),
				FrameShift:     new(big.Int).Sub(mf, of).String(),
			})
		}
		reports = append(reports, rep)
	}
	return reports
}
