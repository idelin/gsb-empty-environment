package temporalfx

import (
	"math/big"
	"testing"
	"time"
)

var shanghai = time.FixedZone("Asia/Shanghai", 8*3600)
var london = time.FixedZone("Europe/London", 0)

func mustParse(t *testing.T, loc *time.Location, v string) time.Time {
	t.Helper()
	ts, err := ParseIn(loc, v)
	if err != nil {
		t.Fatalf("ParseIn(%q): %v", v, err)
	}
	return ts
}

// 验收场景：交易业务 10:00 发生；
// 10:05 收到 10:00 生效的汇率 A(7.20)；
// 10:10 收到对 10:00 牌价的紧急更正 B(7.25)。
// 知悉时间 10:06 查询应命中 A，10:11 查询应命中 B —— 版本跃迁。
func buildAcceptanceStore(t *testing.T) (*Store, time.Time, time.Time, time.Time) {
	t.Helper()
	s := NewStore()
	txn := mustParse(t, shanghai, "2026-10-02 10:00")
	if _, err := s.Publish("USD/CNY", txn, mustParse(t, shanghai, "2026-10-02 10:05"), "7.20", Initial, "延迟发布的10:00牌价A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("USD/CNY", txn, mustParse(t, shanghai, "2026-10-02 10:10"), "7.25", Correction, "紧急更正10:00牌价B"); err != nil {
		t.Fatal(err)
	}
	return s, txn,
		mustParse(t, shanghai, "2026-10-02 10:06"),
		mustParse(t, shanghai, "2026-10-02 10:11")
}

func TestAcceptanceVersionTransition(t *testing.T) {
	s, txn, asOfEarly, asOfLate := buildAcceptanceStore(t)

	early, err := s.Convert("USD/CNY", "1000", txn, asOfEarly)
	if err != nil {
		t.Fatalf("convert asOf 10:06: %v", err)
	}
	if early.Rate.VersionID() != "USD/CNY#1" {
		t.Fatalf("asOf 10:06 应命中版本A(#1)，实际 %s", early.Rate.VersionID())
	}
	if got := early.LocalAmount.RatString(); got != "7200" {
		t.Fatalf("asOf 10:06 本币金额应为 7200，实际 %s", got)
	}

	late, err := s.Convert("USD/CNY", "1000", txn, asOfLate)
	if err != nil {
		t.Fatalf("convert asOf 10:11: %v", err)
	}
	if late.Rate.VersionID() != "USD/CNY#2" {
		t.Fatalf("asOf 10:11 应命中更正版本B(#2)，实际 %s", late.Rate.VersionID())
	}
	if got := late.LocalAmount.RatString(); got != "7250" {
		t.Fatalf("asOf 10:11 本币金额应为 7250，实际 %s", got)
	}

	// 同一笔交易、不同时态视角 -> 不同折算依据：版本跃迁可审计。
	if early.Rate.VersionID() == late.Rate.VersionID() {
		t.Fatal("两个知悉时点应产生版本跃迁")
	}
	if log := s.AuditLog(); len(log) != 2 {
		t.Fatalf("审计日志应有 2 条，实际 %d", len(log))
	}
}

func TestUnknownBeforePublication(t *testing.T) {
	s, txn, _, _ := buildAcceptanceStore(t)
	asOf := mustParse(t, shanghai, "2026-10-02 10:04") // A 尚未知悉
	if _, err := s.Convert("USD/CNY", "1000", txn, asOf); err == nil {
		t.Fatal("10:04 查询应失败：10:00 牌价尚未知悉")
	}
}

func TestDelayedPublicationBackfillsEffectiveTime(t *testing.T) {
	s := NewStore()
	// 09:30 生效的牌价延迟到 10:05 才知悉。
	if _, err := s.Publish("USD/CNY",
		mustParse(t, shanghai, "2026-10-02 09:30"),
		mustParse(t, shanghai, "2026-10-02 10:05"),
		"7.10", Initial, "延迟发布"); err != nil {
		t.Fatal(err)
	}
	// 交易 09:45，知悉 10:06：应回溯命中 09:30 生效的牌价。
	c, err := s.Convert("USD/CNY", "500",
		mustParse(t, shanghai, "2026-10-02 09:45"),
		mustParse(t, shanghai, "2026-10-02 10:06"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LocalAmount.RatString(); got != "3550" {
		t.Fatalf("延迟发布回溯折算应为 3550，实际 %s", got)
	}
	// 交易 09:15（生效前）不可使用该牌价。
	if _, err := s.Convert("USD/CNY", "500",
		mustParse(t, shanghai, "2026-10-02 09:15"),
		mustParse(t, shanghai, "2026-10-02 10:06")); err == nil {
		t.Fatal("生效时刻前的交易不应命中该牌价")
	}
}

func TestCorrectionDoesNotRewritePastAsOf(t *testing.T) {
	s, txn, asOfEarly, _ := buildAcceptanceStore(t)
	// 更正发布后，再以更正前的知悉时间查询，仍应得到 A（双时态不可篡改）。
	c, err := s.Convert("USD/CNY", "1000", txn, asOfEarly)
	if err != nil {
		t.Fatal(err)
	}
	if c.Rate.Kind != Initial || c.Rate.Value.RatString() != "36/5" {
		t.Fatalf("历史知悉视角应仍命中 A(7.20)，实际 %v %s", c.Rate.Kind, c.Rate.Value.RatString())
	}
}

func TestMultiTimezoneSameInstant(t *testing.T) {
	s := NewStore()
	// 发布中心在伦敦（UTC），业务在上海（UTC+8）：同一物理时刻。
	effLondon := mustParse(t, london, "2026-10-02 02:00")
	effShanghai := mustParse(t, shanghai, "2026-10-02 10:00")
	if !effLondon.Equal(effShanghai) {
		t.Fatal("跨时区解析应归一到同一 UTC 时刻")
	}
	if _, err := s.Publish("USD/CNY", effLondon, effLondon, "7.20", Initial, "伦敦中心发布"); err != nil {
		t.Fatal(err)
	}
	c, err := s.Convert("USD/CNY", "100", effShanghai, effShanghai)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LocalAmount.RatString(); got != "720" {
		t.Fatalf("跨时区折算应为 720，实际 %s", got)
	}
}

func TestLatestEffectiveWins(t *testing.T) {
	s := NewStore()
	s.Publish("USD/CNY", mustParse(t, shanghai, "2026-10-02 09:00"), mustParse(t, shanghai, "2026-10-02 09:01"), "7.00", Initial, "")
	s.Publish("USD/CNY", mustParse(t, shanghai, "2026-10-02 10:00"), mustParse(t, shanghai, "2026-10-02 10:05"), "7.20", Initial, "")
	c, err := s.Convert("USD/CNY", "100",
		mustParse(t, shanghai, "2026-10-02 10:30"),
		mustParse(t, shanghai, "2026-10-02 10:06"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LocalAmount.RatString(); got != "720" {
		t.Fatalf("应命中 10:00 生效牌价 -> 720，实际 %s", got)
	}
}

func TestAuditBasisAndVersions(t *testing.T) {
	s, txn, _, asOfLate := buildAcceptanceStore(t)
	c, err := s.Convert("USD/CNY", "1000", txn, asOfLate)
	if err != nil {
		t.Fatal(err)
	}
	if c.Basis == "" || c.Rate.Kind != Correction {
		t.Fatalf("折算依据应记录更正版本，实际 basis=%q kind=%v", c.Basis, c.Rate.Kind)
	}
	vs := s.Versions("USD/CNY")
	if len(vs) != 2 || vs[0].Seq != 1 || vs[1].Seq != 2 {
		t.Fatalf("版本清单应含 #1/#2 两个版本，实际 %+v", vs)
	}
	// 金额精度：big.Rat 无浮点误差。
	if c.LocalAmount.Cmp(big.NewRat(7250, 1)) != 0 {
		t.Fatal("折算金额应精确等于 7250")
	}
}

func TestInvalidInput(t *testing.T) {
	s := NewStore()
	if _, err := s.Publish("USD/CNY", time.Now(), time.Now(), "abc", Initial, ""); err == nil {
		t.Fatal("非法汇率值应报错")
	}
	if _, err := s.Publish("USD/CNY", time.Now(), time.Now(), "-1", Initial, ""); err == nil {
		t.Fatal("非正汇率应报错")
	}
}
