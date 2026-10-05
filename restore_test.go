package main

import (
	"strings"
	"testing"
	"time"
)

func testRule() FeeRule {
	return FeeRule{Free: 15 * time.Minute, Unit: 30 * time.Minute, RateCents: 200, DailyCapCents: 2000}
}

func ev(s string, typ EventType) Event {
	return Event{Time: mustTime(s), Type: typ}
}

func unix(s string) int64 {
	return mustTime(s).Unix()
}

func TestCompleteRecord(t *testing.T) {
	events := []Event{
		ev("2026-10-01T08:00:00+08:00", EventIn),
		ev("2026-10-01T09:00:00+08:00", EventOut),
	}
	a, err := Analyze(events, testRule(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Complete {
		t.Fatal("应判定为记录完整")
	}
	// 1 小时, 减 15 分钟免费 = 45 分钟, 进位 2 单位 × ¥2 = ¥4
	if a.FeeCents != 400 {
		t.Errorf("费用: 得到 %d, 期望 400", a.FeeCents)
	}
	if !a.UniqueBill() {
		t.Error("应唯一确定")
	}
}

func TestMissingExitBounded(t *testing.T) {
	events := []Event{ev("2026-10-01T08:00:00+08:00", EventIn)}
	asOf := mustTime("2026-10-01T20:00:00+08:00")
	a, err := Analyze(events, testRule(), nil, &asOf)
	if err != nil {
		t.Fatal(err)
	}
	if a.Complete {
		t.Fatal("应判定为记录缺失")
	}
	if a.UniqueBill() {
		t.Error("费用不应唯一确定")
	}
	// 最低: 免费时长内出场, 费用 0, 区间 [08:00:01, 08:15:00]
	if a.Min.FeeCents != 0 {
		t.Errorf("最低费用: 得到 %d, 期望 0", a.Min.FeeCents)
	}
	if len(a.Min.Options) != 1 || len(a.Min.Options[0].Intervals) != 1 {
		t.Fatalf("最低费用方案数不符: %+v", a.Min.Options)
	}
	iv := a.Min.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T08:00:01+08:00") || iv.To != unix("2026-10-01T08:15:00+08:00") {
		t.Errorf("最低费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	// 最高: 20:00 出场, 原始 ¥48, 封顶 ¥20; 达到封顶的区间为 [12:45:01, 20:00:00]
	if a.Max.Unbounded {
		t.Fatal("有界时不应报告无界")
	}
	if a.Max.FeeCents != 2000 {
		t.Errorf("最高费用: 得到 %d, 期望 2000", a.Max.FeeCents)
	}
	if len(a.Max.Options) != 1 || len(a.Max.Options[0].Intervals) != 1 {
		t.Fatalf("最高费用方案数不符: %+v", a.Max.Options)
	}
	iv = a.Max.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T12:45:01+08:00") || iv.To != unix("2026-10-01T20:00:00+08:00") {
		t.Errorf("最高费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	if a.Min.Options[0].Insert != EventOut {
		t.Errorf("应补出场事件, 得到 %s", a.Min.Options[0].Insert)
	}
}

func TestMissingExitUnbounded(t *testing.T) {
	events := []Event{ev("2026-10-01T08:00:00+08:00", EventIn)}
	a, err := Analyze(events, testRule(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Max.Unbounded {
		t.Error("未提供 -as-of 时最高费用应无界")
	}
	if a.UniqueBill() {
		t.Error("无界时不应唯一确定")
	}
	if a.Min.FeeCents != 0 {
		t.Errorf("最低费用: 得到 %d, 期望 0", a.Min.FeeCents)
	}
	iv := a.Min.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T08:00:01+08:00") || iv.To != unix("2026-10-01T08:15:00+08:00") {
		t.Errorf("最低费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
}

func TestMissingEntryBounded(t *testing.T) {
	events := []Event{ev("2026-10-01T18:00:00+08:00", EventOut)}
	since := mustTime("2026-10-01T08:00:00+08:00")
	a, err := Analyze(events, testRule(), &since, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 最低: 免费时长内进场, 区间 [17:45:00, 17:59:59]
	if a.Min.FeeCents != 0 {
		t.Errorf("最低费用: 得到 %d, 期望 0", a.Min.FeeCents)
	}
	iv := a.Min.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T17:45:00+08:00") || iv.To != unix("2026-10-01T17:59:59+08:00") {
		t.Errorf("最低费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	// 最高: 08:00 进场, 原始 ¥40, 封顶 ¥20; 达到封顶的区间为 [08:00:00, 13:14:59]
	if a.Max.FeeCents != 2000 {
		t.Errorf("最高费用: 得到 %d, 期望 2000", a.Max.FeeCents)
	}
	iv = a.Max.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T08:00:00+08:00") || iv.To != unix("2026-10-01T13:14:59+08:00") {
		t.Errorf("最高费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	if a.Min.Options[0].Insert != EventIn {
		t.Errorf("应补进场事件, 得到 %s", a.Min.Options[0].Insert)
	}
}

func TestMissingMiddleEvent(t *testing.T) {
	// 第二段缺出场: 需在两条 IN 之间补 OUT
	events := []Event{
		ev("2026-10-01T08:00:00+08:00", EventIn),
		ev("2026-10-01T09:00:00+08:00", EventOut),
		ev("2026-10-01T10:00:00+08:00", EventIn),
		ev("2026-10-01T12:00:00+08:00", EventIn),
		ev("2026-10-01T13:00:00+08:00", EventOut),
	}
	a, err := Analyze(events, testRule(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 完整段: 08:00-09:00 → ¥4; 12:00-13:00 → ¥4; base = 800
	// 补 OUT ∈ (10:00, 12:00): 最短 10:00:01 出场 → 免费, 总 ¥8
	if a.Min.FeeCents != 800 {
		t.Errorf("最低费用: 得到 %d, 期望 800", a.Min.FeeCents)
	}
	iv := a.Min.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T10:00:01+08:00") || iv.To != unix("2026-10-01T10:15:00+08:00") {
		t.Errorf("最低费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	// 最长 11:59:59 出场: 1h59m59s - 15m = 1h44m59s → 4 单位 → ¥8, 总 ¥16
	if a.Max.FeeCents != 1600 {
		t.Errorf("最高费用: 得到 %d, 期望 1600", a.Max.FeeCents)
	}
	iv = a.Max.Options[0].Intervals[0]
	if iv.From != unix("2026-10-01T11:45:01+08:00") || iv.To != unix("2026-10-01T11:59:59+08:00") {
		t.Errorf("最高费用区间: 得到 [%d, %d]", iv.From, iv.To)
	}
	if a.Min.Options[0].Pos != 3 {
		t.Errorf("插入位置: 得到 %d, 期望 3", a.Min.Options[0].Pos)
	}
}

func TestUniqueBillDespiteMissing(t *testing.T) {
	// 补事件时间范围完全落在免费时长内, 费用唯一为 0
	events := []Event{ev("2026-10-01T08:00:00+08:00", EventIn)}
	asOf := mustTime("2026-10-01T08:10:00+08:00")
	a, err := Analyze(events, testRule(), nil, &asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !a.UniqueBill() {
		t.Error("费用应唯一确定为 0")
	}
	if a.Min.FeeCents != 0 || a.Max.FeeCents != 0 {
		t.Errorf("费用应为 0, 得到 min=%d max=%d", a.Min.FeeCents, a.Max.FeeCents)
	}
}

func TestInvalidRecords(t *testing.T) {
	rule := testRule()
	// 偶数条且不合法: 缺失超过 1 个
	bad1 := []Event{ev("2026-10-01T08:00:00+08:00", EventIn), ev("2026-10-01T09:00:00+08:00", EventIn)}
	if _, err := Analyze(bad1, rule, nil, nil); err == nil {
		t.Error("连续两个进场应报错")
	}
	// 奇数条但无法通过补 1 个修复
	bad2 := []Event{
		ev("2026-10-01T08:00:00+08:00", EventIn),
		ev("2026-10-01T09:00:00+08:00", EventIn),
		ev("2026-10-01T10:00:00+08:00", EventIn),
	}
	if _, err := Analyze(bad2, rule, nil, nil); err == nil {
		t.Error("三个进场应报错")
	}
	// 时间范围约束导致无可行方案
	events := []Event{ev("2026-10-01T08:00:00+08:00", EventIn)}
	asOf := mustTime("2026-10-01T07:00:00+08:00")
	if _, err := Analyze(events, rule, nil, &asOf); err == nil {
		t.Error("as-of 早于进场时间应报错")
	}
}

func TestEmptyRecord(t *testing.T) {
	a, err := Analyze(nil, testRule(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Complete || a.FeeCents != 0 {
		t.Errorf("空记录应为完整且费用 0, 得到 %+v", a)
	}
}

func TestParseEventsValidation(t *testing.T) {
	if _, err := ParseEvents([]byte(`[{"time":"2026-10-01T08:00:00+08:00","type":"in"}]`)); err != nil {
		t.Errorf("合法输入不应报错: %v", err)
	}
	if _, err := ParseEvents([]byte(`[{"time":"bad","type":"in"}]`)); err == nil {
		t.Error("非法时间应报错")
	}
	if _, err := ParseEvents([]byte(`[{"time":"2026-10-01T08:00:00+08:00","type":"enter"}]`)); err == nil {
		t.Error("非法类型应报错")
	}
	dup := `[{"time":"2026-10-01T08:00:00+08:00","type":"in"},{"time":"2026-10-01T08:00:00+08:00","type":"out"}]`
	if _, err := ParseEvents([]byte(dup)); err == nil {
		t.Error("相同时间应报错")
	}
}

func TestRunCLI(t *testing.T) {
	stdin := strings.NewReader(`[{"time":"2026-10-01T08:00:00+08:00","type":"in"}]`)
	var out, errOut strings.Builder
	code := run([]string{"-free", "15m", "-unit", "30m", "-rate", "2", "-daily-cap", "20",
		"-as-of", "2026-10-01T20:00:00+08:00", "-json", "-"}, stdin, &out, &errOut)
	if code != 0 {
		t.Fatalf("退出码 %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"feeCents": 2000`) {
		t.Errorf("JSON 输出应包含最高费用 2000 分: %s", out.String())
	}
	if !strings.Contains(out.String(), `"uniqueBill": false`) {
		t.Errorf("JSON 输出应标记非唯一账单: %s", out.String())
	}
}
