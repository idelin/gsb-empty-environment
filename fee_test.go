package main

import (
	"testing"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSegmentFeeFreeAndRounding(t *testing.T) {
	rule := FeeRule{Free: 10 * time.Minute, Unit: 30 * time.Minute, RateCents: 300}
	start := mustTime("2026-10-01T08:00:00+08:00")
	cases := []struct {
		minutes int
		want    int64
	}{
		{8, 0},    // 免费时长内
		{10, 0},   // 恰好等于免费时长
		{11, 300}, // 超出 1 分钟, 进位为 1 单位
		{40, 300}, // 计费 30 分钟, 1 单位
		{41, 600}, // 进位为 2 单位
	}
	for _, c := range cases {
		got := rule.SegmentFee(start, start.Add(time.Duration(c.minutes)*time.Minute))
		if got != c.want {
			t.Errorf("时长 %d 分钟: 得到 %d, 期望 %d", c.minutes, got, c.want)
		}
	}
}

func TestSegmentFeeDailyCap(t *testing.T) {
	rule := FeeRule{Unit: time.Hour, RateCents: 300, DailyCapCents: 1000}
	// 同一天 5 小时: 原始 1500, 封顶 1000
	got := rule.SegmentFee(mustTime("2026-10-01T08:00:00+08:00"), mustTime("2026-10-01T13:00:00+08:00"))
	if got != 1000 {
		t.Errorf("单日封顶: 得到 %d, 期望 1000", got)
	}
	// 跨午夜 12 小时: 原始 3600, 2 个自然日, 封顶 2000
	got = rule.SegmentFee(mustTime("2026-10-01T20:00:00+08:00"), mustTime("2026-10-02T08:00:00+08:00"))
	if got != 2000 {
		t.Errorf("跨日封顶: 得到 %d, 期望 2000", got)
	}
	// 不封顶
	rule.DailyCapCents = 0
	got = rule.SegmentFee(mustTime("2026-10-01T08:00:00+08:00"), mustTime("2026-10-01T13:00:00+08:00"))
	if got != 1500 {
		t.Errorf("不封顶: 得到 %d, 期望 1500", got)
	}
}

func TestDaysSpanned(t *testing.T) {
	if d := daysSpanned(mustTime("2026-10-01T08:00:00+08:00"), mustTime("2026-10-01T20:00:00+08:00")); d != 1 {
		t.Errorf("同一天: 得到 %d, 期望 1", d)
	}
	if d := daysSpanned(mustTime("2026-10-01T23:00:00+08:00"), mustTime("2026-10-02T01:00:00+08:00")); d != 2 {
		t.Errorf("跨午夜: 得到 %d, 期望 2", d)
	}
}

func TestParseYuan(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"2.5", 250},
		{"10", 1000},
		{"0.01", 1},
		{"0.005", 1}, // 四舍五入
		{"0.004", 0},
	}
	for _, c := range cases {
		got, err := parseYuan(c.in)
		if err != nil {
			t.Errorf("%q: 意外错误 %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: 得到 %d, 期望 %d", c.in, got, c.want)
		}
	}
	if _, err := parseYuan("abc"); err == nil {
		t.Error("无效金额应报错")
	}
}
