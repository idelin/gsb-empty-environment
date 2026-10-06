package main

// Event 时间线事件；同一时刻可有多个事件（同刻关系必须保持）。
type Event struct {
	T     Rat    `json:"t"`
	Track string `json:"track"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Protected 受保护区间（如 mouse-drag 鼠标拖动、audio-continuous 音频连续段），不可压缩。
type Protected struct {
	Start Rat    `json:"start"`
	End   Rat    `json:"end"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Track 同步媒体轨：有理起点 + 有理采样间隔。
type Track struct {
	ID       string `json:"id"`
	Start    Rat    `json:"start"`
	Interval Rat    `json:"interval"`
}

// Config 压缩配置。
type Config struct {
	MinIdle Rat `json:"min_idle"` // 空闲段超过该阈值才可压缩
	Ratio   Rat `json:"ratio"`    // 目标时长 = max(min_keep, 原长*ratio)，须属于 (0,1]
	MinKeep Rat `json:"min_keep"` // 压缩后时长下限，须 > 0 以保证斜率为正、映射可逆
}

// Spec 输入规格。
type Spec struct {
	Config     Config      `json:"config"`
	Events     []Event     `json:"events"`
	Protected  []Protected `json:"protected"`
	Tracks     []Track     `json:"tracks"`
	SyncPoints []Rat       `json:"sync_points"`
}
