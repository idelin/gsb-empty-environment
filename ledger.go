// Package pricing 实现双时态价格规则引擎。
//
// 每条价格规则携带两个时间维度：
//   - Valid Time（业务生效时间）：价格业务上生效的区间，左闭右开 [ValidFrom, ValidTo)。
//   - Transaction Time（系统知悉时间）：系统何时知悉该规则，左闭右开 [TxFrom, TxTo)。
//
// 查询 PriceAt(validAt, txAt) 同时按两个维度过滤；多条规则重叠时，
// 以知悉时间更晚（TxFrom 更大）的规则为准。撤销（Retract）只是将既有
// 记录的 TxTo 关闭到撤销时刻，因此只影响撤销发生之后的知悉时间查询，
// 历史知悉时间下的查询结果保持不变（可“回到过去”）。
//
// 金额一律使用整数分（int64）。
package pricing

import (
	"errors"
	"fmt"
	"math"
)

// ErrNotFound 表示在给定双时态坐标下没有任何价格规则命中。
var ErrNotFound = errors.New("pricing: no price rule covers the given valid/transaction time")

// Record 是一条价格规则的不可变快照（撤销通过追加 TxTo 实现，不改写历史）。
type Record struct {
	ValidFrom int64 // 业务生效起点（含）
	ValidTo   int64 // 业务生效终点（不含）
	TxFrom    int64 // 系统知悉起点（含）
	TxTo      int64 // 系统知悉终点（不含）；未撤销时为 MaxInt64
	Price     int64 // 金额，整数分
	seq       int   // 插入序号，用于知悉时间相同时的后入为主
}

// Covers 报告该记录是否覆盖双时态坐标 (validAt, txAt)。
func (r Record) Covers(validAt, txAt int64) bool {
	return r.ValidFrom <= validAt && validAt < r.ValidTo &&
		r.TxFrom <= txAt && txAt < r.TxTo
}

// Ledger 是双时态价格账本。零值即可使用；非并发安全。
type Ledger struct {
	records []Record
}

// AddRule 在知悉时间 txAt 登记一条价格规则：
// price（整数分）在业务区间 [validFrom, validTo) 内生效。
func (l *Ledger) AddRule(validFrom, validTo, price, txAt int64) {
	l.records = append(l.records, Record{
		ValidFrom: validFrom,
		ValidTo:   validTo,
		TxFrom:    txAt,
		TxTo:      math.MaxInt64,
		Price:     price,
		seq:       len(l.records),
	})
}

// Retract 在知悉时间 txAt 撤销所有当前仍开放（TxTo 未关闭）的规则。
// 撤销只把记录的知悉终点关闭到 txAt，不删除历史，
// 因此 txAt 之前的知悉时间查询结果不受影响。
func (l *Ledger) Retract(txAt int64) {
	for i := range l.records {
		if l.records[i].TxTo == math.MaxInt64 && l.records[i].TxFrom < txAt {
			l.records[i].TxTo = txAt
		}
	}
}

// PriceAt 返回业务时间 validAt、知悉时间 txAt 下的价格（整数分）。
// 多条规则重叠时，知悉时间更晚者胜出；知悉时间相同则后登记者胜出。
// 无命中时返回 ErrNotFound。
func (l *Ledger) PriceAt(validAt, txAt int64) (int64, error) {
	best := -1
	for i := range l.records {
		r := l.records[i]
		if !r.Covers(validAt, txAt) {
			continue
		}
		if best == -1 ||
			r.TxFrom > l.records[best].TxFrom ||
			(r.TxFrom == l.records[best].TxFrom && r.seq > l.records[best].seq) {
			best = i
		}
	}
	if best == -1 {
		return 0, ErrNotFound
	}
	return l.records[best].Price, nil
}

// MustPriceAt 是 PriceAt 的便捷包装，未命中时 panic，主要用于样例与测试。
func (l *Ledger) MustPriceAt(validAt, txAt int64) int64 {
	p, err := l.PriceAt(validAt, txAt)
	if err != nil {
		panic(fmt.Sprintf("pricing: %v", err))
	}
	return p
}
