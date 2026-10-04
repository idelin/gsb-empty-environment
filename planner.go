package main

import (
	"fmt"
	"sort"
)

// MaxReservations 是题目约束：单次最多处理 25 单预约。
const MaxReservations = 25

// Problem 描述一次预约规划请求。
// 时间统一使用整数分钟（相对同一参考时刻）。
type Problem struct {
	EntryBuffer  int64         `json:"entry_buffer_minutes"` // 入场缓冲（分钟）
	ExitBuffer   int64         `json:"exit_buffer_minutes"`  // 离场缓冲（分钟）
	Spots        []Spot        `json:"spots"`
	Reservations []Reservation `json:"reservations"`
}

// Spot 是一个车位，vehicle_types 列出可接纳的车型。
type Spot struct {
	ID    string   `json:"id"`
	Types []string `json:"vehicle_types"`
}

// Reservation 是一单预约。
type Reservation struct {
	ID          string `json:"id"`
	VehicleType string `json:"vehicle_type"`
	Arrival     int64  `json:"arrival"`   // 预计入场时刻（分钟）
	Departure   int64  `json:"departure"` // 预计离场时刻（分钟）
	Revenue     int64  `json:"revenue"`
	Confirmed   bool   `json:"confirmed"` // 已确认预约，不可撤回
}

// occupy 返回预约对车位的实际占用区间 [start, end)，左闭右开：
// 占用 = [arrival-entry_buffer, departure+exit_buffer)。
func (p *Problem) occupy(r Reservation) interval {
	return interval{start: r.Arrival - p.EntryBuffer, end: r.Departure + p.ExitBuffer, res: -1}
}

type interval struct {
	start, end int64
	res        int // 原始预约下标，-1 表示未绑定
}

// overlaps 按左闭右开规则判定冲突：仅当区间真正相交（交集非空）才冲突，
// 首尾相接（a.end == b.start）不算冲突。
func overlaps(a, b interval) bool {
	return a.start < b.end && b.start < a.end
}

// ---- 输出结构 ----

// Assignment 描述一单被接纳预约的车位安排。
type Assignment struct {
	ReservationID string `json:"reservation_id"`
	SpotID        string `json:"spot_id"`
	OccupyStart   int64  `json:"occupy_start"` // 含入场缓冲
	OccupyEnd     int64  `json:"occupy_end"`   // 含离场缓冲，左闭右开
	Revenue       int64  `json:"revenue"`
	Confirmed     bool   `json:"confirmed"`
}

// ConflictEvidence 是一条冲突证据：被拒预约与某已接纳预约在某车位上时段相交。
type ConflictEvidence struct {
	WithReservation string `json:"with_reservation"`
	SpotID          string `json:"spot_id"`
	OverlapStart    int64  `json:"overlap_start"`
	OverlapEnd      int64  `json:"overlap_end"`
}

// Rejection 描述一单被回绝的预约及其原因/证据。
type Rejection struct {
	ReservationID string             `json:"reservation_id"`
	Reason        string             `json:"reason"`
	Conflicts     []ConflictEvidence `json:"conflicts,omitempty"`
}

// SlotEntry 是车位时间表中的一项。
type SlotEntry struct {
	ReservationID string `json:"reservation_id"`
	OccupyStart   int64  `json:"occupy_start"`
	OccupyEnd     int64  `json:"occupy_end"`
}

// SpotSchedule 是一个车位的占用时间表（按开始时刻升序）。
type SpotSchedule struct {
	SpotID  string      `json:"spot_id"`
	Entries []SlotEntry `json:"entries"`
}

// Result 是规划结果。
type Result struct {
	TotalRevenue int64          `json:"total_revenue"`
	Accepted     []Assignment   `json:"accepted"`
	Rejected     []Rejection    `json:"rejected"`
	Schedule     []SpotSchedule `json:"schedule"`
}

// validate 校验输入合法性。
func validate(p *Problem) error {
	if len(p.Reservations) > MaxReservations {
		return fmt.Errorf("too many reservations: %d > %d", len(p.Reservations), MaxReservations)
	}
	if p.EntryBuffer < 0 || p.ExitBuffer < 0 {
		return fmt.Errorf("buffers must be non-negative")
	}
	spotIDs := map[string]bool{}
	for _, s := range p.Spots {
		if s.ID == "" {
			return fmt.Errorf("spot id must not be empty")
		}
		if spotIDs[s.ID] {
			return fmt.Errorf("duplicate spot id %q", s.ID)
		}
		spotIDs[s.ID] = true
	}
	resIDs := map[string]bool{}
	for _, r := range p.Reservations {
		if r.ID == "" {
			return fmt.Errorf("reservation id must not be empty")
		}
		if resIDs[r.ID] {
			return fmt.Errorf("duplicate reservation id %q", r.ID)
		}
		resIDs[r.ID] = true
		if r.Arrival > r.Departure {
			return fmt.Errorf("reservation %q: arrival %d is after departure %d", r.ID, r.Arrival, r.Departure)
		}
		if r.Revenue < 0 {
			return fmt.Errorf("reservation %q: negative revenue %d", r.ID, r.Revenue)
		}
	}
	return nil
}

// eligibleSpots 返回可接纳该车型的车位下标列表。
func eligibleSpots(p *Problem, vehicleType string) []int {
	var out []int
	for i, s := range p.Spots {
		for _, t := range s.Types {
			if t == vehicleType {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// solver 执行分支限界搜索。
type solver struct {
	prob       *Problem
	order      []int   // 按收益降序排列的预约下标
	remSum     []int64 // remSum[k] = order[k:] 的收益和（上界）
	eligible   [][]int // 每个预约（原始下标）的可用车位
	occ        []interval
	spotBusy   [][]interval // 每个车位当前已接纳的占用区间
	curAssign  []int        // 原始预约下标 -> 车位下标，-1 拒绝，-2 未决定
	best       int64
	bestAssign []int
	found      bool
}

// Solve 求接纳收益最大的预约子集（已确认预约必须全部接纳），并给出车位安排。
func Solve(p *Problem) (*Result, error) {
	if err := validate(p); err != nil {
		return nil, err
	}

	n := len(p.Reservations)
	s := &solver{
		prob:      p,
		eligible:  make([][]int, n),
		occ:       make([]interval, n),
		spotBusy:  make([][]interval, len(p.Spots)),
		curAssign: make([]int, n),
		best:      -1,
	}
	for i, r := range p.Reservations {
		s.eligible[i] = eligibleSpots(p, r.VehicleType)
		s.occ[i] = p.occupy(r)
		s.occ[i].res = i
		s.curAssign[i] = -2
	}

	// 已确认预约若没有任何可用车位，问题必然不可行。
	for i, r := range p.Reservations {
		if r.Confirmed && len(s.eligible[i]) == 0 {
			return nil, fmt.Errorf("confirmed reservation %q (type %q) has no eligible spot", r.ID, r.VehicleType)
		}
	}

	// 按收益降序排列以加速剪枝，并列时按 ID 保证确定性。
	s.order = make([]int, n)
	for i := range s.order {
		s.order[i] = i
	}
	sort.Slice(s.order, func(a, b int) bool {
		ra, rb := p.Reservations[s.order[a]], p.Reservations[s.order[b]]
		if ra.Revenue != rb.Revenue {
			return ra.Revenue > rb.Revenue
		}
		return ra.ID < rb.ID
	})
	s.remSum = make([]int64, n+1)
	for k := n - 1; k >= 0; k-- {
		s.remSum[k] = s.remSum[k+1] + p.Reservations[s.order[k]].Revenue
	}

	s.dfs(0, 0)

	if !s.found {
		return nil, fmt.Errorf("confirmed reservations are mutually infeasible: %s", confirmedConflictEvidence(p))
	}

	return s.buildResult(), nil
}

// dfs 处理 order[pos] 起的预约；rev 为当前已接纳收益。
func (s *solver) dfs(pos int, rev int64) {
	if pos == len(s.order) {
		if !s.found || rev > s.best {
			s.found = true
			s.best = rev
			s.bestAssign = append([]int(nil), s.curAssign...)
		}
		return
	}
	// 上界剪枝：即使余下全部接纳也无法超过当前最优。
	if s.found && rev+s.remSum[pos] <= s.best {
		return
	}
	idx := s.order[pos]
	r := s.prob.Reservations[idx]
	occ := s.occ[idx]

	// 分支一：尝试停入每个可用且无冲突的车位。
	for _, sp := range s.eligible[idx] {
		if s.spotFree(sp, occ) {
			s.spotBusy[sp] = append(s.spotBusy[sp], occ)
			s.curAssign[idx] = sp
			s.dfs(pos+1, rev+r.Revenue)
			s.curAssign[idx] = -2
			s.spotBusy[sp] = s.spotBusy[sp][:len(s.spotBusy[sp])-1]
		}
	}
	// 分支二：回绝该预约（已确认预约不可回绝）。
	if !r.Confirmed {
		s.curAssign[idx] = -1
		s.dfs(pos+1, rev)
		s.curAssign[idx] = -2
	}
}

// spotFree 判断区间 occ 是否能停入车位 sp（左闭右开，相接不冲突）。
func (s *solver) spotFree(sp int, occ interval) bool {
	for _, busy := range s.spotBusy[sp] {
		if overlaps(busy, occ) {
			return false
		}
	}
	return true
}

// confirmedConflictEvidence 在已确认预约彼此不可行时给出诊断信息。
func confirmedConflictEvidence(p *Problem) string {
	msg := ""
	for i := 0; i < len(p.Reservations); i++ {
		for j := i + 1; j < len(p.Reservations); j++ {
			a, b := p.Reservations[i], p.Reservations[j]
			if !a.Confirmed || !b.Confirmed {
				continue
			}
			ia, ib := p.occupy(a), p.occupy(b)
			if !overlaps(ia, ib) {
				continue
			}
			// 是否存在同时可接纳两者的车位。
			shared := false
			for _, s := range p.Spots {
				okA, okB := false, false
				for _, t := range s.Types {
					if t == a.VehicleType {
						okA = true
					}
					if t == b.VehicleType {
						okB = true
					}
				}
				if okA && okB {
					shared = true
					break
				}
			}
			if shared {
				lo := ia.start
				if ib.start > lo {
					lo = ib.start
				}
				hi := ia.end
				if ib.end < hi {
					hi = ib.end
				}
				msg += fmt.Sprintf("%s and %s overlap on [%d,%d) with a common eligible spot; ",
					a.ID, b.ID, lo, hi)
			}
		}
	}
	if msg == "" {
		msg = "spot capacity is insufficient for the confirmed set"
	}
	return msg
}

// buildResult 根据最优指派构造输出。
func (s *solver) buildResult() *Result {
	p := s.prob
	res := &Result{TotalRevenue: s.best}

	schedules := make([][]SlotEntry, len(p.Spots))
	for i, r := range p.Reservations {
		sp := s.bestAssign[i]
		if sp >= 0 {
			occ := s.occ[i]
			res.Accepted = append(res.Accepted, Assignment{
				ReservationID: r.ID,
				SpotID:        p.Spots[sp].ID,
				OccupyStart:   occ.start,
				OccupyEnd:     occ.end,
				Revenue:       r.Revenue,
				Confirmed:     r.Confirmed,
			})
			schedules[sp] = append(schedules[sp], SlotEntry{
				ReservationID: r.ID,
				OccupyStart:   occ.start,
				OccupyEnd:     occ.end,
			})
		} else {
			res.Rejected = append(res.Rejected, s.buildRejection(i))
		}
	}

	for i, sp := range p.Spots {
		entries := schedules[i]
		sort.Slice(entries, func(a, b int) bool {
			if entries[a].OccupyStart != entries[b].OccupyStart {
				return entries[a].OccupyStart < entries[b].OccupyStart
			}
			return entries[a].ReservationID < entries[b].ReservationID
		})
		if entries == nil {
			entries = []SlotEntry{}
		}
		res.Schedule = append(res.Schedule, SpotSchedule{SpotID: sp.ID, Entries: entries})
	}
	if res.Accepted == nil {
		res.Accepted = []Assignment{}
	}
	if res.Rejected == nil {
		res.Rejected = []Rejection{}
	}
	return res
}

// buildRejection 为被拒预约生成原因与冲突证据。
func (s *solver) buildRejection(idx int) Rejection {
	p := s.prob
	r := p.Reservations[idx]
	occ := s.occ[idx]
	rej := Rejection{ReservationID: r.ID}

	if len(s.eligible[idx]) == 0 {
		rej.Reason = fmt.Sprintf("no spot accepts vehicle type %q", r.VehicleType)
		return rej
	}
	rej.Reason = "rejected to maximize total revenue"

	// 证据：在每个可用车位上，列出与该预约占用区间相交的已接纳预约。
	for _, sp := range s.eligible[idx] {
		for j, other := range p.Reservations {
			if s.bestAssign[j] != sp {
				continue
			}
			o := s.occ[j]
			if overlaps(occ, o) {
				lo := occ.start
				if o.start > lo {
					lo = o.start
				}
				hi := occ.end
				if o.end < hi {
					hi = o.end
				}
				rej.Conflicts = append(rej.Conflicts, ConflictEvidence{
					WithReservation: other.ID,
					SpotID:          p.Spots[sp].ID,
					OverlapStart:    lo,
					OverlapEnd:      hi,
				})
			}
		}
	}
	sort.Slice(rej.Conflicts, func(a, b int) bool {
		if rej.Conflicts[a].SpotID != rej.Conflicts[b].SpotID {
			return rej.Conflicts[a].SpotID < rej.Conflicts[b].SpotID
		}
		return rej.Conflicts[a].WithReservation < rej.Conflicts[b].WithReservation
	})
	return rej
}
