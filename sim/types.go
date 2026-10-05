// Package sim 实现一个保障结算模拟器。
//
// 模拟器完全按输入规则（JSON 配置）处理保障区间、等待期、免赔额与
// 累计限额，不引用任何真实保险条款。金额一律使用整数“分”表示，
// 避免浮点误差。
package sim

import (
	"encoding/json"
	"fmt"
	"time"
)

// Date 是一个 civil date（YYYY-MM-DD），不含时区。
type Date struct {
	time.Time
}

// ParseDate 解析 YYYY-MM-DD 格式的日期。
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: %v", s, err)
	}
	return Date{t}, nil
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	d.Time = parsed.Time
	return nil
}

// MarshalJSON 实现 json.Marshaler。
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Format("2006-01-02"))
}

// Window 表示保障区间，起止日期均为闭区间。
type Window struct {
	Start Date `json:"start"`
	End   Date `json:"end"`
}

// Contains 判断日期是否落在保障区间内（含边界）。
func (w Window) Contains(d Date) bool {
	return !d.Before(w.Start.Time) && !d.After(w.End.Time)
}

// Coverage 表示一项保障责任及其结算规则。
type Coverage struct {
	ID     string `json:"id"`
	Window Window `json:"window"`
	// WaitingDays 为等待期天数：保障区间起算日 + WaitingDays 之前
	// 发生的就诊不予赔付。
	WaitingDays int `json:"waiting_days"`
	// DeductibleCents 为整个保障区间内的累计免赔额（分），按事件
	// 顺序消耗，耗尽即止。
	DeductibleCents int64 `json:"deductible_cents"`
	// PerEventLimitCents 为单次事件赔付上限（分），0 表示不限。
	PerEventLimitCents int64 `json:"per_event_limit_cents"`
	// AggregateLimitCents 为该责任在保障区间内的累计限额（分），
	// 0 表示不限。
	AggregateLimitCents int64 `json:"aggregate_limit_cents"`
	// SharedGroup 非空时表示该责任参与指定 ID 的共享限额组。
	SharedGroup string `json:"shared_limit_group,omitempty"`
}

// WaitingEnd 返回等待期结束日（不含）：就诊日期早于该日即在等待期内。
func (c Coverage) WaitingEnd() Date {
	return Date{c.Window.Start.AddDate(0, 0, c.WaitingDays)}
}

// SharedLimit 表示跨责任共享的累计限额组。
type SharedLimit struct {
	ID         string `json:"id"`
	LimitCents int64  `json:"limit_cents"`
}

// Policy 为整套输入规则。
type Policy struct {
	Coverages    []Coverage    `json:"coverages"`
	SharedLimits []SharedLimit `json:"shared_limits,omitempty"`
}

// Event 为一次理赔事件。事件可以迟到：Seq 表示报案（到达）顺序，
// ServiceDate 表示就诊日期，二者无关。
type Event struct {
	ID          string `json:"id"`
	Coverage    string `json:"coverage"`
	ServiceDate Date   `json:"service_date"`
	Seq         int    `json:"seq"`
	AmountCents int64  `json:"amount_cents"`
}

// Input 为模拟器的完整输入。
type Input struct {
	Policy Policy  `json:"policy"`
	Events []Event `json:"events"`
}

// 扣减原因代码。
const (
	ReasonOutOfWindow   = "OUT_OF_WINDOW"     // 保障区间外
	ReasonWaitingPeriod = "IN_WAITING_PERIOD" // 等待期内
	ReasonDeductible    = "DEDUCTIBLE"        // 免赔额扣减
	ReasonPerEventLimit = "PER_EVENT_LIMIT"   // 单次限额
	ReasonAggregate     = "AGGREGATE_LIMIT"   // 责任累计限额
	ReasonSharedLimit   = "SHARED_LIMIT"      // 共享限额
)

// Reason 记录一笔扣减的原因与金额。
type Reason struct {
	Code        string `json:"code"`
	Detail      string `json:"detail"`
	AmountCents int64  `json:"amount_cents"`
}

// Settlement 为单个事件的最终结算结果。
type Settlement struct {
	EventID      string   `json:"event_id"`
	Coverage     string   `json:"coverage"`
	ServiceDate  Date     `json:"service_date"`
	ClaimedCents int64    `json:"claimed_cents"`
	PayableCents int64    `json:"payable_cents"`
	Reasons      []Reason `json:"reasons,omitempty"`
}

// 调整记录类型。
const (
	AdjSupplement = "追补" // 应付金额上调
	AdjClawback   = "追回" // 应付金额下调
)

// Adjustment 表示迟到事件触发重算后，对已结算事件的追补/追回记录。
type Adjustment struct {
	EventID     string `json:"event_id"`
	Kind        string `json:"kind"`
	AmountCents int64  `json:"amount_cents"`
	Note        string `json:"note"`
}

// Report 为模拟器输出。
type Report struct {
	Settlements       []Settlement     `json:"settlements"`
	Adjustments       []Adjustment     `json:"adjustments,omitempty"`
	TotalsByCoverage  map[string]int64 `json:"totals_paid_cents"`
	TotalsBySharedGrp map[string]int64 `json:"shared_totals_cents,omitempty"`
	Violations        []string         `json:"violations,omitempty"`
}
