package blockingflow

import (
	"fmt"
	"sort"
)

// Optimize 求解无停机的经典情形：在不超过 MaxJobs 件工件中枚举全部
// 排列，返回 makespan 最小的加工顺序；并列最优按 id 序列字典序取最小。
func Optimize(jobs []Job) (Schedule, error) {
	return search(jobs, nil, nil, nil, false)
}

// Replan 针对有限已知停机表重新优化剩余顺序：prefix 为已开工工件的 id
// （按既定顺序，不可重排），其余工件重新排序以最小化 makespan。
// 返回的方案包含阻塞时段（Blocks）与停机暂停证据（Pauses）。
func Replan(jobs []Job, prefix []int, down1, down2 []Interval) (Schedule, error) {
	return search(jobs, prefix, down1, down2, false)
}

// Verify 与 Replan 求解同一问题，但每个排列都用离散事件模拟评估，
// 用于核对 Replan 基于递推式的结果。复杂度为阶乘级，仅适合小规模核对。
func Verify(jobs []Job, prefix []int, down1, down2 []Interval) (Schedule, error) {
	return search(jobs, prefix, down1, down2, true)
}

// Simulate 用递推式评估一个给定的完整加工顺序（工件 id 序列）。
func Simulate(jobs []Job, seq []int, down1, down2 []Interval) (Schedule, error) {
	posSeq, d1, d2, err := prepareSeq(jobs, seq, down1, down2)
	if err != nil {
		return Schedule{}, err
	}
	return evaluate(jobs, posSeq, d1, d2), nil
}

// SimulateEvents 用离散事件模拟评估一个给定的完整加工顺序。
func SimulateEvents(jobs []Job, seq []int, down1, down2 []Interval) (Schedule, error) {
	posSeq, d1, d2, err := prepareSeq(jobs, seq, down1, down2)
	if err != nil {
		return Schedule{}, err
	}
	return simulateEvents(jobs, posSeq, d1, d2), nil
}

// prepare 校验输入，返回前缀下标、按 id 升序的剩余工件下标以及合并后的停机表。
func prepare(jobs []Job, prefix []int, down1, down2 []Interval) ([]int, []int, []Interval, []Interval, error) {
	if len(jobs) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("blockingflow: no jobs")
	}
	if len(jobs) > MaxJobs {
		return nil, nil, nil, nil, fmt.Errorf("blockingflow: %d jobs exceed MaxJobs=%d", len(jobs), MaxJobs)
	}
	posOf := make(map[int]int, len(jobs))
	for i, j := range jobs {
		if j.P1 < 0 || j.P2 < 0 {
			return nil, nil, nil, nil, fmt.Errorf("blockingflow: job %d has negative processing time", j.ID)
		}
		if _, dup := posOf[j.ID]; dup {
			return nil, nil, nil, nil, fmt.Errorf("blockingflow: duplicate job id %d", j.ID)
		}
		posOf[j.ID] = i
	}
	inPrefix := make([]bool, len(jobs))
	prefixPos := make([]int, 0, len(prefix))
	for _, id := range prefix {
		p, ok := posOf[id]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("blockingflow: prefix job id %d not found", id)
		}
		if inPrefix[p] {
			return nil, nil, nil, nil, fmt.Errorf("blockingflow: duplicate prefix job id %d", id)
		}
		inPrefix[p] = true
		prefixPos = append(prefixPos, p)
	}
	suffixPos := make([]int, 0, len(jobs)-len(prefix))
	for i := range jobs {
		if !inPrefix[i] {
			suffixPos = append(suffixPos, i)
		}
	}
	sort.Slice(suffixPos, func(a, b int) bool { return jobs[suffixPos[a]].ID < jobs[suffixPos[b]].ID })
	d1, err := mergeDowntimes(down1)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	d2, err := mergeDowntimes(down2)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return prefixPos, suffixPos, d1, d2, nil
}

// prepareSeq 校验输入并校验 seq 是全体工件 id 的一个排列。
func prepareSeq(jobs []Job, seq []int, down1, down2 []Interval) ([]int, []Interval, []Interval, error) {
	prefixPos, suffixPos, d1, d2, err := prepare(jobs, nil, down1, down2)
	if err != nil {
		return nil, nil, nil, err
	}
	posOf := make(map[int]int, len(jobs))
	for i, j := range jobs {
		posOf[j.ID] = i
	}
	_ = prefixPos
	if len(seq) != len(jobs) {
		return nil, nil, nil, fmt.Errorf("blockingflow: seq length %d != %d jobs", len(seq), len(jobs))
	}
	seen := make([]bool, len(jobs))
	posSeq := make([]int, len(seq))
	for i, id := range seq {
		p, ok := posOf[id]
		if !ok {
			return nil, nil, nil, fmt.Errorf("blockingflow: seq job id %d not found", id)
		}
		if seen[p] {
			return nil, nil, nil, fmt.Errorf("blockingflow: duplicate seq job id %d", id)
		}
		seen[p] = true
		posSeq[i] = p
	}
	_ = suffixPos
	return posSeq, d1, d2, nil
}

// search 在固定前缀下枚举剩余工件的全部排列，返回 makespan 最小的方案。
// 排列按 id 字典序生成，严格更优才替换，因此并列时保留字典序最小者。
// useDES 为 true 时每个排列用离散事件模拟评估（核对用），否则用递推式。
func search(jobs []Job, prefix []int, down1, down2 []Interval, useDES bool) (Schedule, error) {
	prefixPos, suffixPos, d1, d2, err := prepare(jobs, prefix, down1, down2)
	if err != nil {
		return Schedule{}, err
	}
	best := -1
	var bestSeq []int
	perm := append([]int(nil), suffixPos...)
	for {
		seq := make([]int, 0, len(jobs))
		seq = append(seq, prefixPos...)
		seq = append(seq, perm...)
		var mk int
		if useDES {
			mk = simulateEvents(jobs, seq, d1, d2).Makespan
		} else {
			mk = makespanRecurrence(jobs, seq, d1, d2)
		}
		if best < 0 || mk < best {
			best = mk
			bestSeq = seq
		}
		if !nextPermutation(jobs, perm) {
			break
		}
	}
	if useDES {
		return simulateEvents(jobs, bestSeq, d1, d2), nil
	}
	return evaluate(jobs, bestSeq, d1, d2), nil
}

// nextPermutation 把 perm（工件下标）调整为按工件 id 字典序的下一个排列，
// 已是最后一个排列时返回 false。
func nextPermutation(jobs []Job, perm []int) bool {
	less := func(a, b int) bool { return jobs[perm[a]].ID < jobs[perm[b]].ID }
	i := len(perm) - 2
	for i >= 0 && !less(i, i+1) {
		i--
	}
	if i < 0 {
		return false
	}
	j := len(perm) - 1
	for !less(i, j) {
		j--
	}
	perm[i], perm[j] = perm[j], perm[i]
	for lo, hi := i+1, len(perm)-1; lo < hi; lo, hi = lo+1, hi-1 {
		perm[lo], perm[hi] = perm[hi], perm[lo]
	}
	return true
}
