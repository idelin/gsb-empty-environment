package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Check 单项核对结果。
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// TrackWindow 同步点在某轨上的帧窗口反向定位结果。
type TrackWindow struct {
	Track        string `json:"track"`
	Frame        string `json:"frame"`
	OrigWindowLo Rat    `json:"orig_window_lo"`
	OrigWindowHi Rat    `json:"orig_window_hi"`
	ContainsSync bool   `json:"contains_sync"`
}

// SyncPointReport 跨轨同步点核对：反向定位区间 + 各轨帧窗口。
type SyncPointReport struct {
	SyncTime    Rat           `json:"sync_time"`
	MappedTime  Rat           `json:"mapped_time"`
	ReverseLo   Rat           `json:"reverse_lo"`
	ReverseHi   Rat           `json:"reverse_hi"`
	RoundTripOK bool          `json:"round_trip_ok"`
	Windows     []TrackWindow `json:"windows"`
	OK          bool          `json:"ok"`
}

// Verification 全部核对项。
type Verification struct {
	KeyPoints   Check   `json:"key_points"`
	Overlaps    []Check `json:"overlap_checks"`
	EventOrder  Check   `json:"event_order"`
	RoundTrip   Check   `json:"round_trip"`
	SyncSummary Check   `json:"sync_summary"`
	OK          bool    `json:"ok"`
}

// interiorOverlap 内部重叠判定（端点相接不算重叠）。
func interiorOverlap(aLo, aHi, bLo, bHi *big.Rat) bool {
	return maxRat(aLo, bLo).Cmp(minRat(aHi, bHi)) < 0
}

// buildSyncReports 对每个跨轨同步点：正映射 -> 反向定位回原时间区间，
// 并在每轨上按半偶舍入取帧、把帧窗口反映射回原时间，核对覆盖同步时刻。
func buildSyncReports(spec *Spec, pm *PiecewiseMap) []SyncPointReport {
	reports := []SyncPointReport{}
	for _, sp := range spec.SyncPoints {
		m := pm.Forward(sp.R)
		inv := pm.InvertRange(m)
		rep := SyncPointReport{
			SyncTime:    sp,
			MappedTime:  Rat{m},
			ReverseLo:   Rat{inv.Lo},
			ReverseHi:   Rat{inv.Hi},
			RoundTripOK: inv.Lo.Cmp(sp.R) == 0,
			Windows:     []TrackWindow{},
			OK:          true,
		}
		if !rep.RoundTripOK {
			rep.OK = false
		}
		for _, tr := range spec.Tracks {
			mStart := pm.Forward(tr.Start.R)
			pos := framePos(m, mStart, tr.Interval.R)
			k := roundHalfEven(pos)
			center := radd(mStart, rmul(new(big.Rat).SetInt(k), tr.Interval.R))
			half := rquo(tr.Interval.R, ratFromInt(2))
			win := pm.InvertWindow(center, half)
			contains := win.Lo.Cmp(sp.R) <= 0 && sp.R.Cmp(win.Hi) <= 0
			if !contains {
				rep.OK = false
			}
			rep.Windows = append(rep.Windows, TrackWindow{
				Track: tr.ID, Frame: k.String(),
				OrigWindowLo: Rat{win.Lo}, OrigWindowHi: Rat{win.Hi},
				ContainsSync: contains,
			})
		}
		reports = append(reports, rep)
	}
	return reports
}

// verify 关键点、区间重叠、事件次序与同刻关系、往返一致性、同步点汇总。
func verify(spec *Spec, plan *Plan, syncs []SyncPointReport) Verification {
	pm := plan.Map
	v := Verification{Overlaps: []Check{}, OK: true}

	// 1. 关键点检查：T、M 严格递增 => 各段斜率 > 0 => 可逆范围覆盖全时间轴。
	if err := pm.validate(); err != nil {
		v.KeyPoints = Check{Name: "key_points", OK: false, Detail: err.Error()}
		v.OK = false
	} else {
		v.KeyPoints = Check{Name: "key_points", OK: true,
			Detail: fmt.Sprintf("%d 个关键点：原时间与映射时间均严格递增，各段斜率>0，可逆范围覆盖全时间轴", len(pm.Knots))}
	}

	// 2. 区间重叠检查。
	pairwise := true
	for i := 0; i+1 < len(plan.Segments); i++ {
		if plan.Segments[i].End.Cmp(plan.Segments[i+1].Start) > 0 {
			pairwise = false
		}
	}
	v.Overlaps = append(v.Overlaps, Check{Name: "compressed_pairwise", OK: pairwise,
		Detail: "压缩段两两无内部重叠"})
	if !pairwise {
		v.OK = false
	}

	protOK := true
	protDetail := "压缩段与全部受保护段（鼠标拖动/音频连续段）无内部重叠"
	for _, s := range plan.Segments {
		for _, p := range spec.Protected {
			if p.Start.R == nil || p.End.R == nil || p.Start.R.Cmp(p.End.R) >= 0 {
				continue
			}
			if interiorOverlap(s.Start, s.End, p.Start.R, p.End.R) {
				protOK = false
				protDetail = fmt.Sprintf("压缩段[%s,%s] 与受保护段[%s,%s](%s) 重叠",
					s.Start, s.End, p.Start, p.End, p.Kind)
			}
		}
	}
	v.Overlaps = append(v.Overlaps, Check{Name: "compressed_vs_protected", OK: protOK, Detail: protDetail})
	if !protOK {
		v.OK = false
	}

	mappedOK := true
	mappedDetail := "映射后的压缩段像与受保护段像仍无内部重叠"
	for _, s := range plan.Segments {
		ms, me := pm.Forward(s.Start), pm.Forward(s.End)
		for _, p := range spec.Protected {
			if p.Start.R == nil || p.End.R == nil || p.Start.R.Cmp(p.End.R) >= 0 {
				continue
			}
			ps, pe := pm.Forward(p.Start.R), pm.Forward(p.End.R)
			if interiorOverlap(ms, me, ps, pe) {
				mappedOK = false
				mappedDetail = fmt.Sprintf("映射后压缩段像[%s,%s] 与受保护段像[%s,%s](%s) 重叠",
					ms, me, ps, pe, p.Kind)
			}
		}
	}
	v.Overlaps = append(v.Overlaps, Check{Name: "mapped_compressed_vs_protected", OK: mappedOK, Detail: mappedDetail})
	if !mappedOK {
		v.OK = false
	}

	// 3. 事件次序与同刻关系：同刻必同像，异刻必异像。
	type ev struct{ t, m *big.Rat }
	evs := make([]ev, 0, len(spec.Events))
	for _, e := range spec.Events {
		evs = append(evs, ev{t: e.T.R, m: pm.Forward(e.T.R)})
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].t.Cmp(evs[j].t) < 0 })
	orderOK, simOK := true, true
	for i := 0; i+1 < len(evs); i++ {
		if evs[i].m.Cmp(evs[i+1].m) > 0 {
			orderOK = false
		}
		sameT := evs[i].t.Cmp(evs[i+1].t) == 0
		sameM := evs[i].m.Cmp(evs[i+1].m) == 0
		if sameT != sameM {
			simOK = false
		}
	}
	v.EventOrder = Check{Name: "event_order", OK: orderOK && simOK,
		Detail: fmt.Sprintf("事件次序保持=%v，同刻关系保持=%v（同刻必同像，异刻必异像）", orderOK, simOK)}
	if !(orderOK && simOK) {
		v.OK = false
	}

	// 4. 往返一致性：关键点、事件时刻、同步点、轨道起点。
	rtOK := true
	bad := ""
	checkRT := func(t *big.Rat) {
		if pm.Inverse(pm.Forward(t)).Cmp(t) != 0 {
			rtOK = false
			bad = t.RatString()
		}
	}
	for _, k := range pm.Knots {
		checkRT(k.T)
		if pm.Forward(pm.Inverse(k.M)).Cmp(k.M) != 0 {
			rtOK = false
			bad = k.M.RatString()
		}
	}
	for _, e := range spec.Events {
		checkRT(e.T.R)
	}
	for _, sp := range spec.SyncPoints {
		checkRT(sp.R)
	}
	for _, tr := range spec.Tracks {
		checkRT(tr.Start.R)
	}
	v.RoundTrip = Check{Name: "round_trip", OK: rtOK,
		Detail: "关键点、事件时刻、同步点、轨道起点均满足 逆(正(t))=t 且 正(逆(m))=m"}
	if !rtOK {
		v.RoundTrip.Detail = "往返失败于 " + bad
		v.OK = false
	}

	// 5. 跨轨同步点汇总。
	syncOK := true
	for _, s := range syncs {
		if !s.OK {
			syncOK = false
		}
	}
	v.SyncSummary = Check{Name: "sync_summary", OK: syncOK,
		Detail: fmt.Sprintf("%d 个跨轨同步点：反向定位区间均覆盖原同步时刻，各轨帧窗口一致", len(syncs))}
	if !syncOK {
		v.OK = false
	}

	return v
}
