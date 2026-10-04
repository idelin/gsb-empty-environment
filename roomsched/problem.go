package main

import "fmt"

const (
	maxRooms    = 10
	maxMeetings = 16
)

// Room 一间物理房间，房间按索引顺序排成一行，仅相邻房间可合并。
type Room struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Meeting 一场会议：[Start, End) 半开区间，Size 为所需容量，Weight 为拒绝代价。
type Meeting struct {
	ID     string `json:"id"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Size   int    `json:"size"`
	Weight int    `json:"weight"`
}

// Problem 一次排程问题。Prep 为一次合并或拆分（恢复）所需的准备时间。
type Problem struct {
	Prep     int       `json:"prep"`
	Rooms    []Room    `json:"rooms"`
	Meetings []Meeting `json:"meetings"`
}

func (p *Problem) Validate() error {
	if p.Prep < 0 {
		return fmt.Errorf("prep 不能为负")
	}
	if len(p.Rooms) == 0 || len(p.Rooms) > maxRooms {
		return fmt.Errorf("房间数量须在 1..%d 之间，当前 %d", maxRooms, len(p.Rooms))
	}
	if len(p.Meetings) > maxMeetings {
		return fmt.Errorf("会议数量最多 %d 场，当前 %d", maxMeetings, len(p.Meetings))
	}
	names := map[string]bool{}
	for _, r := range p.Rooms {
		if r.Name == "" || names[r.Name] {
			return fmt.Errorf("房间名称为空或重复: %q", r.Name)
		}
		names[r.Name] = true
		if r.Capacity < 1 {
			return fmt.Errorf("房间 %s 容量必须为正", r.Name)
		}
	}
	ids := map[string]bool{}
	for _, m := range p.Meetings {
		if m.ID == "" || ids[m.ID] {
			return fmt.Errorf("会议 ID 为空或重复: %q", m.ID)
		}
		ids[m.ID] = true
		if m.Start >= m.End {
			return fmt.Errorf("会议 %s 起止时间非法: [%d,%d)", m.ID, m.Start, m.End)
		}
		if m.Size < 1 {
			return fmt.Errorf("会议 %s 人数必须为正", m.ID)
		}
		if m.Weight < 0 {
			return fmt.Errorf("会议 %s 权重不能为负", m.ID)
		}
	}
	return nil
}

// Assignment 一场被接受的会议及其占用的连续房间块 [Lo, Hi]。
type Assignment struct {
	Meeting Meeting
	Lo, Hi  int
}

// Op 一次拆合操作：在 [Start, End) 内对 Boundary 与 Boundary+1 两房间执行
// 合并（Merge=true）或拆分恢复（Merge=false），为会议 MeetingID 做准备。
type Op struct {
	Boundary  int
	Merge     bool
	Start     int
	End       int
	MeetingID string
}

// Solution 求解结果。
type Solution struct {
	Accepted       []Assignment
	Rejected       []Meeting
	Ops            []Op
	RejectedWeight int
	TotalOps       int
}
