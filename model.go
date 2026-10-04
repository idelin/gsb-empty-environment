package main

import (
	"fmt"
	"strings"
)

// Batch 一批积分：同一来源、同一到期时段的若干积分。
type Batch struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Amount       int    `json:"amount"`
	ExpirePeriod int    `json:"expire_period"` // 在该时段结束时到期
}

// Offer 一个可兑换的商品/权益，挂在某个时段上，库存有限。
type Offer struct {
	ID             string   `json:"id"`
	Period         int      `json:"period"`
	Cost           int      `json:"cost"`                 // 每份消耗积分
	Value          int      `json:"value"`                // 每份兑换价值
	Rebate         int      `json:"rebate"`               // 每份返还积分（下一时段才可用）
	RebateSource   string   `json:"rebate_source"`        // 返还积分的来源，默认 "rebate"
	RebateExpire   int      `json:"rebate_expire_period"` // 返还积分到期时段，默认到最后一个时段
	Inventory      int      `json:"inventory"`            // 该时段库存（份数）
	AllowedSources []string `json:"allowed_sources"`      // 允许支付的积分来源，空 = 不限
}

// Input 规划器的完整输入。
type Input struct {
	Periods int     `json:"periods"`
	Batches []Batch `json:"batches"`
	Offers  []Offer `json:"offers"`
}

func (in *Input) setDefaults() {
	for i := range in.Offers {
		if in.Offers[i].RebateSource == "" {
			in.Offers[i].RebateSource = "rebate"
		}
		if in.Offers[i].RebateExpire == 0 {
			in.Offers[i].RebateExpire = in.Periods
		}
	}
}

func (in *Input) validate() error {
	if in.Periods < 1 {
		return fmt.Errorf("periods 必须 >= 1，当前为 %d", in.Periods)
	}
	seenBatch := map[string]bool{}
	for _, b := range in.Batches {
		if b.ID == "" {
			return fmt.Errorf("批次 id 不能为空")
		}
		if strings.Contains(b.ID, "#") {
			return fmt.Errorf("批次 %q 的 id 不能包含保留字符 '#'", b.ID)
		}
		if seenBatch[b.ID] {
			return fmt.Errorf("批次 id %q 重复", b.ID)
		}
		seenBatch[b.ID] = true
		if b.Amount < 0 {
			return fmt.Errorf("批次 %q 的积分数量不能为负", b.ID)
		}
		if b.ExpirePeriod < 1 || b.ExpirePeriod > in.Periods {
			return fmt.Errorf("批次 %q 的到期时段 %d 超出 [1, %d]", b.ID, b.ExpirePeriod, in.Periods)
		}
	}
	type periodOffer struct {
		period int
		id     string
	}
	seenOffer := map[periodOffer]bool{}
	for _, o := range in.Offers {
		if o.ID == "" {
			return fmt.Errorf("兑换项 id 不能为空")
		}
		if o.Period < 1 || o.Period > in.Periods {
			return fmt.Errorf("兑换项 %q 的时段 %d 超出 [1, %d]", o.ID, o.Period, in.Periods)
		}
		if seenOffer[periodOffer{o.Period, o.ID}] {
			return fmt.Errorf("时段 %d 的兑换项 id %q 重复", o.Period, o.ID)
		}
		seenOffer[periodOffer{o.Period, o.ID}] = true
		if o.Cost <= 0 {
			return fmt.Errorf("兑换项 %q 的 cost 必须为正", o.ID)
		}
		if o.Value < 0 || o.Rebate < 0 || o.Inventory < 0 {
			return fmt.Errorf("兑换项 %q 的 value/rebate/inventory 不能为负", o.ID)
		}
		if o.RebateExpire != 0 && o.RebateExpire <= o.Period {
			return fmt.Errorf("兑换项 %q 的返还到期时段 %d 必须晚于其所在时段 %d", o.ID, o.RebateExpire, o.Period)
		}
	}
	return nil
}
