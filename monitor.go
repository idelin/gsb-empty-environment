// Package monitor 实现多币种联合授信的双时态（业务时间 / 记录时间）敞口监控器。
//
// 双时态模型：
//   - 业务时间（valid time）：事实在业务上生效的时间区间，如借款发生日、
//     汇率生效日、授信协议有效期、担保物估值生效日。
//   - 记录时间（transaction time）：事实被系统记录 / 修正的时间区间。
//     追溯修正不会改写历史，而是关闭旧记录（txTo）并追加新记录，
//     因此可以重现"任意记录时间点上我们所知道的历史"。
package monitor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// forever 表示开区间端点（无限远）。
const forever = int64(1) << 62

// record 是一条双时态事实记录。
type record[T any] struct {
	value     T
	validFrom int64
	validTo   int64 // 排他端点
	txFrom    int64
	txTo      int64 // 排他端点，forever 表示当前认知
}

// visible 判断记录在 (validTime, txTime) 双时态坐标下是否可见。
func (r record[T]) visible(validTime, txTime int64) bool {
	return r.validFrom <= validTime && validTime < r.validTo &&
		r.txFrom <= txTime && txTime < r.txTo
}

// table 是同一实体键的双时态历史。
type table[T any] struct {
	records []record[T]
}

// put 追加一条事实。若存在 validFrom 相同且仍未关闭的记录，
// 将其在记录时间上关闭（追溯修正语义）；否则新记录仅扩展业务时间线。
func (t *table[T]) put(txTime, validFrom, validTo int64, v T) {
	for i := range t.records {
		r := &t.records[i]
		if r.txTo == forever && r.validFrom == validFrom {
			r.txTo = txTime
		}
	}
	t.records = append(t.records, record[T]{v, validFrom, validTo, txTime, forever})
}

// get 返回"在记录时间 txTime 所认知的、业务时间 validTime 上生效的"事实：
// 在所有可见记录中取 validFrom 最大者（并列时取 txFrom 最大者）。
func (t *table[T]) get(validTime, txTime int64) (T, bool) {
	best := -1
	for i := range t.records {
		r := &t.records[i]
		if !r.visible(validTime, txTime) {
			continue
		}
		if best == -1 ||
			r.validFrom > t.records[best].validFrom ||
			(r.validFrom == t.records[best].validFrom && r.txFrom > t.records[best].txFrom) {
			best = i
		}
	}
	if best == -1 {
		var zero T
		return zero, false
	}
	return t.records[best].value, true
}

func getFrom[T any](t *table[T], validTime, txTime int64) (T, bool) {
	if t == nil {
		var zero T
		return zero, false
	}
	return t.get(validTime, txTime)
}

// Loan 是一笔子公司借款（金额为借款币种，单位：万）。
type Loan struct {
	ID         string
	Subsidiary string
	Currency   string
	Amount     float64
}

// Agreement 是子公司授信协议（额度为折合人民币万元）。
type Agreement struct {
	Subsidiary string
	Limit      float64
}

// Collateral 是担保物估值（折合人民币万元）。
type Collateral struct {
	ID         string
	Subsidiary string
	Value      float64
}

// Store 是双时态事实库 + 监控参数。
type Store struct {
	groupLimit      float64 // 集团总授信额度（万元）
	warningRatio    float64 // 预警线占授信额度比例
	pledgeWarnRatio float64 // 质押率告警阈值

	loans       map[string]*table[Loan]       // 键：借款 ID
	fxRates     map[string]*table[float64]    // 键：币种，值：折人民币汇率
	agreements  map[string]*table[Agreement]  // 键：子公司
	collaterals map[string]*table[Collateral] // 键：担保物 ID
}

// NewStore 创建监控器。groupLimit 为集团总授信（万元），
// warningRatio 为预警线比例，pledgeWarnRatio 为质押率告警阈值。
func NewStore(groupLimit, warningRatio, pledgeWarnRatio float64) *Store {
	return &Store{
		groupLimit:      groupLimit,
		warningRatio:    warningRatio,
		pledgeWarnRatio: pledgeWarnRatio,
		loans:           map[string]*table[Loan]{},
		fxRates:         map[string]*table[float64]{},
		agreements:      map[string]*table[Agreement]{},
		collaterals:     map[string]*table[Collateral]{},
	}
}

// RecordLoan 在记录时间 txTime 登记一笔自 validFrom 起生效的借款。
// 用相同 ID 和相同 validFrom 再次登记即为追溯修正历史借款。
func (s *Store) RecordLoan(txTime, validFrom int64, l Loan) {
	t := s.loans[l.ID]
	if t == nil {
		t = &table[Loan]{}
		s.loans[l.ID] = t
	}
	t.put(txTime, validFrom, forever, l)
}

// RecordFxRate 在记录时间 txTime 登记某币种自 validFrom 起生效的汇率。
func (s *Store) RecordFxRate(txTime, validFrom int64, currency string, rate float64) {
	t := s.fxRates[currency]
	if t == nil {
		t = &table[float64]{}
		s.fxRates[currency] = t
	}
	t.put(txTime, validFrom, forever, rate)
}

// RecordAgreement 登记子公司授信协议，业务有效期为 [validFrom, validTo)。
func (s *Store) RecordAgreement(txTime, validFrom, validTo int64, a Agreement) {
	t := s.agreements[a.Subsidiary]
	if t == nil {
		t = &table[Agreement]{}
		s.agreements[a.Subsidiary] = t
	}
	t.put(txTime, validFrom, validTo, a)
}

// RecordCollateral 在记录时间 txTime 登记担保物自 validFrom 起的估值。
// 估值变动（新 validFrom）与估值追溯修正（同 validFrom）均走双时态语义。
func (s *Store) RecordCollateral(txTime, validFrom int64, c Collateral) {
	t := s.collaterals[c.ID]
	if t == nil {
		t = &table[Collateral]{}
		s.collaterals[c.ID] = t
	}
	t.put(txTime, validFrom, forever, c)
}

// ExposureDetail 是单笔借款在重放时点上的折算明细。
type ExposureDetail struct {
	Subsidiary string
	LoanID     string
	Currency   string
	Amount     float64
	FxRate     float64
	Exposure   float64 // 折合人民币万元
	Counted    bool    // 是否计入集团总敞口
	Note       string  // 未计入原因等说明
}

// PledgeAssessment 是担保物价值变动引发的质押率重算结果。
type PledgeAssessment struct {
	Subsidiary      string
	CollateralValue float64 // 该子公司担保物估值合计（万元）
	Exposure        float64 // 该子公司计入敞口（万元）
	PledgeRate      float64 // 敞口 / 担保物估值
	Alert           bool
}

// Report 是一次双时态重放的监控结果。
type Report struct {
	ValidTime   int64
	TxTime      int64
	Details     []ExposureDetail
	Pledges     []PledgeAssessment
	Trace       []string // 质押率重算等追溯记录
	Total       float64
	Limit       float64
	WarningLine float64
	Alerts      []string
}

// HasAlert 报告是否存在告警。
func (r Report) HasAlert() bool { return len(r.Alerts) > 0 }

// Replay 重放"记录时间 txTime 时所认知的业务时间 validTime 的集团敞口"，
// 折算受该时点的汇率与授信协议有效期共同影响，并对担保物重算质押率。
func (s *Store) Replay(validTime, txTime int64) Report {
	rep := Report{
		ValidTime:   validTime,
		TxTime:      txTime,
		Limit:       s.groupLimit,
		WarningLine: s.groupLimit * s.warningRatio,
	}

	subExposure := map[string]float64{}

	for _, id := range sortedKeys(s.loans) {
		l, ok := s.loans[id].get(validTime, txTime)
		if !ok {
			continue
		}
		d := ExposureDetail{
			Subsidiary: l.Subsidiary,
			LoanID:     l.ID,
			Currency:   l.Currency,
			Amount:     l.Amount,
			Counted:    true,
		}

		agr, hasAgr := getFrom(s.agreements[l.Subsidiary], validTime, txTime)
		if !hasAgr {
			d.Counted = false
			d.Note = "授信协议不在有效期，敞口不计入"
		}
		rate, hasRate := getFrom(s.fxRates[l.Currency], validTime, txTime)
		if d.Counted && !hasRate {
			d.Counted = false
			d.Note = "缺少 " + l.Currency + " 汇率，敞口未折算"
			rep.Alerts = append(rep.Alerts, fmt.Sprintf(
				"子公司 %s 借款 %s 缺少 %s 汇率，敞口未计入", l.Subsidiary, l.ID, l.Currency))
		}
		if d.Counted {
			d.FxRate = rate
			d.Exposure = l.Amount * rate
			if agr.Limit > 0 && d.Exposure > agr.Limit {
				rep.Alerts = append(rep.Alerts, fmt.Sprintf(
					"子公司 %s 敞口 %s万 超过其授信协议额度 %s万",
					l.Subsidiary, wan(d.Exposure), wan(agr.Limit)))
			}
			subExposure[l.Subsidiary] += d.Exposure
			rep.Total += d.Exposure
		}
		rep.Details = append(rep.Details, d)
	}

	// 担保物估值（按子公司汇总）与质押率重算。
	subCollateral := map[string]float64{}
	for _, id := range sortedKeys(s.collaterals) {
		c, ok := s.collaterals[id].get(validTime, txTime)
		if !ok {
			continue
		}
		subCollateral[c.Subsidiary] += c.Value
	}
	for _, sub := range sortedKeys(subCollateral) {
		exposure := subExposure[sub]
		if exposure <= 0 {
			continue
		}
		value := subCollateral[sub]
		rate := exposure / value
		pa := PledgeAssessment{
			Subsidiary:      sub,
			CollateralValue: value,
			Exposure:        exposure,
			PledgeRate:      rate,
			Alert:           rate > s.pledgeWarnRatio,
		}
		rep.Pledges = append(rep.Pledges, pa)
		rep.Trace = append(rep.Trace, fmt.Sprintf(
			"质押率重算: 子公司%s 敞口%s万 / 担保物估值%s万 = 质押率%.2f%% (阈值%.0f%%)",
			sub, wan(exposure), wan(value), rate*100, s.pledgeWarnRatio*100))
		if pa.Alert {
			rep.Alerts = append(rep.Alerts, fmt.Sprintf(
				"子公司 %s 质押率 %.2f%% 超过阈值 %.0f%%（敞口 %s万 / 担保物 %s万）",
				sub, rate*100, s.pledgeWarnRatio*100, wan(exposure), wan(value)))
		}
	}

	if rep.Total >= rep.WarningLine {
		rep.Alerts = append(rep.Alerts, fmt.Sprintf(
			"集团总敞口 %s万 触碰预警线 %s万（集团授信 %s万，预警比例 %.0f%%）",
			wan(rep.Total), wan(rep.WarningLine), wan(rep.Limit), s.warningRatio*100))
	}
	if rep.Total > rep.Limit {
		rep.Alerts = append(rep.Alerts, fmt.Sprintf(
			"集团总敞口 %s万 击穿集团授信额度 %s万", wan(rep.Total), wan(rep.Limit)))
	}
	return rep
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func wan(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// String 以可读形式输出重放报告。
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "重放时点(业务时间=%d, 记录时间=%d) 集团总敞口=%s万 / 授信=%s万 (预警线=%s万)\n",
		r.ValidTime, r.TxTime, wan(r.Total), wan(r.Limit), wan(r.WarningLine))
	for _, d := range r.Details {
		if d.Counted {
			fmt.Fprintf(&b, "  借款 %s: 子公司%s %s万 %s × 汇率%s = 敞口%s万\n",
				d.LoanID, d.Subsidiary, wan(d.Amount), d.Currency, wan(d.FxRate), wan(d.Exposure))
		} else {
			fmt.Fprintf(&b, "  借款 %s: 子公司%s %s万 %s —— %s\n",
				d.LoanID, d.Subsidiary, wan(d.Amount), d.Currency, d.Note)
		}
	}
	for _, t := range r.Trace {
		fmt.Fprintf(&b, "  %s\n", t)
	}
	for _, a := range r.Alerts {
		fmt.Fprintf(&b, "  [告警] %s\n", a)
	}
	if !r.HasAlert() {
		fmt.Fprintf(&b, "  无告警\n")
	}
	return b.String()
}
