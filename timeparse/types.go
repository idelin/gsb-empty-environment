// Package timeparse 面向网页采集宿主，把页面文本里的日期时间表达式解析为
// UTC 候选时刻。
//
// 解析遵循站点本地日历：相对日期（如“昨日”）使用 time.Time.AddDate 做日历
// 加减，而不是按固定 24 小时平移；缺年份的月日只允许落在站点规则给定的回看
// 窗口内，窗口内出现多个候选年份时标记为歧义；夏令时回退产生的重复本地时刻
// 返回两个 UTC 候选，春令时跳跃产生的不存在时刻直接拒绝。
//
// 每次解析都会保留命中的文本片段（Fragment）与逐步推导依据（Trace），
// 便于宿主审计跨年归错、歧义与 DST 边界等问题。
package timeparse

import "time"

// FragmentKind 标识命中片段的种类。
type FragmentKind string

const (
	// KindRelative 相对日期词，如“昨日”“前天”。
	KindRelative FragmentKind = "relative"
	// KindFullDate 带年份的完整日期，如“2010-12-31”“2010年12月31日”。
	KindFullDate FragmentKind = "full_date"
	// KindMonthDay 缺年份的月日，如“12-31”“12月31日”。
	KindMonthDay FragmentKind = "month_day"
	// KindTime 一天内的时钟表达式，如“01:30”“下午3点”。
	KindTime FragmentKind = "time"
)

// Fragment 是文本中命中的一个日期/时间片段及其结构化字段。
//
// Start/End 为在输入文本中的字节偏移（左闭右开），未使用的数值字段为零值。
type Fragment struct {
	Kind   FragmentKind
	Text   string
	Start  int
	End    int
	Year   int
	Month  int
	Day    int
	Hour   int
	Minute int
	Second int
	// Offset 仅 KindRelative 使用，表示相对抓取日本地日历的天数偏移。
	Offset int
	// Instant 为 true 表示该片段表示“抓取瞬间”（如“刚刚”），
	// 不再与时钟片段合并。
	Instant bool
	// Period 是时钟片段携带的中文时段修饰词（上午/下午/晚上等），可为空。
	Period string
}

// Candidate 是一个 UTC 候选时刻及其在站点时区下的观测信息。
type Candidate struct {
	// UTC 候选时刻（UTC 时区）。
	UTC time.Time
	// Zone 是该时刻在站点时区下的时区缩写，如 EST/EDT。
	Zone string
	// OffsetSec 是该时刻相对 UTC 的偏移秒数。
	OffsetSec int
	// IsDST 表示该时刻是否处于夏令时。
	IsDST bool
}

// Trace 记录一条推导依据，便于宿主审计解析过程。
type Trace struct {
	// Step 是推导步骤名。
	Step string
	// Detail 是该步骤的人类可读细节。
	Detail string
}

// Result 是一次解析的产物。即使返回错误，Fragments 与 Traces 也可能已填充。
type Result struct {
	// Candidates 是去重并按时间升序排列的 UTC 候选。
	Candidates []Candidate
	// Ambiguous 在候选多于一个（多个候选年份或 DST 重复时刻）时为 true。
	Ambiguous bool
	// Fragments 是所有命中且未被更长片段覆盖的文本片段。
	Fragments []Fragment
	// Traces 是完整的推导依据。
	Traces []Trace
}

// First 返回最早的 UTC 候选；没有候选时返回零值 time.Time。
func (r *Result) First() time.Time {
	if len(r.Candidates) == 0 {
		return time.Time{}
	}
	return r.Candidates[0].UTC
}
