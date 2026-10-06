package main

import (
	"math/big"
	"sort"
)

// KnotReport 正映射关键点（t->m）或反映射关键点（m->t）。
type KnotReport struct {
	T Rat `json:"t"`
	M Rat `json:"m"`
}

// MappingReport 正反映射的关键点表示。
type MappingReport struct {
	Kind         string       `json:"kind"`
	Knots        []KnotReport `json:"knots"`
	InverseKnots []KnotReport `json:"inverse_knots"`
}

// SegmentReport 压缩段及其理由。
type SegmentReport struct {
	Start       Rat    `json:"start"`
	End         Rat    `json:"end"`
	MappedStart Rat    `json:"mapped_start"`
	MappedEnd   Rat    `json:"mapped_end"`
	Reason      string `json:"reason"`
}

// SkipReport 未压缩区间及其理由。
type SkipReport struct {
	Start  Rat    `json:"start"`
	End    Rat    `json:"end"`
	Reason string `json:"reason"`
}

// Report 完整输出报告。
type Report struct {
	Mapping      MappingReport     `json:"mapping"`
	Segments     []SegmentReport   `json:"segments"`
	Skipped      []SkipReport      `json:"skipped"`
	Tracks       []TrackReport     `json:"tracks"`
	SyncPoints   []SyncPointReport `json:"sync_points"`
	Verification Verification      `json:"verification"`
}

// buildReport 组装完整报告：正反映射、压缩段理由、每轨偏移证据、
// 同步点反向定位，以及全部核对结果。
func buildReport(spec *Spec, plan *Plan) *Report {
	pm := plan.Map
	rep := &Report{
		Segments: []SegmentReport{},
		Skipped:  []SkipReport{},
	}
	rep.Mapping.Kind = "单调分段线性：压缩段斜率∈(0,1)，其余斜率=1；所有压缩段共享同一映射，全程严格递增可逆"
	rep.Mapping.Knots = []KnotReport{}
	rep.Mapping.InverseKnots = []KnotReport{}
	for _, k := range pm.Knots {
		rep.Mapping.Knots = append(rep.Mapping.Knots, KnotReport{T: Rat{k.T}, M: Rat{k.M}})
		rep.Mapping.InverseKnots = append(rep.Mapping.InverseKnots, KnotReport{T: Rat{k.M}, M: Rat{k.T}})
	}
	for _, s := range plan.Segments {
		rep.Segments = append(rep.Segments, SegmentReport{
			Start: Rat{s.Start}, End: Rat{s.End},
			MappedStart: Rat{s.MStart}, MappedEnd: Rat{s.MEnd},
			Reason: s.Reason,
		})
	}
	for _, s := range plan.Skipped {
		rep.Skipped = append(rep.Skipped, SkipReport{
			Start: Rat{s.Start}, End: Rat{s.End}, Reason: s.Reason,
		})
	}

	// 检查点：映射关键点、同步点、轨道起点（去重排序）。
	cpSet := map[string]bool{}
	cps := []*big.Rat{}
	addCP := func(t *big.Rat) {
		key := t.RatString()
		if !cpSet[key] {
			cpSet[key] = true
			cps = append(cps, t)
		}
	}
	for _, k := range pm.Knots {
		addCP(k.T)
	}
	for _, sp := range spec.SyncPoints {
		addCP(sp.R)
	}
	for _, tr := range spec.Tracks {
		addCP(tr.Start.R)
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].Cmp(cps[j]) < 0 })

	rep.Tracks = buildTrackReports(spec, pm, cps)
	rep.SyncPoints = buildSyncReports(spec, pm)
	rep.Verification = verify(spec, plan, rep.SyncPoints)
	return rep
}
