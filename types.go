package main

import "fmt"

// dayLength 营业时间窗口按天循环。
const dayLength = 24

// Edge 有向边：耗时 Time，消耗整数电量 Energy。
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Time   int    `json:"time"`
	Energy int    `json:"energy"`
}

// Window 每日营业窗口 [Open, Close)，单位与边耗时一致。
type Window struct {
	Open  int `json:"open"`
	Close int `json:"close"`
}

// Station 换电站：支付 Fee 可将电池换为满电，仅营业窗口内可换。
type Station struct {
	Fee     int      `json:"fee"`
	Windows []Window `json:"windows"`
}

// Input 规划输入。
type Input struct {
	Capacity  int                `json:"capacity"`
	Start     string             `json:"start"`
	Goal      string             `json:"goal"`
	StartTime int                `json:"startTime"`
	Edges     []Edge             `json:"edges"`
	Stations  map[string]Station `json:"stations"`
}

func (in *Input) validate() error {
	if in.Capacity <= 0 {
		return fmt.Errorf("capacity 必须为正整数")
	}
	if in.Start == "" || in.Goal == "" {
		return fmt.Errorf("start/goal 不能为空")
	}
	if in.StartTime < 0 {
		return fmt.Errorf("startTime 不能为负")
	}
	for _, e := range in.Edges {
		if e.Time < 0 || e.Energy < 0 {
			return fmt.Errorf("边 %s->%s 的 time/energy 不能为负", e.From, e.To)
		}
	}
	for name, st := range in.Stations {
		if st.Fee < 0 {
			return fmt.Errorf("换电站 %s 的 fee 不能为负", name)
		}
		if len(st.Windows) == 0 {
			return fmt.Errorf("换电站 %s 缺少营业窗口", name)
		}
		for _, w := range st.Windows {
			if w.Open < 0 || w.Close > dayLength || w.Open >= w.Close {
				return fmt.Errorf("换电站 %s 的营业窗口 [%d,%d) 非法", name, w.Open, w.Close)
			}
		}
	}
	return nil
}

// Result 规划结果。
type Result struct {
	Reachable bool
	Arrival   int
	Cost      int
	Steps     []string
}
