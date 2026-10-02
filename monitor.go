// Package exposure 实现多币种联合授信的双时态（valid time / transaction time）
// 集团敞口监控器。
//
// 核心概念：
//   - valid time（业务时间）：事实在业务上生效的时间区间；
//   - transaction time（记录时间）：事实被系统知悉的时间区间。
// 任何追溯调整（如事后上调历史借款）都不会改写历史，而是关闭旧版本的
// transaction-time 区间并写入新版本，从而支持"以任意知悉时点重放历史"。
package exposure

import (
	"fmt"
	"math"
	"sort"
)

// Time 为逻辑时钟节拍，避免依赖真实墙钟，便于测试与重放。
type Time int64

// Infinity 表示开区间端点（永不失效 / 当前仍被知悉）。
const Infinity = Time(1) << 62

func inInterval(t, from, to Time) bool { return t >= from && t < to }

// FacilityVersion 集团授信协议的一个双时态版本。
type FacilityVersion struct {
	Limit              float64 // 授信额度（基准币种）
	ValidFrom, ValidTo Time    // 协议有效期
	TxFrom, TxTo       Time    // 记录有效期
}

// ExposureVersion 子公司借款敞口的一个双时态版本。
type ExposureVersion struct {
	Subsidiary         string
	Amount             float64
	Currency           string
	ValidFrom, ValidTo Time
	TxFrom, TxTo       Time
}

// FXRate 汇率观测点：1 单位外币 = Rate 单位基准币，自 ValidFrom 起生效。
type FXRate struct {
	Currency  string
	Rate      float64
	ValidFrom Time
}

// CollateralVersion 担保物价值的一个双时态版本。
type CollateralVersion struct {
	ID                 string
	Owner              string // 出质子公司
	Value              float64
	Currency           string
	ValidFrom, ValidTo Time
	TxFrom, TxTo       Time
}

// ExposureLine 单笔敞口折算明细。
type ExposureLine struct {
	Subsidiary string
	Amount     float64
	Currency   string
	Rate       float64 // 折算所用汇率
	Base       float64 // 折算后基准币金额
}

// Alert 预警记录。
type Alert struct {
	ValidTime Time
	TxTime    Time
	Total     float64 // 集团总敞口（基准币）
	Limit     float64 // 授信额度
	Ratio     float64 // 敞口 / 额度
	Reason    string
}

// PledgeTrace 质押率重算追溯记录。
type PledgeTrace struct {
	ValidTime       Time
	TxTime          Time
	Owner           string
	CollateralValue float64 // 担保物价值（基准币）
	Exposure        float64 // 该子公司敞口（基准币）
	PledgeRatio     float64 // 质押率 = 敞口 / 担保物价值
	Note            string  // 触发原因
}

// Monitor 集团授信敞口双时态监控器。
type Monitor struct {
	baseCurrency string
	warningRatio float64 // 预警线：敞口/额度 达到该比例即告警

	facilities  []FacilityVersion
	exposures   []ExposureVersion
	fxRates     map[string][]FXRate
	collaterals []CollateralVersion

	alerts       []Alert
	pledgeTraces []PledgeTrace
}

// NewMonitor 创建监控器。baseCurrency 为折算基准币；warningRatio 为预警线
// （如 0.65 表示集团总敞口达到授信额度 65% 时告警）。
func NewMonitor(baseCurrency string, warningRatio float64) *Monitor {
	return &Monitor{
		baseCurrency: baseCurrency,
		warningRatio: warningRatio,
		fxRates:      make(map[string][]FXRate),
	}
}

// WarningRatio 返回预警线比例。
func (m *Monitor) WarningRatio() float64 { return m.warningRatio }

// SetFacility 登记集团授信协议（额度、有效期）。同一时刻只保留一个当前版本，
// 重复调用视为对协议的更正（双时态：旧版本记录区间在 tx 关闭）。
func (m *Monitor) SetFacility(limit float64, validFrom, validTo Time, tx Time) {
	for i := range m.facilities {
		f := &m.facilities[i]
		if f.TxTo == Infinity {
			f.TxTo = tx
		}
	}
	m.facilities = append(m.facilities, FacilityVersion{
		Limit: limit, ValidFrom: validFrom, ValidTo: validTo, TxFrom: tx, TxTo: Infinity,
	})
}

// RecordBorrowing 登记子公司借款敞口，自 validFrom 起业务生效、自 tx 起被系统
// 知悉。生效日期规则：
//   - 若当前版本 ValidFrom >= validFrom，视为对既有记录的更正（如追溯上调历史
//     借款），旧版本记录区间在 tx 关闭；
//   - 若当前版本 ValidFrom < validFrom，视为新的生效日期记录，旧版本业务区间
//     在 validFrom 截断，记录区间保持开放。
func (m *Monitor) RecordBorrowing(subsidiary string, amount float64, currency string, validFrom Time, tx Time) {
	for i := range m.exposures {
		e := &m.exposures[i]
		if e.Subsidiary != subsidiary || e.TxTo != Infinity {
			continue
		}
		if e.ValidFrom >= validFrom {
			e.TxTo = tx
		} else {
			e.ValidTo = validFrom
		}
	}
	m.exposures = append(m.exposures, ExposureVersion{
		Subsidiary: subsidiary, Amount: amount, Currency: currency,
		ValidFrom: validFrom, ValidTo: Infinity, TxFrom: tx, TxTo: Infinity,
	})
}

// SetFXRate 登记汇率观测点，自 validFrom 起生效。
func (m *Monitor) SetFXRate(currency string, rate float64, validFrom Time) {
	m.fxRates[currency] = append(m.fxRates[currency], FXRate{
		Currency: currency, Rate: rate, ValidFrom: validFrom,
	})
}

// fxRateAt 返回 valid 时点最新生效的汇率；基准币恒为 1；未知币种返回 0。
func (m *Monitor) fxRateAt(currency string, valid Time) float64 {
	if currency == m.baseCurrency {
		return 1
	}
	best := 0.0
	bestFrom := Time(-1) << 62
	for _, r := range m.fxRates[currency] {
		if r.ValidFrom <= valid && r.ValidFrom >= bestFrom {
			best, bestFrom = r.Rate, r.ValidFrom
		}
	}
	return best
}

// RecordCollateral 登记担保物价值，自 validFrom 起业务生效、自 tx 起被知悉。
// 生效日期规则与 RecordBorrowing 相同：同 ValidFrom 重复登记视为价值更正
// （关闭旧版本记录区间）；更晚 ValidFrom 登记视为价值变动（旧版本业务区间在
// validFrom 截断，历史时点仍可按原价值重现）。
func (m *Monitor) RecordCollateral(id, owner string, value float64, currency string, validFrom Time, tx Time) {
	for i := range m.collaterals {
		c := &m.collaterals[i]
		if c.ID != id || c.TxTo != Infinity {
			continue
		}
		if c.ValidFrom >= validFrom {
			c.TxTo = tx
		} else {
			c.ValidTo = validFrom
		}
	}
	m.collaterals = append(m.collaterals, CollateralVersion{
		ID: id, Owner: owner, Value: value, Currency: currency,
		ValidFrom: validFrom, ValidTo: Infinity, TxFrom: tx, TxTo: Infinity,
	})
}

// ExposureAt 双时态查询：以 tx 时点的知悉状态，重放 valid 时点的集团总敞口
// （按 valid 时点汇率折算为基准币），并返回折算明细。
func (m *Monitor) ExposureAt(valid, tx Time) (float64, []ExposureLine) {
	var total float64
	var lines []ExposureLine
	for _, e := range m.exposures {
		if inInterval(valid, e.ValidFrom, e.ValidTo) && inInterval(tx, e.TxFrom, e.TxTo) {
			rate := m.fxRateAt(e.Currency, valid)
			base := e.Amount * rate
			lines = append(lines, ExposureLine{
				Subsidiary: e.Subsidiary, Amount: e.Amount,
				Currency: e.Currency, Rate: rate, Base: base,
			})
			total += base
		}
	}
	return total, lines
}

// subsidiaryExposureAt 返回单一子公司在 (valid, tx) 的敞口（基准币）。
func (m *Monitor) subsidiaryExposureAt(subsidiary string, valid, tx Time) float64 {
	var total float64
	for _, e := range m.exposures {
		if e.Subsidiary == subsidiary &&
			inInterval(valid, e.ValidFrom, e.ValidTo) && inInterval(tx, e.TxFrom, e.TxTo) {
			total += e.Amount * m.fxRateAt(e.Currency, valid)
		}
	}
	return total
}

// LimitAt 返回 (valid, tx) 时点生效的集团授信额度；ok 为 false 表示协议不在有效期。
func (m *Monitor) LimitAt(valid, tx Time) (limit float64, ok bool) {
	for _, f := range m.facilities {
		if inInterval(valid, f.ValidFrom, f.ValidTo) && inInterval(tx, f.TxFrom, f.TxTo) {
			return f.Limit, true
		}
	}
	return 0, false
}

// correctedAt 判断 tx 时点是否发生了敞口追溯调整（存在同子公司旧版本在 tx
// 关闭、新版本在 tx 生效）。
func (m *Monitor) correctedAt(tx Time) bool {
	closed := make(map[string]bool)
	opened := make(map[string]bool)
	for _, e := range m.exposures {
		if e.TxTo == tx {
			closed[e.Subsidiary] = true
		}
		if e.TxFrom == tx {
			opened[e.Subsidiary] = true
		}
	}
	for s := range closed {
		if opened[s] {
			return true
		}
	}
	return false
}

// Evaluate 评估 (valid, tx) 时点集团总敞口是否触碰预警线，命中则返回告警
// （纯查询，不写入告警日志；日志由 Replay 在告警沿记录）。
func (m *Monitor) Evaluate(valid, tx Time) *Alert {
	total, _ := m.ExposureAt(valid, tx)
	limit, ok := m.LimitAt(valid, tx)
	if !ok {
		if total > 0 {
			return &Alert{
				ValidTime: valid, TxTime: tx, Total: total,
				Ratio: math.Inf(1),
				Reason: fmt.Sprintf("授信协议不在有效期内，存在裸露敞口 %.0f %s",
					total, m.baseCurrency),
			}
		}
		return nil
	}
	ratio := total / limit
	if ratio >= m.warningRatio {
		reason := fmt.Sprintf("集团总敞口 %.0f %s 达到授信额度 %.0f 的 %.1f%%（预警线 %.0f%%）",
			total, m.baseCurrency, limit, ratio*100, m.warningRatio*100)
		if m.correctedAt(tx) {
			reason += fmt.Sprintf("；由时间 %d 的追溯调整触发", tx)
		}
		return &Alert{ValidTime: valid, TxTime: tx, Total: total, Limit: limit, Ratio: ratio, Reason: reason}
	}
	return nil
}

// Replay 以 tx 时点的知悉状态，重放 [from, to] 内每个业务时点并评估预警线。
// 返回所有命中告警的时点；告警日志仅在"无告警 -> 有告警"的上升沿记录一次，
// 避免重放刷屏。
func (m *Monitor) Replay(tx, from, to Time) []Alert {
	var hits []Alert
	alerting := false
	for v := from; v <= to; v++ {
		a := m.Evaluate(v, tx)
		if a == nil {
			alerting = false
			continue
		}
		hits = append(hits, *a)
		if !alerting {
			m.alerts = append(m.alerts, *a)
			alerting = true
		}
	}
	return hits
}

// Alerts 返回已记录的告警日志。
func (m *Monitor) Alerts() []Alert { return m.alerts }

// RecalcPledgeRatio 重算子公司在 (valid, tx) 时点的质押率并追加追溯记录。
// 担保物价值变动或借款被追溯调整后均应调用，note 记录触发原因。
func (m *Monitor) RecalcPledgeRatio(owner string, valid, tx Time, note string) PledgeTrace {
	var collateral float64
	for _, c := range m.collaterals {
		if c.Owner == owner &&
			inInterval(valid, c.ValidFrom, c.ValidTo) && inInterval(tx, c.TxFrom, c.TxTo) {
			collateral += c.Value * m.fxRateAt(c.Currency, valid)
		}
	}
	exposure := m.subsidiaryExposureAt(owner, valid, tx)
	ratio := math.Inf(1)
	if collateral > 0 {
		ratio = exposure / collateral
	}
	trace := PledgeTrace{
		ValidTime: valid, TxTime: tx, Owner: owner,
		CollateralValue: collateral, Exposure: exposure,
		PledgeRatio: ratio, Note: note,
	}
	m.pledgeTraces = append(m.pledgeTraces, trace)
	return trace
}

// PledgeTraces 返回全部质押率重算追溯记录（按记录顺序）。
func (m *Monitor) PledgeTraces() []PledgeTrace { return m.pledgeTraces }

// ExposureVersions 返回子公司敞口的全部双时态版本（按 validFrom、txFrom 排序），
// 用于审计与测试。
func (m *Monitor) ExposureVersions(subsidiary string) []ExposureVersion {
	var out []ExposureVersion
	for _, e := range m.exposures {
		if e.Subsidiary == subsidiary {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ValidFrom != out[j].ValidFrom {
			return out[i].ValidFrom < out[j].ValidFrom
		}
		return out[i].TxFrom < out[j].TxFrom
	})
	return out
}
