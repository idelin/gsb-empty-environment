// Package scd2merge 实现缓慢变化维（SCD Type-2）的区间合并。
//
// 每个业务键维护一组半开生效区间 [From, To)，任一时刻最多一个有效版本。
// 上游事件允许乱序、迟到、重复到达：
//   - 迟到的变更插入到正确位置并切分已有区间；
//   - 相邻且内容相同的区间在输出时自动合并；
//   - 重复事件按事件 ID 幂等去重；
//   - 删除事件关闭当前区间；
//   - 同一事件时间戳上删除优先于变更（与 Rebuild 的排序规则一致）。
//
// 因此以任意顺序 Apply 同一批事件，结果与 Rebuild 按事件时间
// 全量重建完全一致。
//
// 内部为每个事件时刻保留独立分段（不做提前合并），以保证乱序到达的
// 汇合性：分段边界本身承载了后续同刻裁决所需的信息，提前合并会丢失它。
package scd2merge

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Op 表示事件类型。
type Op int

const (
	// OpUpsert 写入或变更维度内容。
	OpUpsert Op = iota
	// OpDelete 删除维度，关闭当前生效区间。
	OpDelete
)

// Event 是一条上游变更事件。ID 必须在全局唯一且非空，用于幂等去重；
// 同一事件时间戳上的多个 upsert 按 ID 字典序大者胜出。
type Event[V comparable] struct {
	ID    string
	Key   string
	At    time.Time
	Op    Op
	Value V
}

// Interval 是一个生效区间 [From, To)。To 为零值表示开放区间（至今有效）。
type Interval[V comparable] struct {
	From  time.Time
	To    time.Time
	Value V
}

// Open 报告区间是否开放（至今有效）。
func (iv Interval[V]) Open() bool { return iv.To.IsZero() }

// Contains 报告 t 是否落在区间内。
func (iv Interval[V]) Contains(t time.Time) bool {
	return !t.Before(iv.From) && (iv.To.IsZero() || t.Before(iv.To))
}

// segment 是内部区间表示，附带产生区间起点的事件 ID 用于同刻裁决。
type segment[V comparable] struct {
	from  time.Time
	to    time.Time // 零值表示开放
	value V
	src   string
}

func (s segment[V]) interval() Interval[V] {
	return Interval[V]{From: s.from, To: s.to, Value: s.value}
}

// dimension 保存单个业务键的全部区间与去重状态。
type dimension[V comparable] struct {
	segs      []segment[V] // 按 from 升序，互不重叠
	seen      map[string]struct{}
	deletedAt map[time.Time]struct{} // 已应用删除事件的发生时刻（同刻 upsert 拦截）
	deletes   []time.Time            // 升序去重的删除时刻，作为区间屏障
}

// Store 按业务键维护所有维度区间，并发安全。
type Store[V comparable] struct {
	mu   sync.Mutex
	dims map[string]*dimension[V]
}

// New 返回一个空的 Store。
func New[V comparable]() *Store[V] {
	return &Store[V]{dims: make(map[string]*dimension[V])}
}

// Apply 应用一条事件，返回事件是否被接受（重复事件返回 false）。
// 事件 ID 为空会 panic。
func (s *Store[V]) Apply(ev Event[V]) bool {
	if ev.ID == "" {
		panic("scd2merge: event ID must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dims[ev.Key]
	if d == nil {
		d = &dimension[V]{
			seen:      make(map[string]struct{}),
			deletedAt: make(map[time.Time]struct{}),
		}
		s.dims[ev.Key] = d
	}
	if _, dup := d.seen[ev.ID]; dup {
		return false
	}
	d.seen[ev.ID] = struct{}{}
	switch ev.Op {
	case OpUpsert:
		d.upsert(ev)
	case OpDelete:
		d.remove(ev)
	default:
		panic(fmt.Sprintf("scd2merge: unknown op %d", ev.Op))
	}
	return true
}

// ApplyAll 依次应用一批事件。
func (s *Store[V]) ApplyAll(events []Event[V]) {
	for _, ev := range events {
		s.Apply(ev)
	}
}

// Intervals 返回业务键当前的生效区间，按 From 升序，
// 相邻且内容相同的区间已合并。
func (s *Store[V]) Intervals(key string) []Interval[V] {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dims[key]
	if d == nil {
		return nil
	}
	out := make([]Interval[V], 0, len(d.segs))
	for _, sg := range d.segs {
		if n := len(out); n > 0 && out[n-1].To.Equal(sg.from) && out[n-1].Value == sg.value {
			out[n-1].To = sg.to
			continue
		}
		out = append(out, sg.interval())
	}
	return out
}

// ValueAt 返回业务键在时刻 t 的有效版本内容。
func (s *Store[V]) ValueAt(key string, t time.Time) (V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero V
	d := s.dims[key]
	if d == nil {
		return zero, false
	}
	if i := d.find(t); i >= 0 {
		return d.segs[i].value, true
	}
	return zero, false
}

// Rebuild 按（事件时间，删除优先，事件 ID）排序后全量重建，
// 作为乱序合并结果的对照基准。
func Rebuild[V comparable](events []Event[V]) *Store[V] {
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b Event[V]) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		if c := cmp.Compare(opRank(a.Op), opRank(b.Op)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	s := New[V]()
	s.ApplyAll(sorted)
	return s
}

// opRank 保证同一时刻删除排在变更之后（删除生效）。
func opRank(op Op) int {
	if op == OpDelete {
		return 1
	}
	return 0
}

// find 返回包含 t 的区间下标，不存在返回 -1。
func (d *dimension[V]) find(t time.Time) int {
	i, exact := slices.BinarySearchFunc(d.segs, t, func(sg segment[V], target time.Time) int {
		return sg.from.Compare(target)
	})
	if exact {
		return i
	}
	if j := i - 1; j >= 0 && (d.segs[j].to.IsZero() || t.Before(d.segs[j].to)) {
		return j
	}
	return -1
}

// upsert 应用一条变更事件。
func (d *dimension[V]) upsert(ev Event[V]) {
	// 同一时刻删除优先：该时刻已有删除事件则忽略。
	if _, deleted := d.deletedAt[ev.At]; deleted {
		return
	}
	barrier := d.barrierAfter(ev.At)
	if i := d.find(ev.At); i >= 0 {
		sg := d.segs[i]
		if sg.from.Equal(ev.At) {
			// 同刻多个 upsert：事件 ID 大者胜出。
			if ev.ID > sg.src {
				d.segs[i].value = ev.Value
				d.segs[i].src = ev.ID
			}
			return
		}
		// 迟到变更落在区间内部：切分出新区间。即使内容相同也必须
		// 保留边界——该时刻是独立版本点，后续（事件时间更早的）变更
		// 可能改写左侧区间内容。
		right := segment[V]{from: ev.At, to: clampTo(sg.to, barrier), value: ev.Value, src: ev.ID}
		d.segs[i].to = ev.At
		d.segs = slices.Insert(d.segs, i+1, right)
		return
	}
	// 落在空洞或末尾：插入到下一区间起点（或开放区间）。
	j, _ := slices.BinarySearchFunc(d.segs, ev.At, func(sg segment[V], target time.Time) int {
		return sg.from.Compare(target)
	})
	var to time.Time
	if j < len(d.segs) {
		to = d.segs[j].from
	}
	d.segs = slices.Insert(d.segs, j, segment[V]{from: ev.At, to: clampTo(to, barrier), value: ev.Value, src: ev.ID})
}

// remove 应用一条删除事件，关闭包含事件时刻的区间。
func (d *dimension[V]) remove(ev Event[V]) {
	d.deletedAt[ev.At] = struct{}{}
	if i, found := slices.BinarySearchFunc(d.deletes, ev.At, func(e, target time.Time) int {
		return e.Compare(target)
	}); !found {
		d.deletes = slices.Insert(d.deletes, i, ev.At)
	}
	i := d.find(ev.At)
	if i < 0 {
		return
	}
	if d.segs[i].from.Equal(ev.At) {
		// 删除时刻恰为区间起点：整段作废。
		d.segs = slices.Delete(d.segs, i, i+1)
		return
	}
	d.segs[i].to = ev.At
}

// barrierAfter 返回时刻 t 之后最早的删除时刻，无则返回零值。
// 删除时刻是屏障：任何区间不得跨过它（删除会先到的场景下，
// 迟到 upsert 新建的区间必须在此截断）。
func (d *dimension[V]) barrierAfter(t time.Time) time.Time {
	i, _ := slices.BinarySearchFunc(d.deletes, t, func(e, target time.Time) int {
		return e.Compare(target)
	})
	for i < len(d.deletes) && !d.deletes[i].After(t) {
		i++
	}
	if i < len(d.deletes) {
		return d.deletes[i]
	}
	return time.Time{}
}

// clampTo 用屏障时刻截断区间结束点（零值表示开放）。
func clampTo(to, barrier time.Time) time.Time {
	if !barrier.IsZero() && (to.IsZero() || barrier.Before(to)) {
		return barrier
	}
	return to
}
