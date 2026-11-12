package timeparse

import (
	"errors"
	"testing"
	"time"

	_ "time/tzdata"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

func utc(year, month, day, hour, min, sec int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
}

func expectErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("期望错误 %v，实际 %v", target, err)
	}
}

// 跨年：抓取时刻在 1 月初，“昨日”必须回到上一年的 12 月 31 日。
func TestCrossYearRelativeYesterday(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 1, 10, 0, 0, 0, shanghai)

	res, err := Parse("发布于昨日 09:30", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 720 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if res.Ambiguous || len(res.Candidates) != 1 {
		t.Fatalf("期望唯一候选，实际 ambiguous=%v candidates=%d",
			res.Ambiguous, len(res.Candidates))
	}
	want := utc(2010, 12, 31, 1, 30, 0)
	if !res.First().Equal(want) {
		t.Fatalf("期望 %s，实际 %s", want, res.First())
	}
}

// 跨年：无年份的“12-31”在 30 天回看窗口内只能补全为上一年。
func TestCrossYearMonthDaySingleCandidate(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 5, 12, 0, 0, 0, shanghai)

	res, err := Parse("更新于 12-31", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if res.Ambiguous || len(res.Candidates) != 1 {
		t.Fatalf("期望唯一候选，实际 ambiguous=%v candidates=%d",
			res.Ambiguous, len(res.Candidates))
	}
	// 上海本地 12-31 午夜 00:00（UTC+8）= UTC 前一日 16:00。
	want := utc(2010, 12, 30, 16, 0, 0)
	if !res.First().Equal(want) {
		t.Fatalf("期望 %s，实际 %s", want, res.First())
	}
}

// 缺年份月日在回看窗口内同时匹配两个年份 => 标记歧义并返回两个候选。
func TestMonthDayTwoCandidatesAmbiguous(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 10, 12, 0, 0, 0, shanghai)

	res, err := Parse("归档日期 12-15", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 400 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if !res.Ambiguous || len(res.Candidates) != 2 {
		t.Fatalf("期望两个歧义候选，实际 ambiguous=%v candidates=%d",
			res.Ambiguous, len(res.Candidates))
	}
	want := []time.Time{
		utc(2009, 12, 14, 16, 0, 0),
		utc(2010, 12, 14, 16, 0, 0),
	}
	for i, cand := range res.Candidates {
		if !cand.UTC.Equal(want[i]) {
			t.Fatalf("候选 %d 期望 %s，实际 %s", i, want[i], cand.UTC)
		}
	}
}

// 回看窗口太短，所有候选年份都落在窗口外 => ErrOutOfLookback。
func TestMonthDayOutOfLookback(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 5, 12, 0, 0, 0, shanghai)

	_, err := Parse("更新于 11-01", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 24 * time.Hour,
	})
	expectErrorIs(t, err, ErrOutOfLookback)
}

// 闰日：2012 年合法；2011 年 2 月 29 日不是合法日期，必须报错而不是归一到 3/1。
func TestLeapDayFullDate(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2012, time.March, 1, 8, 0, 0, 0, shanghai)

	res, err := Parse("2012-02-29 08:00", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 365 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("闰年 2/29 应合法，实际错误: %v", err)
	}
	want := utc(2012, 2, 29, 0, 0, 0)
	if !res.First().Equal(want) {
		t.Fatalf("期望 %s，实际 %s", want, res.First())
	}

	_, err = Parse("2011-02-29", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 365 * 24 * time.Hour,
	})
	expectErrorIs(t, err, ErrInvalidDate)
}

// 闰日缺年份：非闰年候选被跳过，窗口内只剩闰年候选，不产生歧义。
func TestLeapDayMonthDaySkipsNonLeapYear(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.March, 5, 8, 0, 0, 0, shanghai)

	res, err := Parse("存档 02-29", fetchedAt, Rules{
		Location:         shanghai,
		Lookback:         1200 * 24 * time.Hour,
		MaxYearlessYears: 4,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("期望仅闰年一个候选，实际 %d 个", len(res.Candidates))
	}
	want := utc(2008, 2, 28, 16, 0, 0)
	if !res.First().Equal(want) {
		t.Fatalf("期望 %s，实际 %s", want, res.First())
	}
}

// DST 回退：纽约 2011-11-06 01:30 出现两次，返回两个 UTC 候选并标歧义。
func TestDSTFoldReturnsTwoCandidates(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	fetchedAt := time.Date(2011, time.November, 7, 12, 0, 0, 0, ny)

	res, err := Parse("2011-11-06 01:30", fetchedAt, Rules{
		Location: ny,
		Lookback: 72 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if !res.Ambiguous || len(res.Candidates) != 2 {
		t.Fatalf("期望两个 DST 候选，实际 ambiguous=%v candidates=%d",
			res.Ambiguous, len(res.Candidates))
	}
	want := []time.Time{
		utc(2011, 11, 6, 5, 30, 0), // EDT，UTC-4
		utc(2011, 11, 6, 6, 30, 0), // EST，UTC-5
	}
	for i, cand := range res.Candidates {
		if !cand.UTC.Equal(want[i]) {
			t.Fatalf("候选 %d 期望 %s，实际 %s", i, want[i], cand.UTC)
		}
	}
	if res.Candidates[0].Zone != "EDT" || !res.Candidates[0].IsDST {
		t.Fatalf("首个候选应为夏令时 EDT，实际 %+v", res.Candidates[0])
	}
	if res.Candidates[1].Zone != "EST" || res.Candidates[1].IsDST {
		t.Fatalf("次候选应为标准时 EST，实际 %+v", res.Candidates[1])
	}
}

// DST 跳时：纽约 2011-03-13 02:30 不存在，必须拒绝。
func TestDSTGapRejected(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	fetchedAt := time.Date(2011, time.March, 14, 12, 0, 0, 0, ny)

	_, err := Parse("2011-03-13 02:30", fetchedAt, Rules{
		Location: ny,
		Lookback: 72 * time.Hour,
	})
	expectErrorIs(t, err, ErrNonexistentTime)
}

// 普通 DST 时刻只返回一个候选，且携带正确的偏移与夏令时标记。
func TestDSTNormalTimeSingleCandidate(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	fetchedAt := time.Date(2011, time.July, 2, 12, 0, 0, 0, ny)

	res, err := Parse("2011-07-01 12:00", fetchedAt, Rules{
		Location: ny,
		Lookback: 72 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if len(res.Candidates) != 1 || res.Ambiguous {
		t.Fatalf("期望唯一候选，实际 %+v", res.Candidates)
	}
	cand := res.Candidates[0]
	want := utc(2011, 7, 1, 16, 0, 0)
	if !cand.UTC.Equal(want) || cand.OffsetSec != -4*3600 || !cand.IsDST {
		t.Fatalf("候选信息异常: %+v", cand)
	}
}

// Lord Howe 岛使用 30 分钟级 DST 过渡，验证采样方案不依赖固定 60 分钟差值。
func TestDSTHalfHourFoldLordHowe(t *testing.T) {
	lh := mustLoc(t, "Australia/Lord_Howe")
	fetchedAt := time.Date(2025, time.April, 7, 12, 0, 0, 0, lh)

	res, err := Parse("2025-04-06 01:45", fetchedAt, Rules{
		Location: lh,
		Lookback: 72 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("期望两个候选，实际 %d 个: %+v", len(res.Candidates), res.Candidates)
	}
	// 夏令时 +11:00 与标准时 +10:30 各对应一个 UTC 瞬间，相差 30 分钟。
	gap := res.Candidates[1].UTC.Sub(res.Candidates[0].UTC)
	if gap != 30*time.Minute {
		t.Fatalf("两候选应相差 30 分钟，实际 %s", gap)
	}
}

// 相对日期必须走日历：DST 跳时前后“昨日”仍然锚定本地日历日期，
// 不能用固定 24 小时平移。
func TestRelativeUsesCalendarNotFixed24Hours(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	fetchedAt := time.Date(2011, time.March, 12, 12, 0, 0, 0, ny)

	res, err := Parse("昨日 12:00", fetchedAt, Rules{
		Location: ny,
		Lookback: 48 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	want := utc(2011, 3, 11, 17, 0, 0) // EST UTC-5
	if !res.First().Equal(want) {
		t.Fatalf("期望本地日历昨日正午 %s，实际 %s", want, res.First())
	}

	// 3 月 14 日的“昨日”必须落在只有 23 小时的 3 月 13 日。
	fetchedAt = time.Date(2011, time.March, 14, 9, 0, 0, 0, ny)
	res, err = Parse("昨日 09:00", fetchedAt, Rules{
		Location: ny,
		Lookback: 48 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	want = utc(2011, 3, 13, 13, 0, 0) // EDT UTC-4
	if !res.First().Equal(want) {
		t.Fatalf("期望跨 DST 的昨日 09:00 => %s，实际 %s", want, res.First())
	}
}

// “刚刚”锚定抓取瞬间，不与任何时钟合并。
func TestInstantWord(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 1, 10, 15, 30, 0, shanghai)

	res, err := Parse("刚刚发布", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if !res.First().Equal(fetchedAt.UTC()) {
		t.Fatalf("期望抓取瞬间 %s，实际 %s", fetchedAt.UTC(), res.First())
	}
}

// 12 小时制中文时段必须正确换算为 24 小时制。
func TestChinesePeriodClock(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.June, 15, 23, 0, 0, 0, shanghai)

	cases := []struct {
		text string
		want time.Time
	}{
		{"2011-06-15 下午3点30分", utc(2011, 6, 15, 7, 30, 0)},
		{"2011-06-15 上午11点", utc(2011, 6, 15, 3, 0, 0)},
		{"2011-06-15 中午12点10分", utc(2011, 6, 15, 4, 10, 0)},
		// 晚上 12 点按午夜 00:00（当日零点）处理。
		{"2011-06-15 晚上12点", utc(2011, 6, 15, 4, 0, 0)},
	}
	for _, tc := range cases {
		res, err := Parse(tc.text, fetchedAt, Rules{
			Location: shanghai,
			Lookback: 720 * time.Hour,
		})
		if err != nil {
			t.Fatalf("%s 返回错误: %v", tc.text, err)
		}
		if !res.First().Equal(tc.want) {
			t.Fatalf("%s 期望 %s，实际 %s", tc.text, tc.want, res.First())
		}
	}
}

// 片段与推导依据必须保留：覆盖位置、种类、关键字段与 Trace 步骤。
func TestFragmentsAndTracesPreserved(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 5, 12, 0, 0, 0, shanghai)

	res, err := Parse("更新于 12-31 09:30", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	if len(res.Fragments) != 2 {
		t.Fatalf("期望 2 个片段，实际 %d: %+v", len(res.Fragments), res.Fragments)
	}
	var md, clock *Fragment
	for i := range res.Fragments {
		switch res.Fragments[i].Kind {
		case KindMonthDay:
			md = &res.Fragments[i]
		case KindTime:
			clock = &res.Fragments[i]
		}
	}
	if md == nil || md.Month != 12 || md.Day != 31 {
		t.Fatalf("月日片段异常: %+v", res.Fragments)
	}
	if clock == nil || clock.Hour != 9 || clock.Minute != 30 {
		t.Fatalf("时钟片段异常: %+v", res.Fragments)
	}
	full := "更新于 12-31 09:30"
	if md.Start < 0 || md.End > len(full) || md.Start >= md.End ||
		full[md.Start:md.Start+len("12-31")] != "12-31" {
		t.Fatalf("月日片段跨度异常: %+v", md)
	}

	steps := map[string]bool{}
	for _, tr := range res.Traces {
		steps[tr.Step] = true
		if tr.Detail == "" {
			t.Fatalf("Trace 步骤 %s 缺少细节", tr.Step)
		}
	}
	for _, want := range []string{"anchor", "extract", "clock", "yearless", "window", "result"} {
		if !steps[want] {
			t.Fatalf("Trace 缺少步骤 %s，实际 %+v", want, res.Traces)
		}
	}
}

// 多个互斥日期表达式直接报错，但仍保留片段与推导依据。
func TestMultipleDateExpressions(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 5, 12, 0, 0, 0, shanghai)

	res, err := Parse("昨日 2010-12-30", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 720 * time.Hour,
	})
	expectErrorIs(t, err, ErrMultipleDateExpressions)
	if len(res.Fragments) < 2 {
		t.Fatalf("错误路径也应保留片段，实际 %+v", res.Fragments)
	}
}

// 无任何可识别日期时返回 ErrNoDate；规则缺时区或窗口非法时返回 ErrInvalidRules。
func TestNoDateAndInvalidRules(t *testing.T) {
	shanghai := mustLoc(t, "Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 5, 12, 0, 0, 0, shanghai)

	_, err := Parse("暂无更新", fetchedAt, Rules{
		Location: shanghai,
		Lookback: 24 * time.Hour,
	})
	expectErrorIs(t, err, ErrNoDate)

	_, err = Parse("昨日", fetchedAt, Rules{Location: nil, Lookback: time.Hour})
	expectErrorIs(t, err, ErrInvalidRules)

	_, err = Parse("昨日", fetchedAt, Rules{Location: shanghai})
	expectErrorIs(t, err, ErrInvalidRules)
}

// 只有时钟时锚定抓取日本地日期。
func TestTimeOnlyAnchorsToday(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	fetchedAt := time.Date(2011, time.July, 1, 18, 0, 0, 0, ny)

	res, err := Parse("更新于 09:00", fetchedAt, Rules{
		Location: ny,
		Lookback: 48 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Parse 返回错误: %v", err)
	}
	want := utc(2011, 7, 1, 13, 0, 0)
	if !res.First().Equal(want) {
		t.Fatalf("期望当日 09:00 => %s，实际 %s", want, res.First())
	}
}
