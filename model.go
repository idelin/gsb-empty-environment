package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// EventType 事件类型: 进场(in) 或 出场(out)。
type EventType string

const (
	EventIn  EventType = "in"
	EventOut EventType = "out"
)

// Event 一条进出场事件。
type Event struct {
	Time time.Time
	Type EventType
}

type eventJSON struct {
	Time string `json:"time"`
	Type string `json:"type"`
}

// ParseEvents 解析事件 JSON 数组, 按时间排序并校验。
// 事件时间精度为秒, 且任意两条事件时间不得相同。
func ParseEvents(data []byte) ([]Event, error) {
	var raw []eventJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("事件 JSON 解析失败: %w", err)
	}
	events := make([]Event, 0, len(raw))
	for i, r := range raw {
		t, err := time.Parse(time.RFC3339, r.Time)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条事件时间格式无效 %q (应为 RFC3339): %w", i+1, r.Time, err)
		}
		typ := EventType(r.Type)
		if typ != EventIn && typ != EventOut {
			return nil, fmt.Errorf("第 %d 条事件类型无效 %q (应为 in 或 out)", i+1, r.Type)
		}
		events = append(events, Event{Time: t, Type: typ})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	for i := 1; i < len(events); i++ {
		if events[i].Time.Equal(events[i-1].Time) {
			return nil, fmt.Errorf("存在时间相同的两条事件: %s", events[i].Time.Format(time.RFC3339))
		}
	}
	return events, nil
}
