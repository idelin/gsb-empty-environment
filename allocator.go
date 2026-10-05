package main

import (
	"errors"
	"sort"
)

// MaxGroups 是问题规模上限：组数不超过 20，因此 2^20 全枚举是精确且可行的。
const MaxGroups = 20

// Interval 是目标版本人数必须落入的闭区间 [Lo, Hi]。
type Interval struct {
	Lo int64
	Hi int64
}

// IntervalFromRatio 由目标比例与容差计算精确人数区间。
// total 为全部组的用户总数。区间端点取整保证：任何整数人数落在
// [Lo, Hi] 内当且仅当其比例落在 [ratio-tol, ratio+tol] 内。
func IntervalFromRatio(total int64, ratio, tol float64) Interval {
	lo := ratio - tol
	hi := ratio + tol
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return Interval{
		Lo: ceilDiv(total, lo),
		Hi: floorMul(total, hi),
	}
}

// ceilDiv 返回 ceil(total*f)，用整数运算避免浮点边界误差。
// 实现方式：找到最小整数 x 使得 x >= total*f。
func ceilDiv(total int64, f float64) int64 {
	x := int64(f * float64(total))
	// 浮点向下取整后，向上修正
	for float64(x) < f*float64(total) {
		x++
	}
	for x > 0 && float64(x-1) >= f*float64(total) {
		x--
	}
	return x
}

// floorMul 返回 floor(total*f)。
func floorMul(total int64, f float64) int64 {
	x := int64(f * float64(total))
	for float64(x) > f*float64(total) {
		x--
	}
	for float64(x+1) <= f*float64(total) {
		x++
	}
	return x
}

// Solve 枚举全部 2^n 种按组分配方案（组不可分割），返回满足人数区间的
// 最小迁移方案；若不存在可行方案，返回 nil 与全部可达人数集合（用于构造
// 不可达证明）。
//
// 迁移代价定义：一个组若分配结果与其当前版本不同，则该组全部用户计入
// 迁移人数。禁止拆组，因此每个组要么整体迁移、要么不动。
func Solve(groups []Group, targetVersion string, iv Interval) (best *Solution, reachable []int64, err error) {
	n := len(groups)
	if n == 0 {
		return nil, nil, errors.New("no groups")
	}
	if n > MaxGroups {
		return nil, nil, errors.New("too many groups: max 20")
	}
	if iv.Lo > iv.Hi {
		return nil, nil, errors.New("empty interval")
	}

	// weightSum[mask] = mask 中所有组的用户数之和（子集和 DP）。
	size := 1 << uint(n)
	weightSum := make([]int64, size)
	for mask := 1; mask < size; mask++ {
		low := mask & (-mask)
		bit := bitIndex(low)
		weightSum[mask] = weightSum[mask^low] + groups[bit].Users
	}

	// curMask：当前已在目标版本上的组。
	var curMask uint64
	for i, g := range groups {
		if g.Version == targetVersion {
			curMask |= 1 << uint(i)
		}
	}

	// 可达人数集合（用于不可达证明）。
	reachSet := make(map[int64]struct{}, size)

	var bestSol *Solution
	var bestDist int64

	for mask := 0; mask < size; mask++ {
		sum := weightSum[mask]
		reachSet[sum] = struct{}{}
		if sum < iv.Lo || sum > iv.Hi {
			continue
		}
		moved := weightSum[uint64(mask)^curMask]
		// 平局时偏向区间中心：比较 2*sum 与 Lo+Hi 的距离，避免浮点。
		dist := abs64(2*sum - (iv.Lo + iv.Hi))
		if bestSol == nil ||
			moved < bestSol.MovedUsers ||
			(moved == bestSol.MovedUsers && dist < bestDist) {
			bestSol = &Solution{
				Mask:        uint64(mask),
				NewUsers:    sum,
				MovedUsers:  moved,
				MovedGroups: popcount(uint64(mask) ^ curMask),
			}
			bestDist = dist
		}
	}

	reachable = make([]int64, 0, len(reachSet))
	for s := range reachSet {
		reachable = append(reachable, s)
	}
	sort.Slice(reachable, func(i, j int) bool { return reachable[i] < reachable[j] })
	return bestSol, reachable, nil
}

// ReachableSums 单独计算全部可达人数（升序、去重），供证明与校验使用。
func ReachableSums(groups []Group) []int64 {
	n := len(groups)
	size := 1 << uint(n)
	set := make(map[int64]struct{}, size)
	weightSum := make([]int64, size)
	for mask := 1; mask < size; mask++ {
		low := mask & (-mask)
		weightSum[mask] = weightSum[mask^low] + groups[bitIndex(low)].Users
	}
	for _, s := range weightSum {
		set[s] = struct{}{}
	}
	out := make([]int64, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TotalUsers 返回全部组的用户总数。
func TotalUsers(groups []Group) int64 {
	var t int64
	for _, g := range groups {
		t += g.Users
	}
	return t
}

// GCD 返回所有组人数的公约数（用于算术不可达判据）。
func GCD(groups []Group) int64 {
	var g int64
	for _, gr := range groups {
		g = gcd64(g, gr.Users)
	}
	return g
}

func gcd64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func bitIndex(low int) int {
	i := 0
	for low > 1 {
		low >>= 1
		i++
	}
	return i
}

func popcount(x uint64) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
