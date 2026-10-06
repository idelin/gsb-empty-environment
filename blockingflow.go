// Package blockingflow 求解两机零缓冲（阻塞式）流水车间排序问题。
//
// 每件工件先在机器一加工，再在机器二加工，两台机器采用相同的加工顺序
// （排列式流水车间）。机器之间没有缓冲区：工件在机器一上加工完毕后，
// 若机器二仍被占用，该工件继续占住机器一，直到机器二空闲（阻塞）。
// 工件数不超过 MaxJobs，目标为最小化最大完工时间（makespan），
// 并列最优时按工件 id 序列的字典序取最小者。
//
// 额外支持已知的机器停机表：停机期间正在加工的工件暂停加工但仍占住
// 机器，缓冲容量仍为零。重规划时已开工的前缀工件顺序不可更改，
// 仅对剩余工件重新优化顺序。
package blockingflow

import (
	"fmt"
	"sort"
)

// MaxJobs 为库支持的最大工件数（全排列搜索的规模上限）。
const MaxJobs = 10

// Interval 表示半开时间区间 [Start, End)。
type Interval struct {
	Start int
	End   int
}

// Job 表示一件工件在两台机器上的加工时间。
type Job struct {
	ID int
	P1 int // 机器一上的加工时间
	P2 int // 机器二上的加工时间
}

// BlockRecord 是阻塞证据：工件在机器一上加工完毕后，因机器二被占用，
// 在 [Start, End) 期间继续占住机器一。
type BlockRecord struct {
	JobID int
	Start int
	End   int
}

// PauseRecord 是停机证据：工件在 Machine（1 或 2）上加工期间，
// 因机器停机在 [Start, End) 内暂停加工（仍占住机器）。
type PauseRecord struct {
	JobID   int
	Machine int
	Start   int
	End     int
}

// Schedule 为一个排序方案及其证据。
type Schedule struct {
	Seq      []int         // 加工顺序（工件 id）
	Makespan int           // 最大完工时间
	Blocks   []BlockRecord // 阻塞时段（按发生先后排列）
	Pauses   []PauseRecord // 停机暂停时段（按工件、机器排列）
}

// mergeDowntimes 校验并合并重叠或相邻的停机区间，返回按起点升序的合并结果。
func mergeDowntimes(downs []Interval) ([]Interval, error) {
	for _, d := range downs {
		if d.Start < 0 || d.End <= d.Start {
			return nil, fmt.Errorf("blockingflow: invalid downtime interval [%d, %d)", d.Start, d.End)
		}
	}
	sorted := append([]Interval(nil), downs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var merged []Interval
	for _, d := range sorted {
		if len(merged) == 0 || d.Start > merged[len(merged)-1].End {
			merged = append(merged, d)
		} else if d.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = d.End
		}
	}
	return merged, nil
}

// completeAfter 返回从 start 时刻开始、在扣除停机时段后累计 proc 个
// 单位加工量的完工时刻。downs 必须已合并且按起点升序。
func completeAfter(start, proc int, downs []Interval) int {
	t := start
	rem := proc
	for _, d := range downs {
		if rem == 0 {
			break
		}
		if d.End <= t {
			continue
		}
		if d.Start >= t+rem {
			break
		}
		if d.Start > t {
			rem -= d.Start - t
		}
		t = d.End
	}
	return t + rem
}

// pausesIn 返回停机区间中与 [start, end) 相交的部分，即加工暂停时段。
func pausesIn(start, end int, downs []Interval) []Interval {
	var out []Interval
	for _, d := range downs {
		if d.End <= start {
			continue
		}
		if d.Start >= end {
			break
		}
		s := d.Start
		if s < start {
			s = start
		}
		e := d.End
		if e > end {
			e = end
		}
		if s < e {
			out = append(out, Interval{Start: s, End: e})
		}
	}
	return out
}

// makespanRecurrence 用递推式计算给定顺序（工件下标序列）的 makespan。
// d1[i] = max(c1[i], d2[i-1])：工件离开机器一的时刻受机器二释放时刻约束。
func makespanRecurrence(jobs []Job, seq []int, down1, down2 []Interval) int {
	d1Prev, d2Prev := 0, 0
	for _, pos := range seq {
		j := jobs[pos]
		c1 := completeAfter(d1Prev, j.P1, down1)
		d1 := c1
		if d2Prev > d1 {
			d1 = d2Prev
		}
		c2 := completeAfter(d1, j.P2, down2)
		d1Prev, d2Prev = d1, c2
	}
	return d2Prev
}

// evaluate 用递推式评估给定顺序，返回带阻塞与停机证据的完整方案。
func evaluate(jobs []Job, seq []int, down1, down2 []Interval) Schedule {
	sched := Schedule{Seq: idsOf(jobs, seq)}
	d1Prev, d2Prev := 0, 0
	for _, pos := range seq {
		j := jobs[pos]
		s1 := d1Prev
		c1 := completeAfter(s1, j.P1, down1)
		for _, iv := range pausesIn(s1, c1, down1) {
			sched.Pauses = append(sched.Pauses, PauseRecord{JobID: j.ID, Machine: 1, Start: iv.Start, End: iv.End})
		}
		d1 := c1
		if d2Prev > d1 {
			d1 = d2Prev
		}
		if d1 > c1 {
			sched.Blocks = append(sched.Blocks, BlockRecord{JobID: j.ID, Start: c1, End: d1})
		}
		c2 := completeAfter(d1, j.P2, down2)
		for _, iv := range pausesIn(d1, c2, down2) {
			sched.Pauses = append(sched.Pauses, PauseRecord{JobID: j.ID, Machine: 2, Start: iv.Start, End: iv.End})
		}
		d1Prev, d2Prev = d1, c2
	}
	sched.Makespan = d2Prev
	return sched
}

// idsOf 把工件下标序列转换为 id 序列。
func idsOf(jobs []Job, seq []int) []int {
	ids := make([]int, len(seq))
	for i, p := range seq {
		ids[i] = jobs[p].ID
	}
	return ids
}
