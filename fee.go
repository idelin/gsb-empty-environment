package main

import (
	"fmt"
	"math/big"
	"time"
)

// FeeRule 计费规则, 全部作为输入提供。
type FeeRule struct {
	Free          time.Duration // 每段免费时长
	Unit          time.Duration // 计费单位, 每段不足一单位向上进位
	RateCents     int64         // 每计费单位价格(分)
	DailyCapCents int64         // 每个自然日封顶(分), 0 表示不封顶
}

func (r FeeRule) validate() error {
	if r.Free < 0 {
		return fmt.Errorf("免费时长不能为负")
	}
	if r.Unit < time.Second {
		return fmt.Errorf("计费单位必须不小于 1 秒")
	}
	if r.Free%time.Second != 0 || r.Unit%time.Second != 0 {
		return fmt.Errorf("免费时长与计费单位必须为整秒")
	}
	if r.RateCents < 0 {
		return fmt.Errorf("单价不能为负")
	}
	if r.DailyCapCents < 0 {
		return fmt.Errorf("日封顶不能为负")
	}
	return nil
}

// SegmentFee 计算单个停车段 [start, end] 的费用(分)。
// 语义: 计费时长 = max(0, 停车时长 - 免费时长), 按计费单位向上进位计费;
// 若设置日封顶, 则费用不超过段跨越的自然日数量 × 日封顶。
func (r FeeRule) SegmentFee(start, end time.Time) int64 {
	d := end.Sub(start)
	if d <= 0 {
		return 0
	}
	billable := d - r.Free
	if billable <= 0 {
		return 0
	}
	units := (int64(billable) + int64(r.Unit) - 1) / int64(r.Unit)
	raw := units * r.RateCents
	if r.DailyCapCents == 0 {
		return raw
	}
	if cap := daysSpanned(start, end) * r.DailyCapCents; raw > cap {
		return cap
	}
	return raw
}

// daysSpanned 段 [start, end] 跨越的自然日数量(按 start 所在时区)。
func daysSpanned(start, end time.Time) int64 {
	loc := start.Location()
	y1, m1, d1 := start.In(loc).Date()
	y2, m2, d2 := end.In(loc).Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int64(b.Sub(a)/(24*time.Hour)) + 1
}

// parseYuan 将以元为单位的十进制字符串转换为分(四舍五入到分)。
func parseYuan(s string) (int64, error) {
	rat, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("金额格式无效: %q", s)
	}
	cents := new(big.Rat).Mul(rat, big.NewRat(100, 1))
	rounded := new(big.Rat).Add(cents, big.NewRat(1, 2))
	q := new(big.Int).Quo(rounded.Num(), rounded.Denom())
	if !q.IsInt64() {
		return 0, fmt.Errorf("金额超出范围: %q", s)
	}
	return q.Int64(), nil
}

// formatCents 将分格式化为 ¥12.30 形式。
func formatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	return fmt.Sprintf("%s¥%d.%02d", sign, c/100, c%100)
}
