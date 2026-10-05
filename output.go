package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// describePos 描述补事件在已观测序列中的插入位置。
func describePos(events []Event, pos int) string {
	n := len(events)
	ev := func(i int) string {
		return fmt.Sprintf("%s(%s)", events[i].Time.Format(time.RFC3339), events[i].Type)
	}
	switch {
	case pos == 0:
		return fmt.Sprintf("首条事件 %s 之前", ev(0))
	case pos == n:
		return fmt.Sprintf("末条事件 %s 之后", ev(n-1))
	default:
		return fmt.Sprintf("%s 与 %s 之间", ev(pos-1), ev(pos))
	}
}

func formatTime(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format(time.RFC3339)
}

func formatInterval(iv Interval, loc *time.Location) string {
	from, to := "-∞", "+∞"
	if !iv.FromOpen {
		from = formatTime(iv.From, loc)
	}
	if !iv.ToOpen {
		to = formatTime(iv.To, loc)
	}
	return from + " ~ " + to
}

func printExtremum(w io.Writer, events []Event, label string, e Extremum) {
	if e.Unbounded {
		fmt.Fprintf(w, "  %s: 无界 (补事件时间无界, 费用随时长增长; 可用 -since/-as-of 限定范围)\n", label)
	} else {
		fmt.Fprintf(w, "  %s: %s\n", label, formatCents(e.FeeCents))
	}
	for _, opt := range e.Options {
		fmt.Fprintf(w, "    方案: 补 %s, 位置: %s\n", opt.Insert, describePos(events, opt.Pos))
		for _, iv := range opt.Intervals {
			fmt.Fprintf(w, "      补事件时间: %s\n", formatInterval(iv, opt.loc))
		}
	}
}

func printText(w io.Writer, events []Event, a *Analysis) {
	if a.Complete {
		fmt.Fprintf(w, "记录完整, 无需补事件。费用: %s\n", formatCents(a.FeeCents))
		return
	}
	fmt.Fprintln(w, "记录缺失 1 个事件, 已在最多补 1 个事件的条件下恢复合法停车段。")
	if a.UniqueBill() {
		fmt.Fprintf(w, "费用唯一确定: %s\n", formatCents(a.Min.FeeCents))
		printExtremum(w, events, "对应补事件区间", a.Min)
		return
	}
	fmt.Fprintln(w, "费用无法唯一确定, 给出可能范围(不猜测单一账单):")
	printExtremum(w, events, "最低费用", a.Min)
	printExtremum(w, events, "最高费用", a.Max)
}

// ---- JSON 输出 ----

type intervalJSON struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type optionJSON struct {
	Insert    EventType      `json:"insert"`
	Position  string         `json:"position"`
	Intervals []intervalJSON `json:"intervals"`
}

type extremumJSON struct {
	FeeCents  int64        `json:"feeCents,omitempty"`
	Fee       string       `json:"fee,omitempty"`
	Unbounded bool         `json:"unbounded,omitempty"`
	Options   []optionJSON `json:"options,omitempty"`
}

type outputJSON struct {
	Status     string        `json:"status"` // "complete" 或 "restored"
	UniqueBill bool          `json:"uniqueBill"`
	FeeCents   int64         `json:"feeCents,omitempty"`
	Fee        string        `json:"fee,omitempty"`
	Min        *extremumJSON `json:"min,omitempty"`
	Max        *extremumJSON `json:"max,omitempty"`
}

func toExtremumJSON(events []Event, e Extremum) *extremumJSON {
	out := &extremumJSON{Unbounded: e.Unbounded}
	if !e.Unbounded {
		out.FeeCents = e.FeeCents
		out.Fee = formatCents(e.FeeCents)
	}
	for _, opt := range e.Options {
		oj := optionJSON{Insert: opt.Insert, Position: describePos(events, opt.Pos)}
		for _, iv := range opt.Intervals {
			var ij intervalJSON
			if !iv.FromOpen {
				ij.From = formatTime(iv.From, opt.loc)
			}
			if !iv.ToOpen {
				ij.To = formatTime(iv.To, opt.loc)
			}
			oj.Intervals = append(oj.Intervals, ij)
		}
		out.Options = append(out.Options, oj)
	}
	return out
}

func printJSON(w io.Writer, events []Event, a *Analysis) error {
	out := outputJSON{UniqueBill: a.UniqueBill()}
	if a.Complete {
		out.Status = "complete"
		out.FeeCents = a.FeeCents
		out.Fee = formatCents(a.FeeCents)
	} else {
		out.Status = "restored"
		out.Min = toExtremumJSON(events, a.Min)
		out.Max = toExtremumJSON(events, a.Max)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
