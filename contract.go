// Package contract 实现双时态（业务时间 × 知悉时间）合同履约状态机。
//
// 业务时间（valid time）：合同条款与交付事实在现实世界中生效的时间轴。
// 知悉时间（transaction time）：系统获知并录入事实的时间轴。
//
// 所有交付凭证以追加方式存储，作废只是关闭其知悉区间 [KnownFrom, KnownTo)，
// 因此可以重建任意历史知悉时刻下的合同视图，并支持对历史业务阶段的
// 交付凭证进行补交与作废。
package contract

import (
	"errors"
	"fmt"
	"math"
)

// State 合同在业务时间轴上的履约状态。
type State int

const (
	Draft      State = iota // 草案：业务时间尚未进入合同有效期
	Effective               // 生效：已进入有效期，但尚无已知交付
	InProgress              // 履行中：已有至少一笔已知交付
	Terminated              // 终止：业务时间越过合同有效期终点
)

func (s State) String() string {
	switch s {
	case Draft:
		return "草案"
	case Effective:
		return "生效"
	case InProgress:
		return "履行中"
	case Terminated:
		return "终止"
	default:
		return "未知"
	}
}

// Delivery 一笔交付凭证的双时态记录。
// 业务时间 BusinessTime 表示交付实际发生（归属）的业务时刻；
// 知悉区间 [KnownFrom, KnownTo) 表示系统从何时起知悉该凭证、何时将其作废。
type Delivery struct {
	ID           int64
	BusinessTime int64
	Quantity     int64
	KnownFrom    int64
	KnownTo      int64 // math.MaxInt64 表示至今仍有效
}

// KnownAt 报告该凭证在知悉时刻 k 是否对系统可见。
func (d *Delivery) KnownAt(k int64) bool {
	return d.KnownFrom <= k && k < d.KnownTo
}

// Contract 双时态合同：业务有效期为 [ValidFrom, ValidTo)。
type Contract struct {
	ValidFrom     int64
	ValidTo       int64
	RequiredTotal int64 // 履约完成度 100% 所需的总交付量

	knowledgeNow int64 // 系统当前知悉时间
	nextID       int64
	deliveries   []*Delivery // 追加式历史，永不物理删除
}

// NewContract 创建业务有效期为 [validFrom, validTo) 的合同。
// requiredTotal 为完成度达到 100% 所需的累计交付量。
func NewContract(validFrom, validTo, requiredTotal int64) (*Contract, error) {
	if validFrom >= validTo {
		return nil, fmt.Errorf("合同业务期非法: [%d, %d)", validFrom, validTo)
	}
	if requiredTotal <= 0 {
		return nil, fmt.Errorf("合同总量必须为正: %d", requiredTotal)
	}
	return &Contract{
		ValidFrom:     validFrom,
		ValidTo:       validTo,
		RequiredTotal: requiredTotal,
		knowledgeNow:  0,
		nextID:        1,
	}, nil
}

// AdvanceKnowledge 将系统知悉时钟推进到 t（只允许向前）。
func (c *Contract) AdvanceKnowledge(t int64) error {
	if t < c.knowledgeNow {
		return fmt.Errorf("知悉时间不可回退: 当前 %d, 目标 %d", c.knowledgeNow, t)
	}
	c.knowledgeNow = t
	return nil
}

// KnowledgeNow 返回当知悉时钟。
func (c *Contract) KnowledgeNow() int64 { return c.knowledgeNow }

// RecordDelivery 在当前知悉时刻录入一笔归属业务时间 businessTime 的交付，
// 支持对历史业务阶段补交。返回凭证 ID。
func (c *Contract) RecordDelivery(businessTime, quantity int64) (int64, error) {
	if businessTime < c.ValidFrom || businessTime >= c.ValidTo {
		return 0, fmt.Errorf("交付业务时间 %d 超出合同业务期 [%d, %d)",
			businessTime, c.ValidFrom, c.ValidTo)
	}
	if quantity <= 0 {
		return 0, fmt.Errorf("交付量必须为正: %d", quantity)
	}
	d := &Delivery{
		ID:           c.nextID,
		BusinessTime: businessTime,
		Quantity:     quantity,
		KnownFrom:    c.knowledgeNow,
		KnownTo:      math.MaxInt64,
	}
	c.nextID++
	c.deliveries = append(c.deliveries, d)
	return d.ID, nil
}

// ErrDeliveryNotFound 凭证不存在。
var ErrDeliveryNotFound = errors.New("交付凭证不存在")

// ErrAlreadyVoided 凭证已被作废。
var ErrAlreadyVoided = errors.New("交付凭证已作废")

// VoidDelivery 在当前知悉时刻作废一笔交付凭证（关闭其知悉区间）。
func (c *Contract) VoidDelivery(id int64) error {
	for _, d := range c.deliveries {
		if d.ID == id {
			if d.KnownTo != math.MaxInt64 {
				return ErrAlreadyVoided
			}
			d.KnownTo = c.knowledgeNow
			return nil
		}
	}
	return ErrDeliveryNotFound
}

// DeliveredAt 计算在知悉时刻 k、业务时刻 t 的切片下，
// 系统已知且业务时间不晚于 t 的累计交付量。
func (c *Contract) DeliveredAt(knowledge, businessTime int64) int64 {
	var total int64
	for _, d := range c.deliveries {
		if d.KnownAt(knowledge) && d.BusinessTime <= businessTime {
			total += d.Quantity
		}
	}
	return total
}

// StateAt 返回双时态切片 (知悉 k, 业务 t) 下的合同状态。
func (c *Contract) StateAt(knowledge, businessTime int64) State {
	switch {
	case businessTime < c.ValidFrom:
		return Draft
	case businessTime >= c.ValidTo:
		return Terminated
	case c.DeliveredAt(knowledge, businessTime) > 0:
		return InProgress
	default:
		return Effective
	}
}

// CompletionAt 返回双时态切片 (知悉 k, 业务 t) 下的履约完成度百分比 [0, 100]。
func (c *Contract) CompletionAt(knowledge, businessTime int64) float64 {
	delivered := c.DeliveredAt(knowledge, businessTime)
	pct := float64(delivered) / float64(c.RequiredTotal) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

// Snapshot 某一双时态切片下的完整视图。
type Snapshot struct {
	Knowledge  int64
	Business   int64
	State      State
	Delivered  int64
	Completion float64
}

// SnapshotAt 实时计算任意双时态切片下的状态与完成度。
func (c *Contract) SnapshotAt(knowledge, businessTime int64) Snapshot {
	return Snapshot{
		Knowledge:  knowledge,
		Business:   businessTime,
		State:      c.StateAt(knowledge, businessTime),
		Delivered:  c.DeliveredAt(knowledge, businessTime),
		Completion: c.CompletionAt(knowledge, businessTime),
	}
}
