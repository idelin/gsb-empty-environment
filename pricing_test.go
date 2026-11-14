package netsec

import (
	"errors"
	"testing"
	"time"
)

var testStart = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// newTestEngine 构造 USD/CNY 策略:汇率 7.10,费率 0.1% + 固定 0.05,
// 计价币最小单位 0.01,HALF_UP 舍入,报价有效期 5 分钟,价差阈值 50 基点。
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	err := e.SetPolicy(PairPolicy{
		Pair:            "USD/CNY",
		Version:         "v1",
		Rate:            MustDecimal("7.10"),
		Fee:             FeeSchedule{Percent: MustDecimal("0.001"), Fixed: MustDecimal("0.05")},
		PriceScale:      2,
		QtyScale:        4,
		Rounding:        RoundHalfUp,
		MinQty:          MustDecimal("0.01"),
		MaxQty:          MustDecimal("1000000"),
		ValidFrom:       testStart,
		ValidUntil:      testStart.Add(24 * time.Hour),
		QuoteTTL:        5 * time.Minute,
		MaxDeviationBps: 50,
	})
	if err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	return e
}

// 合法报价:服务端签发后原样回传,成交价与报价完全一致。
func TestSettleValidQuote(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100.25"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	// 手工核算:gross = 100.25 * 7.10 = 711.775 -> 711.78 (HALF_UP)
	// fee = 711.78 * 0.001 = 0.71178 -> 0.71, + 0.05 = 0.76
	// total = 711.78 + 0.76 = 712.54
	if got := q.Gross.String(); got != "711.78" {
		t.Fatalf("gross = %s, want 711.78", got)
	}
	if got := q.Fee.String(); got != "0.76" {
		t.Fatalf("fee = %s, want 0.76", got)
	}
	if got := q.Total.String(); got != "712.54" {
		t.Fatalf("total = %s, want 712.54", got)
	}
	if q.Version != "v1" {
		t.Fatalf("version = %s, want v1", q.Version)
	}

	s, err := e.Settle(q, at.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !s.Total.Equal(q.Total) {
		t.Fatalf("settled %s != quoted %s", s.Total, q.Total)
	}
}

// 汇率改写:攻击者把回传报价的汇率与总额改低,服务端重算发现不一致。
func TestSettleRateTampering(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	// 篡改:汇率 7.10 -> 6.50,总额跟着改小。
	q.Rate = MustDecimal("6.50")
	q.Gross = MustDecimal("650.00")
	q.Total = MustDecimal("650.70")

	if _, err := e.Settle(q, at.Add(time.Minute)); !errors.Is(err, ErrPriceMismatch) {
		t.Fatalf("err = %v, want ErrPriceMismatch", err)
	}
}

// 舍入操纵:攻击者改用向下舍入并缩小最小单位,压低成交价。
func TestSettleRoundingManipulation(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100.25"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	// 正确结果 711.78 (HALF_UP);攻击者按 RoundDown 得 711.77。
	q.Gross = MustDecimal("711.77")
	q.Total = MustDecimal("712.53")
	if _, err := e.Settle(q, at.Add(time.Minute)); !errors.Is(err, ErrPriceMismatch) {
		t.Fatalf("RoundDown tamper: err = %v, want ErrPriceMismatch", err)
	}

	// 篡改最小单位:用 4 位小数报价,绕开 0.01 归约。
	q2, err := e.Quote("USD/CNY", MustDecimal("100.25"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	q2.Gross = MustDecimal("711.7750")
	q2.Total = MustDecimal("712.4912")
	if _, err := e.Settle(q2, at.Add(time.Minute)); !errors.Is(err, ErrPriceMismatch) {
		t.Fatalf("scale tamper: err = %v, want ErrPriceMismatch", err)
	}
}

// 报价过期:超过 ExpiresAt 的报价一律拒绝。
func TestSettleExpiredQuote(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if _, err := e.Settle(q, q.ExpiresAt); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("at expiry: err = %v, want ErrQuoteExpired", err)
	}
	if _, err := e.Settle(q, q.ExpiresAt.Add(time.Hour)); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("past expiry: err = %v, want ErrQuoteExpired", err)
	}
}

// 版本变更:策略升版后旧版本报价必须重新询价。
func TestSettleVersionChange(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	// 策略升版:汇率微调 0.1%(在价差阈值内),版本 v1 -> v2。
	err = e.SetPolicy(PairPolicy{
		Pair:            "USD/CNY",
		Version:         "v2",
		Rate:            MustDecimal("7.1071"),
		Fee:             FeeSchedule{Percent: MustDecimal("0.001"), Fixed: MustDecimal("0.05")},
		PriceScale:      2,
		QtyScale:        4,
		Rounding:        RoundHalfUp,
		MinQty:          MustDecimal("0.01"),
		MaxQty:          MustDecimal("1000000"),
		ValidFrom:       at.Add(2 * time.Minute),
		ValidUntil:      testStart.Add(24 * time.Hour),
		QuoteTTL:        5 * time.Minute,
		MaxDeviationBps: 50,
	})
	if err != nil {
		t.Fatalf("SetPolicy v2: %v", err)
	}
	if _, err := e.Settle(q, at.Add(3*time.Minute)); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("err = %v, want ErrVersionChanged", err)
	}
	// 用新版本重新询价则可正常成交。
	q2, err := e.Quote("USD/CNY", MustDecimal("100"), at.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("re-quote: %v", err)
	}
	if q2.Version != "v2" {
		t.Fatalf("version = %s, want v2", q2.Version)
	}
	if _, err := e.Settle(q2, at.Add(4*time.Minute)); err != nil {
		t.Fatalf("Settle v2: %v", err)
	}
}

// 异常价差:市场汇率剧烈波动超过阈值,即使报价未过期也拒绝成交。
func TestSettleExcessiveDeviation(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)

	q, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	// 汇率从 7.10 跳到 7.30(约 2.8%,远超 50 基点阈值),同版本更新。
	err = e.SetPolicy(PairPolicy{
		Pair:            "USD/CNY",
		Version:         "v1",
		Rate:            MustDecimal("7.30"),
		Fee:             FeeSchedule{Percent: MustDecimal("0.001"), Fixed: MustDecimal("0.05")},
		PriceScale:      2,
		QtyScale:        4,
		Rounding:        RoundHalfUp,
		MinQty:          MustDecimal("0.01"),
		MaxQty:          MustDecimal("1000000"),
		ValidFrom:       testStart,
		ValidUntil:      testStart.Add(24 * time.Hour),
		QuoteTTL:        5 * time.Minute,
		MaxDeviationBps: 50,
	})
	if err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	// 同版本策略被替换后,重算结果与旧报价不一致,先被重算比对拦截。
	if _, err := e.Settle(q, at.Add(time.Minute)); !errors.Is(err, ErrPriceMismatch) {
		t.Fatalf("err = %v, want ErrPriceMismatch", err)
	}

	// 重新签发 7.30 的报价后,旧报价(7.10)已无法通过重算;
	// 直接验证价差检查:用新报价回传,再让汇率回落到阈值之外。
	q2, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("re-quote: %v", err)
	}
	p, _ := e.policyAt("USD/CNY", at)
	p.Rate = MustDecimal("6.90") // 相对 7.30 偏离约 5.5%
	if err := e.SetPolicy(p); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if _, err := e.Settle(q2, at.Add(time.Minute)); !errors.Is(err, ErrPriceMismatch) {
		t.Fatalf("err = %v, want ErrPriceMismatch", err)
	}

	// 纯价差场景:报价按当前策略正确签发,成交前汇率再次大幅跳动,
	// 此时重算一致(策略同版本同参数),由价差风控拒绝。
	q3, err := e.Quote("USD/CNY", MustDecimal("100"), at)
	if err != nil {
		t.Fatalf("re-quote: %v", err)
	}
	p.Rate = MustDecimal("7.30")
	p.ValidFrom = at.Add(2 * time.Minute) // 未来窗口,成交时刻才生效
	p.Version = "v1b"
	if err := e.SetPolicy(p); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if _, err := e.Settle(q3, at.Add(3*time.Minute)); !errors.Is(err, ErrExcessiveDeviation) {
		t.Fatalf("err = %v, want ErrExcessiveDeviation", err)
	}
}

// 数量精度超限:客户端提交超出最小单位的小数位,签发即拒绝。
func TestQuoteQtyPrecision(t *testing.T) {
	e := newTestEngine(t)
	at := testStart.Add(time.Minute)
	if _, err := e.Quote("USD/CNY", MustDecimal("1.00001"), at); !errors.Is(err, ErrQtyOutOfRange) {
		t.Fatalf("err = %v, want ErrQtyOutOfRange", err)
	}
	if _, err := e.Quote("USD/CNY", MustDecimal("0"), at); !errors.Is(err, ErrQtyOutOfRange) {
		t.Fatalf("err = %v, want ErrQtyOutOfRange", err)
	}
}

// 未知币种对与无生效策略的时段。
func TestQuoteNoPolicy(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.Quote("EUR/USD", MustDecimal("1"), testStart); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("err = %v, want ErrUnknownPair", err)
	}
	if _, err := e.Quote("USD/CNY", MustDecimal("1"), testStart.Add(-time.Hour)); !errors.Is(err, ErrNoActivePolicy) {
		t.Fatalf("err = %v, want ErrNoActivePolicy", err)
	}
}
