package trainsim

import (
	"fmt"
	"sort"
)

// Reservation 把一列车绑定到某轨段上的一个半开占用区间。
type Reservation struct {
	Train TrainID
	Seg   SegmentID
	Iv    Interval
}

// Table 保存已确认的预约，按轨段分组、按开始时刻有序。
type Table struct {
	bySeg map[SegmentID][]Reservation
}

// NewTable 创建空预约表。
func NewTable() *Table { return &Table{bySeg: make(map[SegmentID][]Reservation)} }

// Free 报告 iv 在 seg 上是否空闲（含同向列车，任何重叠都不允许）。
func (t *Table) Free(seg SegmentID, iv Interval) bool {
	for _, r := range t.bySeg[seg] {
		if r.Iv.Overlaps(iv) {
			return false
		}
	}
	return true
}

// Check 校验 rs 能否加入：内部互不重叠，且不与已确认预约重叠。
func (t *Table) Check(rs []Reservation) error {
	for i, r := range rs {
		if r.Iv.End <= r.Iv.Start {
			return fmt.Errorf("列车 %s 在 %s 上的占用区间为空", r.Train, r.Seg)
		}
		for j := i + 1; j < len(rs); j++ {
			if rs[i].Seg == rs[j].Seg && rs[i].Iv.Overlaps(rs[j].Iv) {
				return fmt.Errorf("列车 %s 在 %s 上的预约自相重叠", r.Train, r.Seg)
			}
		}
		if !t.Free(r.Seg, r.Iv) {
			return fmt.Errorf("轨段 %s 在 [%d,%d) 已被占用，列车 %s 无法进入",
				r.Seg, r.Iv.Start, r.Iv.End, r.Train)
		}
	}
	return nil
}

// Reserve 原子确认一批预约：全部成功才写入，失败不留任何部分预约。
func (t *Table) Reserve(rs ...Reservation) error {
	if err := t.Check(rs); err != nil {
		return err
	}
	t.Add(rs...)
	return nil
}

// Add 直接写入预约（调用方须已完成冲突检查）。
func (t *Table) Add(rs ...Reservation) {
	dirty := map[SegmentID]bool{}
	for _, r := range rs {
		t.bySeg[r.Seg] = append(t.bySeg[r.Seg], r)
		dirty[r.Seg] = true
	}
	for seg := range dirty {
		list := t.bySeg[seg]
		sort.Slice(list, func(i, j int) bool {
			if list[i].Iv.Start != list[j].Iv.Start {
				return list[i].Iv.Start < list[j].Iv.Start
			}
			return list[i].Train < list[j].Train
		})
	}
}

// CancelSuffix 取消某列车所有开始时刻不早于 from 的预约。
func (t *Table) CancelSuffix(train TrainID, from Time) {
	for seg, list := range t.bySeg {
		kept := make([]Reservation, 0, len(list))
		for _, r := range list {
			if r.Train == train && r.Iv.Start >= from {
				continue
			}
			kept = append(kept, r)
		}
		t.bySeg[seg] = kept
	}
}

// CancelTrain 取消某列车的全部预约。
func (t *Table) CancelTrain(train TrainID) { t.CancelSuffix(train, 0) }

// Reservations 返回全部预约（按轨段、开始时刻排序）。
func (t *Table) Reservations() []Reservation {
	var out []Reservation
	for _, list := range t.bySeg {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seg != out[j].Seg {
			return out[i].Seg < out[j].Seg
		}
		return out[i].Iv.Start < out[j].Iv.Start
	})
	return out
}

// Clone 深拷贝预约表。
func (t *Table) Clone() *Table {
	c := NewTable()
	for seg, list := range t.bySeg {
		c.bySeg[seg] = append([]Reservation{}, list...)
	}
	return c
}

// Verify 对每个轨段做区间扫描（按开始时刻排序后扫掠），
// 核对任意两条占用区间互不重叠。
func (t *Table) Verify() error {
	for seg, list := range t.bySeg {
		for i := 1; i < len(list); i++ {
			prev, cur := list[i-1], list[i]
			if cur.Iv.Start < prev.Iv.End {
				return fmt.Errorf("轨段 %s：%s [%d,%d) 与 %s [%d,%d) 重叠",
					seg, prev.Train, prev.Iv.Start, prev.Iv.End,
					cur.Train, cur.Iv.Start, cur.Iv.End)
			}
		}
	}
	return nil
}
