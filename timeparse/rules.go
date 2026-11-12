package timeparse

import "time"

// Clock 是未携带时钟片段时使用的默认时分秒。
type Clock struct {
	Hour   int
	Minute int
	Second int
}

// Rules 描述单个站点的解析规则。
type Rules struct {
	// Location 站点 IANA 时区，必填。所有相对日期与缺年份日期都按此时区
	// 的本地日历推导。
	Location *time.Location

	// Lookback 是缺年份月日允许落入的回看窗口长度，必须为正数。
	// 窗口区间为 [fetchedAt-Lookback, fetchedAt]。
	Lookback time.Duration

	// MaxYearlessYears 是缺年份月日最多向前尝试的候选年份数（含抓取日
	// 当年）。零值时取默认值 3。
	MaxYearlessYears int

	// RelativeWords 自定义相对日期词到天数偏移的映射，如
	// map[string]int{"昨日": -1, "前天": -2}。nil 时使用中文默认表。
	RelativeWords map[string]int

	// InstantWords 自定义“抓取瞬间”词表，命中后候选时刻即抓取时刻，
	// 如“刚刚”“刚才”。nil 时使用中文默认表。
	InstantWords []string

	// DefaultClock 是文本只有日期没有时钟时使用的默认时分秒，
	// 零值即本地午夜 00:00:00。
	DefaultClock Clock
}

// 默认中文相对日期词：偏移量按站点本地日历的天数计。
var defaultRelativeWords = map[string]int{
	"昨日": -1,
	"昨天": -1,
	"前日": -2,
	"前天": -2,
	"今日": 0,
	"今天": 0,
	"今晚": 0,
}

// 默认中文“抓取瞬间”词。
var defaultInstantWords = []string{"刚刚", "刚才"}

func (r Rules) relativeWords() map[string]int {
	if r.RelativeWords != nil {
		return r.RelativeWords
	}
	return defaultRelativeWords
}

func (r Rules) instantWords() []string {
	if r.InstantWords != nil {
		return r.InstantWords
	}
	return defaultInstantWords
}

func (r Rules) maxYearlessYears() int {
	if r.MaxYearlessYears > 0 {
		return r.MaxYearlessYears
	}
	return 3
}
