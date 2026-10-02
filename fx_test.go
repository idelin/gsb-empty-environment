package fxconv

import (
	"testing"
	"time"
)

// 业务日 2026-10-02，以当地时间（多时区场景下统一换算到同一时区比较）。
func at(h, m int) time.Time {
	return time.Date(2026, 10, 2, h, m, 0, 0, time.Local)
}

// 验收场景：
//   交易在业务 10:00 发生；
//   10:05 收到 10:00 生效的汇率 A（7.10）；
//   10:10 收到对 10:00 牌价的紧急更正汇率 B（7.12）；
//   分别按知悉时间 10:06 与 10:11 查询，应观察到 A -> B 的版本跃迁。
func TestAcceptanceCorrectionTransition(t *testing.T) {
	s := NewStore()
	a := s.Publish("USD/CNY", 7.10, at(10, 0), at(10, 5), "10:00 牌价")
	b := s.Correct("USD/CNY", 7.12, at(10, 0), at(10, 10), "紧急更正 10:00 牌价")

	// 知悉时刻 10:06：更正尚未知悉，采用 A。
	c1, err := s.Convert("USD/CNY", 1000, at(10, 0), at(10, 6))
	if err != nil {
		t.Fatalf("10:06 查询失败: %v", err)
	}
	if c1.Applied.ID != a.ID || c1.Applied.Rate != 7.10 {
		t.Fatalf("10:06 应采用 A(v%d, 7.10)，实际 %+v", a.ID, c1.Applied)
	}
	if c1.HomeAmount != 7100 {
		t.Fatalf("10:06 折算金额应为 7100，实际 %v", c1.HomeAmount)
	}
	if len(c1.Superseded) != 0 {
		t.Fatalf("10:06 不应有被覆盖版本，实际 %+v", c1.Superseded)
	}

	// 知悉时刻 10:11：更正已知悉，跃迁到 B，A 进入被覆盖清单。
	c2, err := s.Convert("USD/CNY", 1000, at(10, 0), at(10, 11))
	if err != nil {
		t.Fatalf("10:11 查询失败: %v", err)
	}
	if c2.Applied.ID != b.ID || c2.Applied.Rate != 7.12 {
		t.Fatalf("10:11 应采用 B(v%d, 7.12)，实际 %+v", b.ID, c2.Applied)
	}
	if c2.HomeAmount != 7120 {
		t.Fatalf("10:11 折算金额应为 7120，实际 %v", c2.HomeAmount)
	}
	if len(c2.Superseded) != 1 || c2.Superseded[0].ID != a.ID {
		t.Fatalf("10:11 被覆盖版本应为 A(v%d)，实际 %+v", a.ID, c2.Superseded)
	}

	// 同一笔交易、不同知悉时刻结果不同，但历史完整保留。
	if len(s.History()) != 2 {
		t.Fatalf("版本库应保留 2 条历史，实际 %d", len(s.History()))
	}
}

// 延迟发布：牌价 09:30 生效，但 10:08 才送达本中心。
// 10:06 查询时只能回退到更早的已知牌价；10:09 查询时跃迁到 09:30 牌价。
func TestDelayedPublication(t *testing.T) {
	s := NewStore()
	s.Publish("USD/CNY", 7.00, at(9, 0), at(9, 1), "09:00 牌价")
	late := s.Publish("USD/CNY", 7.05, at(9, 30), at(10, 8), "09:30 牌价（延迟送达）")

	early, err := s.Convert("USD/CNY", 100, at(10, 0), at(10, 6))
	if err != nil {
		t.Fatal(err)
	}
	if early.Applied.Rate != 7.00 {
		t.Fatalf("10:06 应回退到 09:00 牌价 7.00，实际 %+v", early.Applied)
	}

	later, err := s.Convert("USD/CNY", 100, at(10, 0), at(10, 9))
	if err != nil {
		t.Fatal(err)
	}
	if later.Applied.ID != late.ID || later.Applied.Rate != 7.05 {
		t.Fatalf("10:09 应采用延迟送达的 09:30 牌价，实际 %+v", later.Applied)
	}
}

// 交易发生时尚无任何已知有效汇率，应返回 ErrNoRate。
func TestNoKnownRate(t *testing.T) {
	s := NewStore()
	s.Publish("USD/CNY", 7.10, at(10, 0), at(10, 5), "")
	if _, err := s.Convert("USD/CNY", 100, at(10, 0), at(10, 4)); err != ErrNoRate {
		t.Fatalf("10:04 知悉时刻应返回 ErrNoRate，实际 %v", err)
	}
	if _, err := s.Convert("EUR/CNY", 100, at(10, 0), at(10, 6)); err != ErrNoRate {
		t.Fatalf("未知货币对应返回 ErrNoRate，实际 %v", err)
	}
}

// 未来生效的牌价不可用于更早的交易。
func TestFutureValidFromNotApplied(t *testing.T) {
	s := NewStore()
	s.Publish("USD/CNY", 7.00, at(9, 0), at(9, 1), "")
	s.Publish("USD/CNY", 7.20, at(11, 0), at(9, 2), "预告 11:00 牌价")

	c, err := s.Convert("USD/CNY", 100, at(10, 0), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if c.Applied.Rate != 7.00 {
		t.Fatalf("11:00 生效的牌价不应用于 10:00 的交易，实际 %+v", c.Applied)
	}
}

// 多次更正：同一 ValidFrom 下最新知悉者胜出，其余全部进入被覆盖清单。
func TestMultipleCorrections(t *testing.T) {
	s := NewStore()
	v1 := s.Publish("USD/CNY", 7.10, at(10, 0), at(10, 5), "")
	v2 := s.Correct("USD/CNY", 7.11, at(10, 0), at(10, 7), "")
	v3 := s.Correct("USD/CNY", 7.12, at(10, 0), at(10, 9), "")

	c, err := s.Convert("USD/CNY", 100, at(10, 0), at(10, 30))
	if err != nil {
		t.Fatal(err)
	}
	if c.Applied.ID != v3.ID {
		t.Fatalf("应采用最新更正 v%d，实际 %+v", v3.ID, c.Applied)
	}
	if len(c.Superseded) != 2 {
		t.Fatalf("应有 2 条被覆盖版本，实际 %+v", c.Superseded)
	}
	ids := map[int]bool{c.Superseded[0].ID: true, c.Superseded[1].ID: true}
	if !ids[v1.ID] || !ids[v2.ID] {
		t.Fatalf("被覆盖清单应包含 v%d 与 v%d，实际 %+v", v1.ID, v2.ID, c.Superseded)
	}
}
