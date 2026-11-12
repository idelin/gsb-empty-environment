package timeparse

import (
	"fmt"
	"time"
)

// resolveLocal 解析站点时区 loc 下的一个本地挂钟时间（年月日时分秒）。
//
// 春令时跳时（gap）造成的不存在时刻返回 ErrNonexistentTime；
// 夏令时回退（fold）造成的重复时刻返回两个 UTC 候选，分别携带
// 偏移较大的首次出现与偏移较小的第二次出现；普通时刻返回单个候选。
//
// 这里不依赖固定 60 分钟的 DST 差值：通过对本地时间前后 ±2 小时、
// 每 30 分钟采样一次偏移，收集同一挂钟字段对应的全部物理时刻，
// 因此 Lord Howe 岛等 30 分钟级别的过渡同样适用。
func resolveLocal(loc *time.Location, year, month, day, hour, min, sec int) ([]Candidate, error) {
	// 先用 loc 构造一次，Go 在 gap 上会把时间平移到过渡之后；
	// sameCivil 校验即可识别这种不存在的挂钟时间。
	first := time.Date(year, time.Month(month), day, hour, min, sec, 0, loc)
	if !sameCivil(first, loc, year, month, day, hour, min, sec) {
		return nil, fmt.Errorf("%w: %04d-%02d-%02d %02d:%02d:%02d in %s",
			ErrNonexistentTime, year, month, day, hour, min, sec, loc.String())
	}

	// 以“相同挂钟字段 + UTC 标签”为采样原点，配合采样到的本地偏移
	// 重建物理时刻，从而枚举该挂钟时间可能对应的所有 UTC 瞬间。
	wall := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)

	type key struct{ unix int64 }
	seen := map[key]bool{}
	var candidates []Candidate

	for step := -4; step <= 4; step++ {
		sample := first.Add(time.Duration(step) * 30 * time.Minute)
		_, offset := sample.Zone()
		inst := wall.Add(-time.Duration(offset) * time.Second)
		k := key{inst.Unix()}
		if seen[k] {
			continue
		}
		local := inst.In(loc)
		if !sameCivil(local, loc, year, month, day, hour, min, sec) {
			continue
		}
		seen[k] = true
		zone, off := local.Zone()
		candidates = append(candidates, Candidate{
			UTC:       inst,
			Zone:      zone,
			OffsetSec: off,
			IsDST:     false,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %04d-%02d-%02d %02d:%02d:%02d in %s",
			ErrNonexistentTime, year, month, day, hour, min, sec, loc.String())
	}

	sortCandidates(candidates)

	if len(candidates) > 1 {
		// 回退产生的重复时刻：偏移较大（时钟更靠前）的一次是夏令时。
		for i := range candidates {
			candidates[i].IsDST = candidates[i].OffsetSec > candidates[len(candidates)-1].OffsetSec
		}
	} else {
		candidates[0].IsDST = isDST(candidates[0].UTC.In(loc))
	}
	return candidates, nil
}

// sameCivil 判断 t（转到 loc 后）是否与给定挂钟字段完全一致。
func sameCivil(t time.Time, loc *time.Location, year, month, day, hour, min, sec int) bool {
	local := t.In(loc)
	return local.Year() == year &&
		int(local.Month()) == month &&
		local.Day() == day &&
		local.Hour() == hour &&
		local.Minute() == min &&
		local.Second() == sec
}

func sortCandidates(candidates []Candidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1].UTC.After(candidates[j].UTC); j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
}

// isDST 通过比较当前偏移与半年后的偏移判断当前是否处于夏令时。
// 处于 DST 时的偏移严格大于同区域的标准时偏移，该判据对南北半球均成立。
func isDST(t time.Time) bool {
	_, current := t.Zone()
	_, other := t.AddDate(0, 6, 0).Zone()
	return current > other
}
