package main

import (
	"strings"
	"testing"
)

func solveAndVerify(t *testing.T, p *Problem) *Solution {
	t.Helper()
	if err := p.Validate(); err != nil {
		t.Fatalf("输入不合法: %v", err)
	}
	sol := Solve(p)
	if err := Verify(p, sol); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	return sol
}

func TestDemoOptimal(t *testing.T) {
	sol := solveAndVerify(t, demoProblem())
	// 最优：9 点前空闲时合并 B+C 供 m2/m4 复用，仅拒绝 m3,m5,m6
	if sol.RejectedWeight != 4 {
		t.Errorf("拒绝权重 = %d, 期望 4", sol.RejectedWeight)
	}
	if sol.TotalOps != 4 {
		t.Errorf("拆合次数 = %d, 期望 4", sol.TotalOps)
	}
	if len(sol.Accepted) != 7 {
		t.Errorf("接受会议数 = %d, 期望 7", len(sol.Accepted))
	}
}

func TestSingleRoomOverlap(t *testing.T) {
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 10}},
		Meetings: []Meeting{
			{ID: "x", Start: 0, End: 5, Size: 2, Weight: 5},
			{ID: "y", Start: 3, End: 8, Size: 2, Weight: 3},
		},
	}
	sol := solveAndVerify(t, p)
	if sol.RejectedWeight != 3 || len(sol.Accepted) != 1 || sol.Accepted[0].Meeting.ID != "x" {
		t.Errorf("应拒绝权重较小的 y: %+v", sol)
	}
}

func TestMergeNeeded(t *testing.T) {
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 2}, {Name: "B", Capacity: 2}},
		Meetings: []Meeting{
			{ID: "big", Start: 5, End: 6, Size: 3, Weight: 7},
		},
	}
	sol := solveAndVerify(t, p)
	if len(sol.Accepted) != 1 || sol.TotalOps != 1 {
		t.Errorf("大会应通过一次合并被接受: %+v", sol)
	}
	if sol.Accepted[0].Lo != 0 || sol.Accepted[0].Hi != 1 {
		t.Errorf("应占用合并块 A+B: %+v", sol.Accepted[0])
	}
}

func TestPrepGapBlocksSplit(t *testing.T) {
	// m1 占用合并块 A+B 直到 10；m2、m3 紧随其后的两场小会只有一场能
	// 复用合并块，另一场需单房，拆分准备 [9,10) 与 m1 冲突，只能拒绝。
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 2}, {Name: "B", Capacity: 2}},
		Meetings: []Meeting{
			{ID: "m1", Start: 0, End: 10, Size: 4, Weight: 10},
			{ID: "m2", Start: 10, End: 11, Size: 1, Weight: 1},
			{ID: "m3", Start: 10, End: 11, Size: 1, Weight: 1},
		},
	}
	sol := solveAndVerify(t, p)
	if sol.RejectedWeight != 1 || len(sol.Accepted) != 2 {
		t.Errorf("应拒绝 m2/m3 之一: %+v", sol)
	}
}

func TestReuseMergedBlockNoExtraOps(t *testing.T) {
	// 两场连续大会复用同一合并块：只需一次合并，无需拆合。
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 2}, {Name: "B", Capacity: 2}},
		Meetings: []Meeting{
			{ID: "g1", Start: 0, End: 5, Size: 3, Weight: 1},
			{ID: "g2", Start: 5, End: 9, Size: 4, Weight: 1},
		},
	}
	sol := solveAndVerify(t, p)
	if sol.RejectedWeight != 0 || sol.TotalOps != 1 {
		t.Errorf("应接受两场且仅合并一次: %+v", sol)
	}
}

func TestVerifyRejectsMergeExclusivityViolation(t *testing.T) {
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 2}, {Name: "B", Capacity: 2}},
		Meetings: []Meeting{
			{ID: "big", Start: 0, End: 10, Size: 3, Weight: 5},
			{ID: "small", Start: 4, End: 6, Size: 1, Weight: 1},
		},
	}
	// 手工构造非法解：big 合并 A+B 期间，small 又单独占用 A。
	sol := &Solution{
		Accepted: []Assignment{
			{Meeting: p.Meetings[0], Lo: 0, Hi: 1},
			{Meeting: p.Meetings[1], Lo: 0, Hi: 0},
		},
		Ops: []Op{
			{Boundary: 0, Merge: true, Start: -1, End: 0, MeetingID: "big"},
			{Boundary: 0, Merge: false, Start: 3, End: 4, MeetingID: "small"},
		},
		TotalOps: 2,
	}
	err := Verify(p, sol)
	if err == nil || !strings.Contains(err.Error(), "合并排他性违反") {
		t.Fatalf("应检出合并排他性违反, 得到: %v", err)
	}
}

func TestVerifyRejectsRoomOverlap(t *testing.T) {
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 4}},
		Meetings: []Meeting{
			{ID: "a", Start: 0, End: 5, Size: 1, Weight: 1},
			{ID: "b", Start: 4, End: 8, Size: 1, Weight: 1},
		},
	}
	sol := &Solution{
		Accepted: []Assignment{
			{Meeting: p.Meetings[0], Lo: 0, Hi: 0},
			{Meeting: p.Meetings[1], Lo: 0, Hi: 0},
		},
	}
	if err := Verify(p, sol); err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("应检出房间时间线重叠, 得到: %v", err)
	}
}

func TestVerifyRejectsMissingPrep(t *testing.T) {
	// 合并会议缺少对应的合并操作记录。
	p := &Problem{
		Prep:  2,
		Rooms: []Room{{Name: "A", Capacity: 2}, {Name: "B", Capacity: 2}},
		Meetings: []Meeting{
			{ID: "big", Start: 5, End: 8, Size: 3, Weight: 5},
		},
	}
	sol := &Solution{
		Accepted: []Assignment{{Meeting: p.Meetings[0], Lo: 0, Hi: 1}},
	}
	if err := Verify(p, sol); err == nil || !strings.Contains(err.Error(), "缺少拆合操作") {
		t.Fatalf("应检出缺少拆合操作, 得到: %v", err)
	}
}

func TestSixteenMeetings(t *testing.T) {
	p := &Problem{
		Prep:  1,
		Rooms: []Room{{Name: "A", Capacity: 3}, {Name: "B", Capacity: 3}, {Name: "C", Capacity: 3}},
	}
	for i := 0; i < 16; i++ {
		p.Meetings = append(p.Meetings, Meeting{
			ID:     string(rune('a' + i)),
			Start:  i / 4 * 3,
			End:    i/4*3 + 2,
			Size:   2 + i%5,
			Weight: 1 + i%4,
		})
	}
	sol := solveAndVerify(t, p)
	if len(sol.Accepted)+len(sol.Rejected) != 16 {
		t.Errorf("会议账目不符: %+v", sol)
	}
}
