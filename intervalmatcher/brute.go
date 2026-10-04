package main

// bruteForce 对小数据枚举所有一一映射 (A 到 B 的单射子集),
// 返回字典序最优的 (匹配对数, 总收益)。用于核对 Match 的正确性。
func bruteForce(a, b []Record) (int, int64) {
	// 预计算正重叠矩阵, 0 表示不可配对。
	w := make([][]int64, len(a))
	for i := range a {
		w[i] = make([]int64, len(b))
		for j := range b {
			w[i][j] = Overlap(a[i], b[j])
		}
	}
	used := make([]bool, len(b))
	bestCount := 0
	var bestGain int64
	var rec func(i, cnt int, gain int64)
	rec = func(i, cnt int, gain int64) {
		if i == len(a) {
			if cnt > bestCount || (cnt == bestCount && gain > bestGain) {
				bestCount, bestGain = cnt, gain
			}
			return
		}
		// 上界剪枝: 剩下的 A 全部匹配也超不过当前最优对数且收益不更优。
		if cnt+(len(a)-i) < bestCount {
			return
		}
		rec(i+1, cnt, gain) // a[i] 不匹配
		for j := range b {
			if used[j] || w[i][j] <= 0 {
				continue
			}
			used[j] = true
			rec(i+1, cnt+1, gain+w[i][j])
			used[j] = false
		}
	}
	rec(0, 0, 0)
	return bestCount, bestGain
}
