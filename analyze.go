package netsec

import "sort"

// Analyze 在至多 20 个候选事件上定位使“所有当前等待仍无法解除”的最小事件子图。
//
// 流程：
//  1. 对完整候选轨迹做基准重放，得到仍未解除的等待（锚点）。
//  2. 枚举全部 2^n 个子集并各做一次具体重放，核对该子集是否仍复现完整卡死状态。
//  3. 用动态规划在枚举结果上求全局最小（既是基数最小也是包含最小）的卡死子集。
//  4. 为每个最小子图生成等待依赖边、依赖闭环与逐事件移除重放证据。
//
// 全程只使用真实事件的真实顺序：未选入子集的事件不产生任何副作用，
// 包内不会重排事件，也不会补造任何消息或资源。
func Analyze(events []Event, opts Options) (Report, error) {
	if err := validate(events); err != nil {
		return Report{}, err
	}
	en := newEngine(events)
	n := len(events)

	full := en.replay(1<<uint(n)-1, true)
	if len(full.Pending) == 0 {
		return Report{FullReplay: full}, ErrNoPendingWaits
	}

	var anchorMask uint32
	for _, idx := range full.Pending {
		anchorMask |= 1 << uint(idx)
	}

	total := 1 << uint(n)
	// stuck[mask]：该子集重放后是否复现完整卡死状态（pending 与 skipped 均一致）。
	stuck := make([]bool, total)
	// hasStuckSub[mask]：mask 的任意子集（含自身）是否存在卡死子集。
	hasStuckSub := make([]bool, total)
	stuckCount := 0
	replayCount := 0

	for mask := 0; mask < total; mask++ {
		replayCount++
		rep := en.replay(uint32(mask), false)
		if reproducesStall(rep, full) {
			stuck[mask] = true
			stuckCount++
		}
		// 子集 DP：hasStuckSub[mask] = stuck[mask] 或任一去位后的 hasStuckSub。
		hs := stuck[mask]
		if !hs {
			bits := uint32(mask)
			for bits != 0 {
				low := bits & -bits
				if hasStuckSub[uint32(mask)^low] {
					hs = true
					break
				}
				bits ^= low
			}
		}
		hasStuckSub[mask] = hs
	}

	// 包含最小：mask 卡死且没有任何真子集卡死；在这些里面取基数最小。
	type cand struct {
		mask uint32
		size int
	}
	var minimal []cand
	minSize := n + 1
	for mask := 0; mask < total; mask++ {
		if !stuck[mask] {
			continue
		}
		bits := uint32(mask)
		hasProper := false
		for b := bits; b != 0; b &= b - 1 {
			low := b & -b
			if hasStuckSub[bits^low] {
				hasProper = true
				break
			}
		}
		if hasProper {
			continue
		}
		size := bitsOn(bits)
		if size < minSize {
			minSize = size
			minimal = minimal[:0]
		}
		if size == minSize {
			minimal = append(minimal, cand{mask: bits, size: size})
		}
	}

	sort.Slice(minimal, func(i, j int) bool { return minimal[i].mask < minimal[j].mask })

	report := Report{
		AnchorWaits:  append([]int(nil), full.Pending...),
		TotalSubsets: total,
		StuckSubsets: stuckCount,
		ReplayCount:  replayCount,
		MinimalSize:  minSize,
		FullReplay:   full,
	}

	limit := len(minimal)
	if opts.MaxCores > 0 && opts.MaxCores < limit {
		limit = opts.MaxCores
	}

	for ci := 0; ci < limit; ci++ {
		mask := minimal[ci].mask
		rep := en.replay(mask, true)
		core := Core{
			Events:       maskIndices(mask),
			Replay:       rep,
			Dependencies: en.buildDependencies(mask, rep.Pending),
		}
		// 移除核对：对每个非锚点事件移除后单独重放，卡死状态必须被破坏
		// （解除某个等待，或让某个被阻塞事件得以执行），以此证明其必要性。
		for _, idx := range core.Events {
			if anchorMask&(1<<uint(idx)) != 0 {
				continue
			}
			sub := mask ^ (1 << uint(idx))
			subRep := en.replay(sub, false)
			released := difference(full.Pending, subRep.Pending)
			skippedLost := difference(full.Skipped, subRep.Skipped)
			core.RemovalChecks = append(core.RemovalChecks, RemovalCheck{
				RemovedIndex: idx,
				StillStuck:   reproducesStall(subRep, full),
				Released:     released,
				SkippedLost:  skippedLost,
				Note:         removalNote(en.events[idx], released, skippedLost),
			})
		}
		core.Trivial = len(core.RemovalChecks) == 0
		report.Cores = append(report.Cores, core)
	}

	ring, closed := en.buildRing(full.Pending, full.Skipped)
	report.Ring = ring
	report.RingClosed = closed
	return report, nil
}

// reproducesStall 判断一次子集重放是否复现完整轨迹的卡死状态。
// 必须同时满足两点：
//   - pending 集合完全一致（同一批等待/接收仍未解除）；
//   - skipped 集合完全一致（完整轨迹中因阻塞而无法执行的真实后续事件，
//     在子集中也必须保持被阻塞）。
//
// 第二点保证最小子图不能只保留等待锚点，而必须带上那些被卡住的真实
// 发送/接收事件——它们正是等待环上的消息依赖；全程不合成任何事件。
func reproducesStall(got, full ReplayReport) bool {
	return equalInts(got.Pending, full.Pending) && equalInts(got.Skipped, full.Skipped)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func maskIndices(mask uint32) []int {
	out := make([]int, 0, bitsOn(mask))
	for i := 0; mask != 0; i++ {
		if mask&1 == 1 {
			out = append(out, i)
		}
		mask >>= 1
	}
	return out
}

func difference(want, have []int) []int {
	set := make(map[int]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	var out []int
	for _, w := range want {
		if !set[w] {
			out = append(out, w)
		}
	}
	return out
}

func removalNote(e Event, released, skippedLost []int) string {
	if len(released) == 0 && len(skippedLost) == 0 {
		return "removing it leaves the stall unchanged"
	}
	note := "removing " + e.Kind.String() + " event breaks the stall"
	if len(released) > 0 {
		note += "; released waits " + intsString(released)
	}
	if len(skippedLost) > 0 {
		note += "; unblocks skipped events " + intsString(skippedLost)
	}
	return note
}

func intsString(xs []int) string {
	out := "["
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += "#" + itoa(x)
	}
	return out + "]"
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var b [20]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
