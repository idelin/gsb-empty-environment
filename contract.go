// Package main 实现双时态（业务时间 valid time × 知悉/事务时间 transaction time）
// 合同履约状态机：条款时态变更、交付凭证补交与作废、任意双时态切片完成度计算。
package main

import (
	"errors"
	"fmt"
	"sort"
)

// farFuture 表示事务时间区间的开端上界（记录仍然有效）。
const farFuture = int64(1) << 62

// State 合同在业务时间轴上的生命周期状态。
type State string

const (
	StateDraft      State = "草案"
	StateEffective  State = "生效"
	StateInProgress State = "履行中"
	StateTerminated State = "终止"
)

// Terms 合同条款：业务期 [EffectiveFrom, End)，以及状态迁移节点与合同总量。
type Terms struct {
	EffectiveFrom int64   // 生效起点（业务时间）
	PerformStart  int64   // 履行中起点（业务时间）
	End           int64   // 终止点（业务时间，半开区间）
	TotalAmount   float64 // 合同应交付总量（完成度分母）
}

func (t Terms) classify(business int64) State {
	switch {
	case business < t.EffectiveFrom:
		return StateDraft
	case business < t.PerformStart:
		return StateEffective
	case business < t.End:
		return StateInProgress
	default:
		return StateTerminated
	}
}

// termsVersion 条款的一个知悉时间版本：自 txFrom 起被系统知悉。
type termsVersion struct {
	terms  Terms
	txFrom int64
}

// Voucher 交付凭证：双时态事实。
// BusinessTime 为交付发生的业务时间；[TxFrom, TxTo) 为系统知悉该事实的事务时间区间。
type Voucher struct {
	ID           int
	BusinessTime int64
	Amount       float64
	TxFrom       int64
	TxTo         int64
}

// activeAt 判断凭证在知悉时间 k 是否有效（已录入且未作废）。
func (v *Voucher) activeAt(k int64) bool {
	return v.TxFrom <= k && k < v.TxTo
}

// Contract 双时态合同：条款版本链 + 交付凭证双时态日志。
type Contract struct {
	terms    []termsVersion // 按 txFrom 升序
	vouchers []*Voucher
	nextID   int
}

// NewContract 在知悉时间 tx 以初始条款创建合同。
func NewContract(tx int64, t Terms) *Contract {
	return &Contract{terms: []termsVersion{{terms: t, txFrom: tx}}}
}

// AmendTerms 在知悉时间 tx 录入条款变更（时态改约，不改写历史）。
func (c *Contract) AmendTerms(tx int64, t Terms) {
	c.terms = append(c.terms, termsVersion{terms: t, txFrom: tx})
}

// termsAsOf 返回知悉时间 k 下系统所知的最新条款。
func (c *Contract) termsAsOf(k int64) Terms {
	best := c.terms[0]
	for _, v := range c.terms {
		if v.txFrom <= k && v.txFrom >= best.txFrom {
			best = v
		}
	}
	return best.terms
}

// RecordDelivery 在知悉时间 tx 补录一张业务时间为 businessTime 的交付凭证。
// 支持补交历史阶段：businessTime 可以早于 tx。
func (c *Contract) RecordDelivery(tx, businessTime int64, amount float64) (int, error) {
	if amount <= 0 {
		return 0, errors.New("交付量必须为正")
	}
	c.nextID++
	c.vouchers = append(c.vouchers, &Voucher{
		ID:           c.nextID,
		BusinessTime: businessTime,
		Amount:       amount,
		TxFrom:       tx,
		TxTo:         farFuture,
	})
	return c.nextID, nil
}

// VoidDelivery 在知悉时间 tx 作废一张凭证（关闭其事务时间区间，不删除历史）。
func (c *Contract) VoidDelivery(tx int64, id int) error {
	for _, v := range c.vouchers {
		if v.ID == id {
			if v.TxTo != farFuture {
				return fmt.Errorf("凭证 %d 已作废", id)
			}
			v.TxTo = tx
			return nil
		}
	}
	return fmt.Errorf("凭证 %d 不存在", id)
}

// StateAt 返回双时态切片 (business, knowledge) 下的合同状态。
func (c *Contract) StateAt(business, knowledge int64) State {
	return c.termsAsOf(knowledge).classify(business)
}

// CompletionAt 返回双时态切片 (business, knowledge) 下的履约完成度百分比。
// 分子：知悉时间 k 下有效、且业务时间落在合同业务期内且不晚于 business 的交付总量；
// 分母：知悉时间 k 下条款规定的合同总量。结果截断到 [0, 100]。
func (c *Contract) CompletionAt(business, knowledge int64) float64 {
	t := c.termsAsOf(knowledge)
	if t.TotalAmount <= 0 {
		return 0
	}
	var delivered float64
	for _, v := range c.vouchers {
		if !v.activeAt(knowledge) {
			continue
		}
		if v.BusinessTime < t.EffectiveFrom || v.BusinessTime >= t.End {
			continue
		}
		if v.BusinessTime > business {
			continue
		}
		delivered += v.Amount
	}
	pct := delivered / t.TotalAmount * 100
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}

// VouchersAsOf 返回知悉时间 k 下有效的凭证快照（按业务时间排序），用于审计与演示。
func (c *Contract) VouchersAsOf(knowledge int64) []Voucher {
	var out []Voucher
	for _, v := range c.vouchers {
		if v.activeAt(knowledge) {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BusinessTime < out[j].BusinessTime })
	return out
}
