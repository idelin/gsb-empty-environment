package main

import (
	"fmt"
	"testing"
)

func solveAndValidate(t *testing.T, cfg Config) Solution {
	t.Helper()
	if err := cfg.check(); err != nil {
		t.Fatalf("配置非法: %v", err)
	}
	sol := Solve(cfg)
	tl := BuildTimeline(cfg, sol)
	if v := Validate(cfg, sol, tl); len(v) > 0 {
		t.Fatalf("校验未通过: %v", v)
	}
	return sol
}

func isAccepted(sol Solution, id string) bool {
	for _, a := range sol.Assigns {
		if a.Meeting.ID == id {
			return true
		}
	}
	return false
}

func TestBasicNoMerge(t *testing.T) {
	cfg := Config{NumRooms: 2, MergeTime: 1, SplitTime: 1, Meetings: []Meeting{
		{ID: "A", Start: 0, End: 5, Rooms: 1, Weight: 3},
		{ID: "B", Start: 0, End: 5, Rooms: 1, Weight: 4},
	}}
	sol := solveAndValidate(t, cfg)
	if sol.RejWeight != 0 || sol.Ops != 0 {
		t.Fatalf("两会议应都被接受且零操作, 得到 拒绝权重=%d 操作=%d", sol.RejWeight, sol.Ops)
	}
}

func TestMergeExclusivity(t *testing.T) {
	// A 合并两房 [1,10); B 与 A 时间重叠且只需一间, 但两房已合并, 必须拒绝 B。
	cfg := Config{NumRooms: 2, MergeTime: 1, SplitTime: 1, Meetings: []Meeting{
		{ID: "A", Start: 1, End: 10, Rooms: 2, Weight: 10},
		{ID: "B", Start: 3, End: 5, Rooms: 1, Weight: 1},
	}}
	sol := solveAndValidate(t, cfg)
	if !isAccepted(sol, "A") || isAccepted(sol, "B") {
		t.Fatalf("应接受 A 拒绝 B, 得到 %+v", sol)
	}
	if sol.Ops != 1 || sol.RejWeight != 1 {
		t.Fatalf("期望 1 次合并、拒绝权重 1, 得到 操作=%d 拒绝=%d", sol.Ops, sol.RejWeight)
	}
}

func TestMergePrepConflict(t *testing.T) {
	// 合并需 2 单位时间: B 的合并窗口 [7,9) 与 A 冲突, 只能拒绝权重小的 A。
	cfg := Config{NumRooms: 2, MergeTime: 2, SplitTime: 1, Meetings: []Meeting{
		{ID: "A", Start: 0, End: 8, Rooms: 1, Weight: 1},
		{ID: "B", Start: 9, End: 12, Rooms: 2, Weight: 10},
	}}
	sol := solveAndValidate(t, cfg)
	if isAccepted(sol, "A") || !isAccepted(sol, "B") {
		t.Fatalf("应拒绝 A 接受 B, 得到 %+v", sol)
	}
}

func TestSplitReleaseTiming(t *testing.T) {
	// 拆分需 2: A 结束于 9, 拆分完成于 11; 之后两间可分别开会。
	base := []Meeting{
		{ID: "A", Start: 1, End: 9, Rooms: 2, Weight: 5},
		{ID: "B", Start: 11, End: 13, Rooms: 1, Weight: 1},
		{ID: "E", Start: 11, End: 13, Rooms: 1, Weight: 1},
	}
	sol := solveAndValidate(t, Config{NumRooms: 2, MergeTime: 1, SplitTime: 2, Meetings: base})
	if !isAccepted(sol, "B") || !isAccepted(sol, "E") {
		t.Fatalf("拆分完成后两间应可分别安排, 得到 %+v", sol)
	}
	// 拆分未完成时, 合并块只能容纳一场会议, C/D 必拒一场。
	tight := []Meeting{
		{ID: "A", Start: 1, End: 9, Rooms: 2, Weight: 5},
		{ID: "C", Start: 10, End: 12, Rooms: 1, Weight: 1},
		{ID: "D", Start: 10, End: 12, Rooms: 1, Weight: 1},
	}
	sol = solveAndValidate(t, Config{NumRooms: 2, MergeTime: 1, SplitTime: 2, Meetings: tight})
	if sol.RejWeight != 1 || !isAccepted(sol, "A") {
		t.Fatalf("应接受 A 并拒绝 C/D 之一, 得到 %+v", sol)
	}
}

func TestRejectLowestWeight(t *testing.T) {
	// 单房间: 拒绝一个 A(2) 可接纳 B+C+D(3)。
	cfg := Config{NumRooms: 1, MergeTime: 0, SplitTime: 0, Meetings: []Meeting{
		{ID: "A", Start: 0, End: 10, Rooms: 1, Weight: 2},
		{ID: "B", Start: 2, End: 4, Rooms: 1, Weight: 1},
		{ID: "C", Start: 4, End: 6, Rooms: 1, Weight: 1},
		{ID: "D", Start: 6, End: 8, Rooms: 1, Weight: 1},
	}}
	sol := solveAndValidate(t, cfg)
	if sol.RejWeight != 2 || isAccepted(sol, "A") {
		t.Fatalf("应拒绝 A, 得到 %+v", sol)
	}
}

func TestSecondaryObjectiveMinOps(t *testing.T) {
	// 两场会议同用同一合并块: 1 次合并即可, 不应多拆合。
	cfg := Config{NumRooms: 3, MergeTime: 1, SplitTime: 1, Meetings: []Meeting{
		{ID: "A", Start: 1, End: 6, Rooms: 2, Weight: 5},
		{ID: "B", Start: 6, End: 11, Rooms: 2, Weight: 5},
	}}
	sol := solveAndValidate(t, cfg)
	if sol.RejWeight != 0 || sol.Ops != 1 {
		t.Fatalf("期望全接受且仅 1 次合并, 得到 拒绝=%d 操作=%d", sol.RejWeight, sol.Ops)
	}
}

func TestValidatorDetectsSeparateAssignmentWhileMerged(t *testing.T) {
	cfg := Config{NumRooms: 2, MergeTime: 1, SplitTime: 1, Meetings: []Meeting{
		{ID: "A", Start: 0, End: 5, Rooms: 1, Weight: 1},
		{ID: "B", Start: 0, End: 5, Rooms: 1, Weight: 1},
	}}
	sol := Solution{
		Assigns: []Assignment{
			{Meeting: cfg.Meetings[0], Lo: 0, Hi: 0},
			{Meeting: cfg.Meetings[1], Lo: 1, Hi: 1},
		},
		RejWeight: 0,
		Ops:       1,
	}
	// 伪造时间线: 两房处于同一合并块 [1,2], 却分别开不同会议。
	tl := [][]Segment{
		{{Start: 0, End: 5, State: StMeeting, MeetingID: "A", BlockLo: 0, BlockHi: 1}},
		{{Start: 0, End: 5, State: StMeeting, MeetingID: "B", BlockLo: 0, BlockHi: 1}},
	}
	v := Validate(cfg, sol, tl)
	if len(v) == 0 {
		t.Fatal("合并期间分别分配应被校验发现")
	}
}

func TestSixteenMeetings(t *testing.T) {
	var ms []Meeting
	for i := 0; i < 16; i++ {
		ms = append(ms, Meeting{
			ID:     fmt.Sprintf("M%02d", i),
			Start:  (i * 3) % 14,
			End:    (i*3)%14 + 2 + i%3,
			Rooms:  1 + i%4,
			Weight: 1 + (i*7)%9,
		})
	}
	cfg := Config{NumRooms: 4, MergeTime: 1, SplitTime: 1, Meetings: ms}
	sol := solveAndValidate(t, cfg)
	total := 0
	for _, m := range ms {
		total += m.Weight
	}
	if sol.RejWeight < 0 || sol.RejWeight > total {
		t.Fatalf("拒绝权重越界: %d", sol.RejWeight)
	}
}
