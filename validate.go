package main

import (
	"fmt"
	"sort"
)

// Validate 独立校验求解结果与时间线, 返回违规列表 (空表示通过)。
// 关键校验: 两间房间处于同一合并块期间, 不能被分别分配给不同会议。
func Validate(cfg Config, sol Solution, tl [][]Segment) []string {
	var violations []string
	add := func(format string, args ...interface{}) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}
	k := cfg.NumRooms
	if len(tl) != k {
		add("时间线房间数 %d 与配置 %d 不符", len(tl), k)
		return violations
	}

	// 1. 每个房间的时间段不得重叠
	for r := 0; r < k; r++ {
		segs := tl[r]
		for i := 1; i < len(segs); i++ {
			if segs[i].Start < segs[i-1].End {
				add("房间 %d: 时间段 [%d,%d) 与前一段重叠", r+1, segs[i].Start, segs[i].End)
			}
		}
	}

	// 2. 分配/拒绝账目一致
	asgByID := map[string]Assignment{}
	for _, a := range sol.Assigns {
		if _, dup := asgByID[a.Meeting.ID]; dup {
			add("会议 %s 被重复分配", a.Meeting.ID)
		}
		asgByID[a.Meeting.ID] = a
	}
	known := map[string]bool{}
	for _, m := range cfg.Meetings {
		known[m.ID] = true
	}
	rejSet := map[string]bool{}
	rej := 0
	for _, m := range sol.Rejected {
		if !known[m.ID] {
			add("拒绝列表中出现未知会议 %s", m.ID)
		}
		if _, ok := asgByID[m.ID]; ok {
			add("会议 %s 既被分配又被拒绝", m.ID)
		}
		rejSet[m.ID] = true
		rej += m.Weight
	}
	if rej != sol.RejWeight {
		add("拒绝权重不一致: 实算 %d, 报告 %d", rej, sol.RejWeight)
	}
	for id := range known {
		_, a := asgByID[id]
		if !a && !rejSet[id] {
			add("会议 %s 既未分配也未被拒绝", id)
		}
	}

	// 3. 会议落位: 容量足够且时间线上确有对应会议段
	for _, a := range sol.Assigns {
		m := a.Meeting
		if a.Lo < 0 || a.Hi >= k || a.Lo > a.Hi {
			add("会议 %s: 房间范围 [%d,%d] 非法", m.ID, a.Lo+1, a.Hi+1)
			continue
		}
		if a.Hi-a.Lo+1 < m.Rooms {
			add("会议 %s: 分配 %d 间, 少于需求 %d 间", m.ID, a.Hi-a.Lo+1, m.Rooms)
		}
		for r := a.Lo; r <= a.Hi; r++ {
			ok := false
			for _, s := range tl[r] {
				if s.State == StMeeting && s.MeetingID == m.ID && s.Start <= m.Start && s.End >= m.End {
					ok = true
				}
			}
			if !ok {
				add("会议 %s 在房间 %d 的 [%d,%d) 内未被安排", m.ID, r+1, m.Start, m.End)
			}
		}
	}

	// 4. 合并排他性: 同一时间切片内, 处于同一合并块的房间不得被分别分配
	timeSet := map[int]bool{}
	for r := 0; r < k; r++ {
		for _, s := range tl[r] {
			timeSet[s.Start] = true
			if s.End >= 0 {
				timeSet[s.End] = true
			}
		}
	}
	times := make([]int, 0, len(timeSet))
	for t := range timeSet {
		times = append(times, t)
	}
	sort.Ints(times)
	segAt := func(r, t0, t1 int) *Segment {
		for i := range tl[r] {
			s := &tl[r][i]
			end := s.End
			if end < 0 {
				end = int(^uint(0) >> 1)
			}
			if s.Start <= t0 && end >= t1 {
				return s
			}
		}
		return nil
	}
	inBlock := func(s *Segment) bool {
		return s.State == StMergedIdle || s.State == StMeeting || s.State == StMergePrep
	}
	for i := 0; i+1 < len(times); i++ {
		t0, t1 := times[i], times[i+1]
		infos := make([]*Segment, k)
		for r := 0; r < k; r++ {
			infos[r] = segAt(r, t0, t1)
		}
		for x := 0; x < k; x++ {
			for y := x + 1; y < k; y++ {
				sx, sy := infos[x], infos[y]
				if sx == nil || sy == nil || !inBlock(sx) || !inBlock(sy) {
					continue
				}
				if sx.BlockLo != sy.BlockLo || sx.BlockHi != sy.BlockHi || sx.BlockHi == sx.BlockLo {
					continue
				}
				ix, iy := "", ""
				if sx.State == StMeeting {
					ix = sx.MeetingID
				}
				if sy.State == StMeeting {
					iy = sy.MeetingID
				}
				if ix != iy {
					add("房间 %d 与 %d 在 [%d,%d) 处于同一合并块 [%d,%d], 却被分别分配 (%q / %q)",
						x+1, y+1, t0, t1, sx.BlockLo+1, sx.BlockHi+1, ix, iy)
				}
			}
		}
	}

	// 5. 会议与所在块一致; 准备时长符合配置; 操作数一致
	merges := map[[3]int]bool{}
	splits := map[[3]int]bool{}
	for r := 0; r < k; r++ {
		for _, s := range tl[r] {
			switch s.State {
			case StMeeting:
				a, ok := asgByID[s.MeetingID]
				if !ok {
					add("时间线上出现未分配的会议 %s", s.MeetingID)
					continue
				}
				if s.BlockLo != a.Lo || s.BlockHi != a.Hi {
					add("会议 %s 所在块 [%d,%d] 与分配范围 [%d,%d] 不一致",
						s.MeetingID, s.BlockLo+1, s.BlockHi+1, a.Lo+1, a.Hi+1)
				}
				if r < a.Lo || r > a.Hi {
					add("会议 %s 出现在分配范围之外的房间 %d", s.MeetingID, r+1)
				}
			case StMergePrep:
				if s.End-s.Start != cfg.MergeTime {
					add("房间 %d 合并准备时长 %d, 应为 %d", r+1, s.End-s.Start, cfg.MergeTime)
				}
				merges[[3]int{s.BlockLo, s.BlockHi, s.Start}] = true
			case StSplitPrep:
				if s.End-s.Start != cfg.SplitTime {
					add("房间 %d 拆分恢复时长 %d, 应为 %d", r+1, s.End-s.Start, cfg.SplitTime)
				}
				splits[[3]int{s.BlockLo, s.BlockHi, s.Start}] = true
			}
		}
	}
	if got := len(merges) + len(splits); got != sol.Ops {
		add("操作数不一致: 时间线含 %d 次, 结果报告 %d 次", got, sol.Ops)
	}
	return violations
}
