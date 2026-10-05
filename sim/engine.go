package sim

import (
	"fmt"
	"sort"
)

// Engine 为结算引擎。事件按到达顺序（Seq）逐个 Submit；每次到达
// 都会对所有已到达事件按“就诊日期 + 到达序号”做确定性重算，
// 与上一轮结果对比生成追补/追回记录，从而精确处理迟到事件。
type Engine struct {
	policy      Policy
	coverages   map[string]Coverage
	sharedCaps  map[string]int64
	arrived     []Event
	lastPayable map[string]int64 // event ID -> 上一轮应付金额
	settlements []Settlement
	adjustments []Adjustment
}

// NewEngine 校验规则并创建引擎。
func NewEngine(p Policy) (*Engine, error) {
	e := &Engine{
		policy:      p,
		coverages:   make(map[string]Coverage),
		sharedCaps:  make(map[string]int64),
		lastPayable: make(map[string]int64),
	}
	for _, c := range p.Coverages {
		if c.ID == "" {
			return nil, fmt.Errorf("coverage with empty id")
		}
		if _, dup := e.coverages[c.ID]; dup {
			return nil, fmt.Errorf("duplicate coverage id %q", c.ID)
		}
		if c.Window.End.Before(c.Window.Start.Time) {
			return nil, fmt.Errorf("coverage %q: window end before start", c.ID)
		}
		if c.WaitingDays < 0 || c.DeductibleCents < 0 ||
			c.PerEventLimitCents < 0 || c.AggregateLimitCents < 0 {
			return nil, fmt.Errorf("coverage %q: negative rule value", c.ID)
		}
		e.coverages[c.ID] = c
	}
	for _, s := range p.SharedLimits {
		if s.ID == "" {
			return nil, fmt.Errorf("shared limit with empty id")
		}
		if s.LimitCents <= 0 {
			return nil, fmt.Errorf("shared limit %q: limit must be positive", s.ID)
		}
		if _, dup := e.sharedCaps[s.ID]; dup {
			return nil, fmt.Errorf("duplicate shared limit id %q", s.ID)
		}
		e.sharedCaps[s.ID] = s.LimitCents
	}
	for _, c := range p.Coverages {
		if c.SharedGroup != "" {
			if _, ok := e.sharedCaps[c.SharedGroup]; !ok {
				return nil, fmt.Errorf("coverage %q references unknown shared limit group %q", c.ID, c.SharedGroup)
			}
		}
	}
	return e, nil
}

// Submit 登记一个到达的理赔事件并触发全量重算。
func (e *Engine) Submit(ev Event) error {
	if _, ok := e.coverages[ev.Coverage]; !ok {
		return fmt.Errorf("event %q references unknown coverage %q", ev.ID, ev.Coverage)
	}
	if ev.AmountCents < 0 {
		return fmt.Errorf("event %q: negative amount", ev.ID)
	}
	for _, prev := range e.arrived {
		if prev.ID == ev.ID {
			return fmt.Errorf("duplicate event id %q", ev.ID)
		}
	}
	e.arrived = append(e.arrived, ev)
	e.settle()
	return nil
}

// coverageState 为重算过程中单个责任的可变状态。
type coverageState struct {
	deductibleLeft int64
	aggregateLeft  int64
}

// settle 对所有已到达事件做确定性重算，并 diff 出追补/追回记录。
func (e *Engine) settle() {
	events := make([]Event, len(e.arrived))
	copy(events, e.arrived)
	// 结算顺序：就诊日期优先，同日按到达序号，再以 ID 兜底保证确定性。
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.ServiceDate.Equal(b.ServiceDate.Time) {
			return a.ServiceDate.Before(b.ServiceDate.Time)
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.ID < b.ID
	})

	states := make(map[string]*coverageState)
	for _, c := range e.policy.Coverages {
		states[c.ID] = &coverageState{
			deductibleLeft: c.DeductibleCents,
			aggregateLeft:  c.AggregateLimitCents, // 0 表示不限
		}
	}
	sharedLeft := make(map[string]int64)
	for id, cap := range e.sharedCaps {
		sharedLeft[id] = cap
	}

	settlements := make([]Settlement, 0, len(events))
	for _, ev := range events {
		cov := e.coverages[ev.Coverage]
		st := states[ev.Coverage]
		s := settleEvent(ev, cov, st, sharedLeft)
		settlements = append(settlements, s)
	}

	// 与上一轮结果 diff：仅对已结算过的事件生成追补/追回记录。
	for _, s := range settlements {
		prev, seen := e.lastPayable[s.EventID]
		if !seen {
			continue // 新到达事件的首轮结算不算调整
		}
		switch delta := s.PayableCents - prev; {
		case delta > 0:
			e.adjustments = append(e.adjustments, Adjustment{
				EventID:     s.EventID,
				Kind:        AdjSupplement,
				AmountCents: delta,
				Note:        "迟到事件触发重算，应付金额上调",
			})
		case delta < 0:
			e.adjustments = append(e.adjustments, Adjustment{
				EventID:     s.EventID,
				Kind:        AdjClawback,
				AmountCents: -delta,
				Note:        "迟到事件触发重算，应付金额下调",
			})
		}
	}

	e.lastPayable = make(map[string]int64, len(settlements))
	for _, s := range settlements {
		e.lastPayable[s.EventID] = s.PayableCents
	}
	e.settlements = settlements
}

// settleEvent 计算单个事件的应付金额与扣减原因，并推进责任/共享状态。
// 扣减顺序（输入规则的一部分，固定且确定）：
//  1. 保障区间校验；2. 等待期校验；3. 单次限额；
//  4. 免赔额；5. 责任累计限额；6. 共享限额。
func settleEvent(ev Event, cov Coverage, st *coverageState, sharedLeft map[string]int64) Settlement {
	s := Settlement{
		EventID:      ev.ID,
		Coverage:     ev.Coverage,
		ServiceDate:  ev.ServiceDate,
		ClaimedCents: ev.AmountCents,
	}
	remaining := ev.AmountCents

	reject := func(code, detail string) Settlement {
		s.Reasons = append(s.Reasons, Reason{Code: code, Detail: detail, AmountCents: remaining})
		return s
	}

	if !cov.Window.Contains(ev.ServiceDate) {
		return reject(ReasonOutOfWindow, fmt.Sprintf("就诊日期 %s 不在保障区间 %s ~ %s 内",
			ev.ServiceDate.Format("2006-01-02"),
			cov.Window.Start.Format("2006-01-02"), cov.Window.End.Format("2006-01-02")))
	}
	if ev.ServiceDate.Before(cov.WaitingEnd().Time) {
		return reject(ReasonWaitingPeriod, fmt.Sprintf("就诊日期 %s 处于等待期内（等待期至 %s 前）",
			ev.ServiceDate.Format("2006-01-02"), cov.WaitingEnd().Format("2006-01-02")))
	}

	if cov.PerEventLimitCents > 0 && remaining > cov.PerEventLimitCents {
		cut := remaining - cov.PerEventLimitCents
		s.Reasons = append(s.Reasons, Reason{Code: ReasonPerEventLimit,
			Detail: fmt.Sprintf("超出单次限额 %d 分", cov.PerEventLimitCents), AmountCents: cut})
		remaining = cov.PerEventLimitCents
	}

	if st.deductibleLeft > 0 && remaining > 0 {
		ded := st.deductibleLeft
		if ded > remaining {
			ded = remaining
		}
		s.Reasons = append(s.Reasons, Reason{Code: ReasonDeductible,
			Detail: "免赔额扣减", AmountCents: ded})
		st.deductibleLeft -= ded
		remaining -= ded
	}

	if st.aggregateLeft > 0 && remaining > st.aggregateLeft {
		cut := remaining - st.aggregateLeft
		s.Reasons = append(s.Reasons, Reason{Code: ReasonAggregate,
			Detail: fmt.Sprintf("责任 %s 累计限额不足", cov.ID), AmountCents: cut})
		remaining = st.aggregateLeft
	}
	if st.aggregateLeft > 0 {
		st.aggregateLeft -= remaining
	}

	if cov.SharedGroup != "" && remaining > 0 {
		left := sharedLeft[cov.SharedGroup]
		if remaining > left {
			cut := remaining - left
			s.Reasons = append(s.Reasons, Reason{Code: ReasonSharedLimit,
				Detail: fmt.Sprintf("共享限额组 %s 余额不足", cov.SharedGroup), AmountCents: cut})
			remaining = left
		}
		sharedLeft[cov.SharedGroup] = left - remaining
	}

	s.PayableCents = remaining
	return s
}

// Report 汇总当前结算结果，并校验所有限额不变量。
func (e *Engine) Report() Report {
	rep := Report{
		Settlements:      e.settlements,
		Adjustments:      e.adjustments,
		TotalsByCoverage: make(map[string]int64),
	}
	if len(e.sharedCaps) > 0 {
		rep.TotalsBySharedGrp = make(map[string]int64)
	}
	for _, s := range e.settlements {
		rep.TotalsByCoverage[s.Coverage] += s.PayableCents
		if cov := e.coverages[s.Coverage]; cov.SharedGroup != "" {
			rep.TotalsBySharedGrp[cov.SharedGroup] += s.PayableCents
		}
	}
	rep.Violations = e.verify()
	return rep
}

// verify 校验不变量：任何事件、责任、共享组的已付金额均不突破限额。
func (e *Engine) verify() []string {
	var violations []string
	for _, s := range e.settlements {
		cov := e.coverages[s.Coverage]
		if s.PayableCents < 0 {
			violations = append(violations, fmt.Sprintf("event %s: negative payable %d", s.EventID, s.PayableCents))
		}
		if s.PayableCents > s.ClaimedCents {
			violations = append(violations, fmt.Sprintf("event %s: payable %d exceeds claimed %d", s.EventID, s.PayableCents, s.ClaimedCents))
		}
		if cov.PerEventLimitCents > 0 && s.PayableCents > cov.PerEventLimitCents {
			violations = append(violations, fmt.Sprintf("event %s: payable %d exceeds per-event limit %d", s.EventID, s.PayableCents, cov.PerEventLimitCents))
		}
		var deducted int64
		for _, r := range s.Reasons {
			deducted += r.AmountCents
		}
		if s.ClaimedCents-deducted != s.PayableCents {
			violations = append(violations, fmt.Sprintf("event %s: claimed %d - deducted %d != payable %d", s.EventID, s.ClaimedCents, deducted, s.PayableCents))
		}
	}
	for id, cov := range e.coverages {
		if cov.AggregateLimitCents <= 0 {
			continue
		}
		var total int64
		for _, s := range e.settlements {
			if s.Coverage == id {
				total += s.PayableCents
			}
		}
		if total > cov.AggregateLimitCents {
			violations = append(violations, fmt.Sprintf("coverage %s: total paid %d exceeds aggregate limit %d", id, total, cov.AggregateLimitCents))
		}
	}
	for gid, cap := range e.sharedCaps {
		var total int64
		for _, s := range e.settlements {
			if e.coverages[s.Coverage].SharedGroup == gid {
				total += s.PayableCents
			}
		}
		if total > cap {
			violations = append(violations, fmt.Sprintf("shared group %s: total paid %d exceeds limit %d", gid, total, cap))
		}
	}
	return violations
}
