package timeparse

import (
	"fmt"
	"time"
)

// Parse 按站点规则把 text 解析为 UTC 候选时刻。
//
// 参数：
//   - text：页面原文中的日期时间文本；
//   - fetchedAt：抓取时刻（任意时区，内部会转到规则时区的本地日历）；
//   - rules：站点规则，至少要提供 IANA 时区与回看窗口。
//
// 相对日期使用 AddDate 做日历加减；缺年份月日只接受回看窗口内的候选年份；
// DST 重复时刻产生两个候选，不存在时刻返回包装了 ErrNonexistentTime 的错误。
// 任何情况下，已识别的片段与推导依据都会挂在返回的 Result 上。
func Parse(text string, fetchedAt time.Time, rules Rules) (*Result, error) {
	res := &Result{}
	trace := func(step, detail string) {
		res.Traces = append(res.Traces, Trace{Step: step, Detail: detail})
	}

	if rules.Location == nil {
		return res, fmt.Errorf("%w: missing IANA location", ErrInvalidRules)
	}
	if rules.Lookback <= 0 {
		return res, fmt.Errorf("%w: lookback window must be positive", ErrInvalidRules)
	}

	localNow := fetchedAt.In(rules.Location)
	trace("anchor", fmt.Sprintf(
		"抓取时刻 %s 按站点时区 %s 归一为本地时间 %s",
		fetchedAt.UTC().Format(time.RFC3339),
		rules.Location.String(),
		localNow.Format("2006-01-02 15:04:05 -07:00")))

	fragments := extract(text, rules)
	res.Fragments = fragments
	if len(fragments) == 0 {
		trace("extract", "未命中任何日期/时间片段")
		return res, fmt.Errorf("%w: %q", ErrNoDate, text)
	}
	trace("extract", fmt.Sprintf("命中 %d 个片段：%s", len(fragments), summarize(fragments)))

	var dateFrags []Fragment
	var timeFrags []Fragment
	for _, frag := range fragments {
		if frag.Kind == KindTime {
			timeFrags = append(timeFrags, frag)
			continue
		}
		dateFrags = append(dateFrags, frag)
	}
	if len(timeFrags) > 1 {
		trace("select", "存在多个时钟片段，按起始位置取第一个")
	}

	// 瞬间词（“刚刚”）直接锚定抓取瞬间，不再合并时钟。
	for _, frag := range dateFrags {
		if frag.Kind == KindRelative && frag.Instant {
			if len(dateFrags) > 1 {
				return res, fmt.Errorf("%w: %q 同时含瞬间词与其它日期",
					ErrMultipleDateExpressions, text)
			}
			zone, offset := localNow.Zone()
			res.Candidates = []Candidate{{
				UTC:       localNow.UTC(),
				Zone:      zone,
				OffsetSec: offset,
				IsDST:     isDST(localNow),
			}}
			trace("instant", fmt.Sprintf("片段 %q 命中瞬间词，候选即抓取时刻", frag.Text))
			return res, nil
		}
	}

	if len(dateFrags) == 0 {
		// 只有时钟：锚定抓取日的本地日历日期。
		trace("select", "无日期片段，时钟锚定抓取日本地日期")
		dateFrags = []Fragment{{
			Kind:  KindFullDate,
			Year:  localNow.Year(),
			Month: int(localNow.Month()),
			Day:   localNow.Day(),
		}}
	} else if len(dateFrags) > 1 {
		trace("select", fmt.Sprintf("存在 %d 个互斥日期片段：%s",
			len(dateFrags), summarize(dateFrags)))
		return res, fmt.Errorf("%w: %q 命中多个日期片段",
			ErrMultipleDateExpressions, text)
	}

	dateFrag := dateFrags[0]
	clock, clockSource := selectClock(timeFrags, rules)
	trace("clock", clockSource)

	dates, err := resolveDates(dateFrag, localNow, rules, clock, trace)
	if err != nil {
		return res, err
	}

	windowStart := localNow.Add(-rules.Lookback)
	type civil struct{ year, month, day int }
	seenDates := map[civil]bool{}

	for _, d := range dates {
		key := civil{d.year, d.month, d.day}
		if seenDates[key] {
			continue
		}
		seenDates[key] = true

		cands, err := resolveLocal(rules.Location, d.year, d.month, d.day,
			clock.Hour, clock.Minute, clock.Second)
		if err != nil {
			trace("dst", err.Error())
			return res, err
		}

		// 缺年份月日必须落在回看窗口内；带年份日期与相对日期不做窗口裁剪。
		if dateFrag.Kind == KindMonthDay {
			kept := cands[:0]
			for _, cand := range cands {
				instant := cand.UTC
				if instant.Before(windowStart.UTC()) || instant.After(localNow.UTC()) {
					trace("window", fmt.Sprintf(
						"候选 %d-%02d-%02d (%s) 落在回看窗口 [%s, %s] 之外，丢弃",
						d.year, d.month, d.day, instant.Format(time.RFC3339),
						windowStart.UTC().Format(time.RFC3339),
						localNow.UTC().Format(time.RFC3339)))
					continue
				}
				trace("window", fmt.Sprintf(
					"候选 %d-%02d-%02d (%s) 落在回看窗口内，保留",
					d.year, d.month, d.day, instant.Format(time.RFC3339)))
				kept = append(kept, cand)
			}
			cands = kept
		}
		res.Candidates = append(res.Candidates, cands...)
	}

	if dateFrag.Kind == KindMonthDay && len(res.Candidates) == 0 {
		return res, fmt.Errorf("%w: 月日 %02d-%02d 在窗口 %s 内无候选年份",
			ErrOutOfLookback, dateFrag.Month, dateFrag.Day, rules.Lookback)
	}

	sortCandidates(res.Candidates)
	res.Candidates = dedupeCandidates(res.Candidates)
	res.Ambiguous = len(res.Candidates) > 1
	if res.Ambiguous {
		trace("result", fmt.Sprintf("共 %d 个 UTC 候选，标记为歧义", len(res.Candidates)))
	} else {
		trace("result", fmt.Sprintf("唯一 UTC 候选 %s", res.First().Format(time.RFC3339)))
	}
	return res, nil
}

type civilDate struct {
	year, month, day int
}

// resolveDates 把日期片段展开为一个或多个本地日历日期。
func resolveDates(
	frag Fragment,
	localNow time.Time,
	rules Rules,
	clock Clock,
	trace func(step, detail string),
) ([]civilDate, error) {
	switch frag.Kind {
	case KindRelative:
		// 日历加减：AddDate 按本地日历处理月末与跨年，绝不使用固定 24 小时。
		base := time.Date(localNow.Year(), localNow.Month(), localNow.Day(),
			0, 0, 0, 0, rules.Location)
		target := base.AddDate(0, 0, frag.Offset)
		trace("relative", fmt.Sprintf(
			"片段 %q 偏移 %d 天，本地日历 %s AddDate(0,0,%d) = %s",
			frag.Text, frag.Offset, base.Format("2006-01-02"),
			frag.Offset, target.Format("2006-01-02")))
		return []civilDate{{target.Year(), int(target.Month()), target.Day()}}, nil

	case KindFullDate:
		if !validCalendar(frag.Year, frag.Month, frag.Day) {
			return nil, fmt.Errorf("%w: %04d-%02d-%02d",
				ErrInvalidDate, frag.Year, frag.Month, frag.Day)
		}
		trace("full_date", fmt.Sprintf("片段 %q 带完整年份 %d-%02d-%02d",
			frag.Text, frag.Year, frag.Month, frag.Day))
		return []civilDate{{frag.Year, frag.Month, frag.Day}}, nil

	case KindMonthDay:
		return resolveMonthDay(frag, localNow, rules, trace), nil

	default:
		return nil, fmt.Errorf("%w: 非日期片段 %q", ErrInvalidDate, frag.Text)
	}
}

// resolveMonthDay 枚举缺年份月日的候选年份：从抓取日当年起向前取
// MaxYearlessYears 个年份；非法日历日期（如非闰年 2/29）直接跳过。
// 窗口过滤在候选时刻构造完成后统一执行。
func resolveMonthDay(
	frag Fragment,
	localNow time.Time,
	rules Rules,
	trace func(step, detail string),
) []civilDate {
	var dates []civilDate
	limit := rules.maxYearlessYears()
	for delta := 0; delta < limit; delta++ {
		year := localNow.Year() - delta
		if !validCalendar(year, frag.Month, frag.Day) {
			trace("yearless", fmt.Sprintf(
				"候选年份 %d 的 %02d-%02d 不是合法日历日期，跳过",
				year, frag.Month, frag.Day))
			continue
		}
		trace("yearless", fmt.Sprintf(
			"月日 %02d-%02d 补全年份 %d（候选 %d/%d）",
			frag.Month, frag.Day, year, delta+1, limit))
		dates = append(dates, civilDate{year, frag.Month, frag.Day})
	}
	return dates
}

func selectClock(timeFrags []Fragment, rules Rules) (Clock, string) {
	if len(timeFrags) > 0 {
		frag := timeFrags[0]
		return Clock{frag.Hour, frag.Minute, frag.Second},
			fmt.Sprintf("采用时钟片段 %q => %02d:%02d:%02d",
				frag.Text, frag.Hour, frag.Minute, frag.Second)
	}
	c := rules.DefaultClock
	return c, fmt.Sprintf("无时钟片段，采用默认时钟 %02d:%02d:%02d",
		c.Hour, c.Minute, c.Second)
}

// validCalendar 用 Go 自身的日历归一化判定日期合法性，
// 自动覆盖大小月与闰年 2 月 29 日。
func validCalendar(year, month, day int) bool {
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}

func dedupeCandidates(candidates []Candidate) []Candidate {
	seen := map[int64]bool{}
	out := candidates[:0]
	for _, cand := range candidates {
		unix := cand.UTC.Unix()
		if seen[unix] {
			continue
		}
		seen[unix] = true
		out = append(out, cand)
	}
	return out
}

func summarize(fragments []Fragment) string {
	out := ""
	for i, frag := range fragments {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("[%s]%q@%d:%d", frag.Kind, frag.Text, frag.Start, frag.End)
	}
	return out
}
