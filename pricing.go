package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// 计价组件错误。客户端提交的汇率、费用、舍入结果一律不可信,
// 成交前由服务端按当前策略重算并逐项比对。
var (
	ErrUnknownPair        = errors.New("pricing: unknown currency pair")
	ErrNoActivePolicy     = errors.New("pricing: no active policy for pair at given time")
	ErrQtyOutOfRange      = errors.New("pricing: quantity out of range or exceeds allowed precision")
	ErrQuoteExpired       = errors.New("pricing: quote expired")
	ErrVersionChanged     = errors.New("pricing: quote version no longer current, re-quote required")
	ErrPriceMismatch      = errors.New("pricing: quote fields do not match server-side recomputation")
	ErrExcessiveDeviation = errors.New("pricing: quote rate deviates from current rate beyond threshold")
)

// FeeSchedule 服务端配置的费用:按比例 + 固定部分。
type FeeSchedule struct {
	Percent Decimal `json:"percent"` // 如 0.001 表示 0.1%
	Fixed   Decimal `json:"fixed"`   // 以计价币种最小单位计
}

// PairPolicy 某币种对在一个时段内的计价策略。
// 汇率、费用、最小单位与舍入规则全部由此确定,客户端无权选择。
type PairPolicy struct {
	Pair            string        `json:"pair"`    // 如 "USD/CNY"
	Version         string        `json:"version"` // 报价版本,策略任何变更必须升版
	Rate            Decimal       `json:"rate"`    // 1 单位基准币 = Rate 计价币
	Fee             FeeSchedule   `json:"fee"`
	PriceScale      int32         `json:"price_scale"` // 计价币最小单位(小数位)
	QtyScale        int32         `json:"qty_scale"`   // 数量允许的最大小数位
	Rounding        RoundingMode  `json:"rounding"`    // 服务端指定舍入规则
	MinQty          Decimal       `json:"min_qty"`
	MaxQty          Decimal       `json:"max_qty"`
	ValidFrom       time.Time     `json:"valid_from"`
	ValidUntil      time.Time     `json:"valid_until"`
	QuoteTTL        time.Duration `json:"quote_ttl"`         // 报价有效期
	MaxDeviationBps int64         `json:"max_deviation_bps"` // 成交时允许的最大价差(基点)
}

// covers 报告策略在时刻 t 是否生效。
func (p PairPolicy) covers(t time.Time) bool {
	return !t.Before(p.ValidFrom) && t.Before(p.ValidUntil)
}

// Quote 服务端签发的报价。客户端只能原样回传,任何字段篡改
// 都会在 Settle 重算时被拒绝。
type Quote struct {
	Pair      string    `json:"pair"`
	Qty       Decimal   `json:"qty"`     // 标的数量(客户端唯一输入之一)
	Rate      Decimal   `json:"rate"`    // 锁定汇率
	Fee       Decimal   `json:"fee"`     // 费用总额(计价币)
	Gross     Decimal   `json:"gross"`   // 汇率换算额(计价币)
	Total     Decimal   `json:"total"`   // 应付总额 = Gross + Fee
	Version   string    `json:"version"` // 报价版本
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Settlement 成交结果,成交价与报价严格一致。
type Settlement struct {
	Quote     Quote     `json:"quote"`
	Total     Decimal   `json:"total"` // 实际成交价,等于 Quote.Total
	SettledAt time.Time `json:"settled_at"`
}

// Engine 计价引擎:维护各币种对的策略表,签发并校验报价。
// 并发安全,可供 Go 报价服务直接导入使用。
type Engine struct {
	mu       sync.RWMutex
	policies map[string][]PairPolicy // pair -> 按 ValidFrom 升序
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{policies: make(map[string][]PairPolicy)}
}

// SetPolicy 注册(或替换同时段同版本的)策略。
func (e *Engine) SetPolicy(p PairPolicy) error {
	if p.Pair == "" || p.Version == "" {
		return fmt.Errorf("pricing: policy pair and version are required")
	}
	if p.Rate.Sign() <= 0 {
		return fmt.Errorf("pricing: policy rate must be positive")
	}
	if !p.ValidFrom.Before(p.ValidUntil) {
		return fmt.Errorf("pricing: invalid policy window")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.policies[p.Pair]
	for i, q := range list {
		if q.Version == p.Version {
			list[i] = p
			e.policies[p.Pair] = list
			return nil
		}
	}
	list = append(list, p)
	sort.Slice(list, func(i, j int) bool { return list[i].ValidFrom.Before(list[j].ValidFrom) })
	e.policies[p.Pair] = list
	return nil
}

// policyAt 返回时刻 t 生效的策略;窗口重叠时取 ValidFrom 最新者。
func (e *Engine) policyAt(pair string, t time.Time) (PairPolicy, error) {
	list, ok := e.policies[pair]
	if !ok {
		return PairPolicy{}, ErrUnknownPair
	}
	best := -1
	for i := range list {
		if list[i].covers(t) && (best < 0 || list[i].ValidFrom.After(list[best].ValidFrom)) {
			best = i
		}
	}
	if best < 0 {
		return PairPolicy{}, ErrNoActivePolicy
	}
	return list[best], nil
}

// policyByVersion 按版本号查找历史策略(用于重算校验)。
func (e *Engine) policyByVersion(pair, version string) (PairPolicy, error) {
	list, ok := e.policies[pair]
	if !ok {
		return PairPolicy{}, ErrUnknownPair
	}
	for _, p := range list {
		if p.Version == version {
			return p, nil
		}
	}
	return PairPolicy{}, ErrVersionChanged
}

// compute 按策略精确计算报价各字段(十进制,无浮点误差)。
func compute(p PairPolicy, qty Decimal, issuedAt time.Time) (Quote, error) {
	if qty.Sign() <= 0 ||
		(!p.MinQty.IsZero() && qty.Cmp(p.MinQty) < 0) ||
		(!p.MaxQty.IsZero() && qty.Cmp(p.MaxQty) > 0) {
		return Quote{}, ErrQtyOutOfRange
	}
	// 数量精度不得超过服务端配置的最小单位。
	if !qty.Quantize(p.QtyScale, RoundDown).Equal(qty) {
		return Quote{}, ErrQtyOutOfRange
	}
	gross := qty.Mul(p.Rate).Quantize(p.PriceScale, p.Rounding)
	fee := gross.Mul(p.Fee.Percent).
		Quantize(p.PriceScale, p.Rounding).
		Add(p.Fee.Fixed.Quantize(p.PriceScale, p.Rounding))
	expiresAt := issuedAt.Add(p.QuoteTTL)
	if expiresAt.After(p.ValidUntil) {
		expiresAt = p.ValidUntil
	}
	return Quote{
		Pair:      p.Pair,
		Qty:       qty,
		Rate:      p.Rate,
		Fee:       fee,
		Gross:     gross,
		Total:     gross.Add(fee),
		Version:   p.Version,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// Quote 为客户端提交的标的与数量签发报价。
// 汇率、费用、最小单位与舍入规则由服务端按币种对与时段确定。
func (e *Engine) Quote(pair string, qty Decimal, at time.Time) (Quote, error) {
	e.mu.RLock()
	p, err := e.policyAt(pair, at)
	e.mu.RUnlock()
	if err != nil {
		return Quote{}, err
	}
	return compute(p, qty, at)
}

// Settle 校验客户端回传的报价并成交。
// 依次执行:过期检查 → 按签发版本重算比对(防汇率/费用/舍入篡改)
// → 价差风控 → 版本一致性检查。全部通过才以报价金额成交。
func (e *Engine) Settle(q Quote, at time.Time) (Settlement, error) {
	if !at.Before(q.ExpiresAt) {
		return Settlement{}, ErrQuoteExpired
	}
	e.mu.RLock()
	issued, err := e.policyByVersion(q.Pair, q.Version)
	if err != nil {
		e.mu.RUnlock()
		return Settlement{}, err
	}
	current, err := e.policyAt(q.Pair, at)
	e.mu.RUnlock()
	if err != nil {
		return Settlement{}, err
	}
	// 用签发时的策略完整重算,任何字段被改写都会暴露。
	want, err := compute(issued, q.Qty, q.IssuedAt)
	if err != nil {
		return Settlement{}, err
	}
	if !q.Rate.Equal(want.Rate) ||
		!q.Fee.Equal(want.Fee) ||
		!q.Gross.Equal(want.Gross) ||
		!q.Total.Equal(want.Total) ||
		!q.ExpiresAt.Equal(want.ExpiresAt) {
		return Settlement{}, ErrPriceMismatch
	}
	// 价差风控:|报价汇率 - 当前汇率| / 当前汇率 超过阈值即拒绝。
	// 用乘法比较避免引入除法舍入:|diff| * 10000 > bps * currentRate。
	diff := q.Rate.Sub(current.Rate)
	if diff.Sign() < 0 {
		diff = diff.Neg()
	}
	limit := current.Rate.Mul(DecimalFromInt(current.MaxDeviationBps))
	if diff.Mul(DecimalFromInt(10000)).Cmp(limit) > 0 {
		return Settlement{}, ErrExcessiveDeviation
	}
	// 策略已升版:报价版本失效,要求重新询价。
	if current.Version != q.Version {
		return Settlement{}, ErrVersionChanged
	}
	return Settlement{Quote: q, Total: q.Total, SettledAt: at}, nil
}
