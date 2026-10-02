// Package fxconv 实现可审计的跨国交易多时区汇率时态折算器。
//
// 每个汇率版本携带两个时间维度：
//   - ValidFrom：牌价生效时间（业务时间，何时起对市场有效）
//   - KnownAt：发布中心知悉时间（系统时间，本中心何时收到该牌价）
//
// 折算规则：对发生于 txnTime 的交易，在知悉时刻 asOf 查询时，
// 选用「KnownAt <= asOf 且 ValidFrom <= txnTime」的版本中
// ValidFrom 最新者；同一 ValidFrom 下取 KnownAt 最新者（紧急更正覆盖原牌价）。
// 存储为只追加（append-only），全部历史版本保留，折算结果附带完整依据。
package fxconv

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Kind 区分版本来源：正常发布或紧急更正。
type Kind string

const (
	KindPublish    Kind = "PUBLISH"
	KindCorrection Kind = "CORRECTION"
)

// ErrNoRate 表示在给定知悉时刻没有任何已知且已生效的汇率。
var ErrNoRate = errors.New("fxconv: no known effective rate")

// RateVersion 是一条不可变的汇率版本记录。
type RateVersion struct {
	ID        int       // 单调递增的版本号（审计序号）
	Pair      string    // 货币对，如 "USD/CNY"
	Rate      float64   // 1 单位外币折合本币数量
	ValidFrom time.Time // 牌价生效时间
	KnownAt   time.Time // 发布中心知悉时间
	Kind      Kind      // 发布 / 更正
	Note      string    // 备注（如更正原因）
}

func (v RateVersion) String() string {
	return fmt.Sprintf("v%d[%s] %s=%.4f 生效=%s 知悉=%s %s",
		v.ID, v.Kind, v.Pair, v.Rate,
		v.ValidFrom.Format("15:04:05"), v.KnownAt.Format("15:04:05"), v.Note)
}

// Store 是只追加的汇率版本库，线程安全。
type Store struct {
	mu       sync.RWMutex
	versions []RateVersion
}

func NewStore() *Store { return &Store{} }

// Publish 登记一条正常发布的牌价。
func (s *Store) Publish(pair string, rate float64, validFrom, knownAt time.Time, note string) RateVersion {
	return s.append(pair, rate, validFrom, knownAt, KindPublish, note)
}

// Correct 登记一条紧急更正牌价（覆盖同一生效时间的先前版本）。
func (s *Store) Correct(pair string, rate float64, validFrom, knownAt time.Time, note string) RateVersion {
	return s.append(pair, rate, validFrom, knownAt, KindCorrection, note)
}

func (s *Store) append(pair string, rate float64, validFrom, knownAt time.Time, kind Kind, note string) RateVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := RateVersion{
		ID:        len(s.versions) + 1,
		Pair:      pair,
		Rate:      rate,
		ValidFrom: validFrom,
		KnownAt:   knownAt,
		Kind:      kind,
		Note:      note,
	}
	s.versions = append(s.versions, v)
	return v
}

// History 返回全部版本（只读副本），用于审计。
func (s *Store) History() []RateVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RateVersion, len(s.versions))
	copy(out, s.versions)
	return out
}

// Conversion 是一次折算的结果及其完整依据。
type Conversion struct {
	Pair          string
	ForeignAmount float64       // 外币金额
	HomeAmount    float64       // 折算后的本币金额
	TxnTime       time.Time     // 交易发生时刻
	AsOf          time.Time     // 查询所用的知悉时刻
	Applied       RateVersion   // 实际采用的汇率版本
	Superseded    []RateVersion // 同一生效时间下被更正覆盖的旧版本
}

func (c Conversion) String() string {
	s := fmt.Sprintf("交易时刻=%s 知悉时刻=%s | %.2f %s × %.4f = %.2f 本币 | 采用 %s",
		c.TxnTime.Format("15:04:05"), c.AsOf.Format("15:04:05"),
		c.ForeignAmount, c.Pair, c.Applied.Rate, c.HomeAmount, c.Applied)
	for _, old := range c.Superseded {
		s += fmt.Sprintf("\n    被覆盖版本: %s", old)
	}
	return s
}

// Convert 按「交易发生时刻最近已知有效汇率」折算外币金额为本币。
//
// 候选版本需同时满足 KnownAt <= asOf（查询时已知悉）与
// ValidFrom <= txnTime（交易时已生效）；在其中取 ValidFrom 最新者，
// 若并列则取 KnownAt 最新者（更正优先），再并列取版本号最大者。
func (s *Store) Convert(pair string, foreignAmount float64, txnTime, asOf time.Time) (Conversion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best *RateVersion
	var superseded []RateVersion
	for i := range s.versions {
		v := s.versions[i]
		if v.Pair != pair || v.KnownAt.After(asOf) || v.ValidFrom.After(txnTime) {
			continue
		}
		if best == nil || less(*best, v) {
			if best != nil && best.ValidFrom.Equal(v.ValidFrom) {
				superseded = append(superseded, *best)
			} else {
				superseded = superseded[:0]
			}
			best = &s.versions[i]
		} else if best.ValidFrom.Equal(v.ValidFrom) {
			superseded = append(superseded, v)
		}
	}
	if best == nil {
		return Conversion{}, ErrNoRate
	}
	return Conversion{
		Pair:          pair,
		ForeignAmount: foreignAmount,
		HomeAmount:    foreignAmount * best.Rate,
		TxnTime:       txnTime,
		AsOf:          asOf,
		Applied:       *best,
		Superseded:    superseded,
	}, nil
}

// less 定义版本优先级：先生效时间，后知悉时间，再版本号。
func less(a, b RateVersion) bool {
	if !a.ValidFrom.Equal(b.ValidFrom) {
		return a.ValidFrom.Before(b.ValidFrom)
	}
	if !a.KnownAt.Equal(b.KnownAt) {
		return a.KnownAt.Before(b.KnownAt)
	}
	return a.ID < b.ID
}
