package main

import "fmt"

// Meeting 描述一场会议: [Start, End) 期间需要 Rooms 间相邻合并的房间。
type Meeting struct {
	ID     string `json:"id"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Rooms  int    `json:"rooms"`
	Weight int    `json:"weight"`
}

// Config 为求解输入: 一排 NumRooms 个相邻房间, 合并/拆分各占准备时间。
type Config struct {
	NumRooms  int       `json:"numRooms"`
	MergeTime int       `json:"mergeTime"`
	SplitTime int       `json:"splitTime"`
	Meetings  []Meeting `json:"meetings"`
}

func (c Config) check() error {
	if c.NumRooms < 1 || c.NumRooms > 10 {
		return fmt.Errorf("numRooms 须在 1..10 之间, 当前 %d", c.NumRooms)
	}
	if c.MergeTime < 0 || c.SplitTime < 0 {
		return fmt.Errorf("mergeTime/splitTime 不能为负")
	}
	if len(c.Meetings) > 16 {
		return fmt.Errorf("会议数 %d 超过上限 16", len(c.Meetings))
	}
	seen := map[string]bool{}
	for _, m := range c.Meetings {
		if m.ID == "" {
			return fmt.Errorf("会议 ID 不能为空")
		}
		if seen[m.ID] {
			return fmt.Errorf("会议 ID %q 重复", m.ID)
		}
		seen[m.ID] = true
		if m.Start < 0 || m.End <= m.Start {
			return fmt.Errorf("会议 %s 时间区间非法: [%d,%d)", m.ID, m.Start, m.End)
		}
		if m.Rooms < 1 {
			return fmt.Errorf("会议 %s 需求房间数非法: %d", m.ID, m.Rooms)
		}
		if m.Weight < 0 {
			return fmt.Errorf("会议 %s 权重不能为负", m.ID)
		}
	}
	return nil
}
