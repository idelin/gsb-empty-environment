// Package bitemporal 实现双时间版本库：
// 同时记录业务生效区间（valid time）与系统获知区间（transaction time）。
// 更正历史时只允许关闭旧系统区间并追加新版本（append-only），
// 业务区间可拆分，且同一键在同一系统切面内禁止重叠生效。
package bitemporal

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Inf 表示开区间的无穷上界（validTo / txTo 的“至今”）。
var Inf = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Version 是一条双时间版本记录。所有区间均为左闭右开 [from, to)。
type Version struct {
	Key       string
	Value     string
	ValidFrom time.Time // 业务生效起点
	ValidTo   time.Time // 业务生效终点（不含）
	TxFrom    time.Time // 系统获知起点
	TxTo      time.Time // 系统获知终点（不含），Inf 表示当前版本
}

// EffectiveAt 判断业务时间点 t 是否落在生效区间内。
func (v *Version) EffectiveAt(t time.Time) bool {
	return !t.Before(v.ValidFrom) && t.Before(v.ValidTo)
}

// KnownAt 判断系统时间点 t 是否落在获知区间内。
func (v *Version) KnownAt(t time.Time) bool {
	return !t.Before(v.TxFrom) && t.Before(v.TxTo)
}

func (v *Version) String() string {
	return fmt.Sprintf("{key=%s value=%s valid=[%s,%s) tx=[%s,%s)}",
		v.Key, v.Value, fmtT(v.ValidFrom), fmtT(v.ValidTo), fmtT(v.TxFrom), fmtT(v.TxTo))
}

func fmtT(t time.Time) string {
	if t.Equal(Inf) {
		return "∞"
	}
	return t.Format("2006-01-02")
}

// operation 记录一次更正操作，用于撤销（undo）。
type operation struct {
	id      int
	tx      time.Time
	closed  []*Version // 本次被关闭（TxTo 被设为 tx）的旧版本
	created []*Version // 本次新增（含拆分残余段与新版本）的版本
	undone  bool
}

// Store 是双时间版本库，仅追加、不物理删除。
type Store struct {
	mu       sync.Mutex
	versions []*Version
	ops      []*operation
}

// NewStore 创建空版本库。
func NewStore() *Store { return &Store{} }

// Put 在系统时刻 tx 写入键 key 在业务区间 [validFrom, validTo) 内的新值。
// 语义即“更正”：与该区间重叠的当前版本被关闭（TxTo=tx），
// 超出更正区间的部分按原值拆分为残余版本，再插入新版本。
// 返回操作 ID，可用于 Undo。
func (s *Store) Put(key, value string, validFrom, validTo, tx time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validFrom.Before(validTo) {
		return -1, fmt.Errorf("非法业务区间: [%s, %s)", fmtT(validFrom), fmtT(validTo))
	}

	op := &operation{id: len(s.ops), tx: tx}

	// 1. 关闭所有与更正区间重叠的当前版本，并拆分出残余段。
	for _, v := range s.versions {
		if v.Key != key || !v.TxTo.Equal(Inf) {
			continue
		}
		// 无重叠：跳过。
		if !v.ValidFrom.Before(validTo) || !validFrom.Before(v.ValidTo) {
			continue
		}
		v.TxTo = tx
		op.closed = append(op.closed, v)

		// 左残余 [v.ValidFrom, validFrom)
		if v.ValidFrom.Before(validFrom) {
			op.created = append(op.created, s.appendLocked(&Version{
				Key: key, Value: v.Value,
				ValidFrom: v.ValidFrom, ValidTo: validFrom,
				TxFrom: tx, TxTo: Inf,
			}))
		}
		// 右残余 [validTo, v.ValidTo)
		if validTo.Before(v.ValidTo) {
			op.created = append(op.created, s.appendLocked(&Version{
				Key: key, Value: v.Value,
				ValidFrom: validTo, ValidTo: v.ValidTo,
				TxFrom: tx, TxTo: Inf,
			}))
		}
	}

	// 2. 插入新版本。
	op.created = append(op.created, s.appendLocked(&Version{
		Key: key, Value: value,
		ValidFrom: validFrom, ValidTo: validTo,
		TxFrom: tx, TxTo: Inf,
	}))

	s.ops = append(s.ops, op)
	return op.id, nil
}

func (s *Store) appendLocked(v *Version) *Version {
	s.versions = append(s.versions, v)
	return v
}

// Undo 在系统时刻 tx 撤销操作 opID：关闭该操作创建的版本，
// 并以新系统区间重新启用该操作关闭的旧版本（仍是 append-only）。
func (s *Store) Undo(opID int, tx time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if opID < 0 || opID >= len(s.ops) {
		return fmt.Errorf("操作 %d 不存在", opID)
	}
	op := s.ops[opID]
	if op.undone {
		return fmt.Errorf("操作 %d 已被撤销", opID)
	}
	for _, v := range op.created {
		if !v.TxTo.Equal(Inf) {
			return errors.New("该操作产生的版本已被后续更正修改，无法直接撤销")
		}
	}

	undo := &operation{id: len(s.ops), tx: tx}
	for _, v := range op.created {
		v.TxTo = tx
		undo.closed = append(undo.closed, v)
	}
	for _, old := range op.closed {
		undo.created = append(undo.created, s.appendLocked(&Version{
			Key: old.Key, Value: old.Value,
			ValidFrom: old.ValidFrom, ValidTo: old.ValidTo,
			TxFrom: tx, TxTo: Inf,
		}))
	}
	op.undone = true
	s.ops = append(s.ops, undo)
	return nil
}

// AsOf 按双时间查询：系统时刻 txAt 所知的、业务时刻 validAt 生效的值。
func (s *Store) AsOf(key string, validAt, txAt time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.versions {
		if v.Key == key && v.EffectiveAt(validAt) && v.KnownAt(txAt) {
			return v.Value, true
		}
	}
	return "", false
}

// Current 返回系统时刻 txAt 下键 key 的全部当前版本，按业务起点排序。
func (s *Store) Current(key string, txAt time.Time) []*Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Version
	for _, v := range s.versions {
		if v.Key == key && v.KnownAt(txAt) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValidFrom.Before(out[j].ValidFrom) })
	return out
}

// History 返回键 key 的全部版本（含已关闭的系统区间），按系统起点、业务起点排序。
func (s *Store) History(key string) []*Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Version
	for _, v := range s.versions {
		if v.Key == key {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].TxFrom.Equal(out[j].TxFrom) {
			return out[i].TxFrom.Before(out[j].TxFrom)
		}
		return out[i].ValidFrom.Before(out[j].ValidFrom)
	})
	return out
}

// ValidateNoOverlap 校验：同一键在系统时刻 txAt 的切面内不存在重叠生效区间。
func (s *Store) ValidateNoOverlap(key string, txAt time.Time) error {
	cur := s.Current(key, txAt)
	for i := 1; i < len(cur); i++ {
		if cur[i].ValidFrom.Before(cur[i-1].ValidTo) {
			return fmt.Errorf("键 %s 在系统切面 %s 存在重叠: %s 与 %s",
				key, fmtT(txAt), cur[i-1], cur[i])
		}
	}
	return nil
}

// CheckContiguous 校验：系统时刻 txAt 下键 key 的版本边界首尾相接（无重叠无空洞）。
func (s *Store) CheckContiguous(key string, txAt time.Time) error {
	cur := s.Current(key, txAt)
	for i := 1; i < len(cur); i++ {
		if !cur[i-1].ValidTo.Equal(cur[i].ValidFrom) {
			return fmt.Errorf("边界不相接: %s 之后是 %s", cur[i-1], cur[i])
		}
	}
	return nil
}
