package main

import (
	"fmt"
	"sort"
)

// PairCheck 记录对一个未匹配可接受对的弱阻挡检查证据。
// 弱稳定性定义: (a, j) 构成阻挡对, 当且仅当
//   a 未匹配或严格偏好 j 胜于当前岗位, 且
//   j 有空余名额或严格偏好 a 胜于某位在岗者。
type PairCheck struct {
	Applicant string
	Job       string
	Blocking  bool
	Detail    string
}

// buildJobAssign 由申请者侧分配重建岗位侧在岗名单。
func buildJobAssign(p *Problem, assign []int) [][]int {
	jobAssign := make([][]int, len(p.Jobs))
	for a, j := range assign {
		if j >= 0 {
			jobAssign[j] = append(jobAssign[j], a)
		}
	}
	return jobAssign
}

// CheckMatching 检查匹配中所有互相可接受但未配对的 (申请者, 岗位) 组合,
// 返回每一对的阻挡检查证据(按申请者、名次、岗位名排序, 保证确定性输出)。
func CheckMatching(p *Problem, assign []int, jobAssign [][]int) []PairCheck {
	var checks []PairCheck
	for a := range p.Applicants {
		jobs := make([]int, 0, len(p.AppRank[a]))
		for j := range p.AppRank[a] {
			if p.acceptable(a, j) && assign[a] != j {
				jobs = append(jobs, j)
			}
		}
		sort.Slice(jobs, func(x, y int) bool {
			rx, ry := p.AppRank[a][jobs[x]], p.AppRank[a][jobs[y]]
			if rx != ry {
				return rx < ry
			}
			return p.Jobs[jobs[x]] < p.Jobs[jobs[y]]
		})
		for _, j := range jobs {
			checks = append(checks, checkPair(p, assign, jobAssign, a, j))
		}
	}
	return checks
}

// checkPair 判定 (a, j) 是否构成弱阻挡对并生成证据文本。
func checkPair(p *Problem, assign []int, jobAssign [][]int, a, j int) PairCheck {
	check := PairCheck{Applicant: p.Applicants[a], Job: p.Jobs[j]}
	an, jn := p.Applicants[a], p.Jobs[j]
	rankAJ := p.AppRank[a][j]

	// 申请者侧: 必须未匹配或严格偏好 j。
	cur := assign[a]
	appStrict := cur == -1 || rankAJ < p.AppRank[a][cur]
	if !appStrict {
		check.Detail = fmt.Sprintf("%s 不严格偏好 %s(名次%d) 胜于当前岗位 %s(名次%d)",
			an, jn, rankAJ, p.Jobs[cur], p.AppRank[a][cur])
		return check
	}

	// 岗位侧: 有空余名额即被阻挡。
	if len(jobAssign[j]) < p.Capacity[j] {
		check.Blocking = true
		if cur == -1 {
			check.Detail = fmt.Sprintf("%s 未匹配, 且 %s 有空余名额(%d/%d)",
				an, jn, len(jobAssign[j]), p.Capacity[j])
		} else {
			check.Detail = fmt.Sprintf("%s 严格偏好 %s(名次%d) 胜于 %s(名次%d), 且 %s 有空余名额(%d/%d)",
				an, jn, rankAJ, p.Jobs[cur], p.AppRank[a][cur], jn, len(jobAssign[j]), p.Capacity[j])
		}
		return check
	}

	// 岗位已满: 仅当岗位严格偏好 a 胜于最差在岗者时才被阻挡。
	worst, worstRank := -1, -1
	for _, b := range jobAssign[j] {
		if r := p.JobRank[j][b]; r > worstRank {
			worstRank, worst = r, b
		}
	}
	rankJA := p.JobRank[j][a]
	if rankJA < worstRank {
		check.Blocking = true
		check.Detail = fmt.Sprintf("%s 严格偏好 %s, 且 %s 严格偏好 %s(名次%d) 胜于在岗者 %s(名次%d)",
			an, jn, jn, an, rankJA, p.Applicants[worst], worstRank)
	} else {
		check.Detail = fmt.Sprintf("%s 严格偏好 %s, 但 %s 已满(%d/%d)且不严格偏好 %s(名次%d) 胜于最差在岗者 %s(名次%d)",
			an, jn, jn, len(jobAssign[j]), p.Capacity[j], an, rankJA, p.Applicants[worst], worstRank)
	}
	return check
}

// Result 汇总求解结果与证据。
type Result struct {
	BestAssign   []int // 申请者下标 -> 岗位下标, -1 表示未匹配
	BestSize     int
	MaxCount     int // 达到最大规模的弱稳定匹配个数
	StableCount  int // 弱稳定匹配总数
	Enumerated   int // 穷举的匹配总数
	Evidence     []PairCheck
	BlockingHits int // 最终匹配中的阻挡对数(应为 0)
}

// Solve 穷举所有匹配, 返回最大规模的弱稳定匹配。
// 由于申请者不超过 10 名, 配合规模上界剪枝, 穷举代价可控;
// 最优性由穷举保证, 而非依赖任何并列偏好的任意打破。
func Solve(p *Problem) Result {
	s := &solver{p: p}
	s.options = make([][]int, len(p.Applicants))
	for a := range p.Applicants {
		for j := range p.AppRank[a] {
			if p.acceptable(a, j) {
				s.options[a] = append(s.options[a], j)
			}
		}
		sort.Slice(s.options[a], func(x, y int) bool {
			rx, ry := p.AppRank[a][s.options[a][x]], p.AppRank[a][s.options[a][y]]
			if rx != ry {
				return rx < ry
			}
			return s.options[a][x] < s.options[a][y]
		})
	}
	assign := make([]int, len(p.Applicants))
	for i := range assign {
		assign[i] = -1
	}
	jobAssign := make([][]int, len(p.Jobs))
	s.search(0, assign, jobAssign, 0)

	jobAssign = buildJobAssign(p, s.bestAssign)
	evidence := CheckMatching(p, s.bestAssign, jobAssign)
	res := Result{
		BestAssign:  s.bestAssign,
		BestSize:    s.bestSize,
		MaxCount:    s.maxCount,
		StableCount: s.stableCount,
		Enumerated:  s.enumerated,
		Evidence:    evidence,
	}
	for _, c := range evidence {
		if c.Blocking {
			res.BlockingHits++
		}
	}
	return res
}

type solver struct {
	p           *Problem
	options     [][]int
	bestAssign  []int
	bestSize    int
	maxCount    int
	stableCount int
	enumerated  int
}

// search 按申请者顺序回溯枚举所有匹配。
// 剪枝: 即使剩余申请者全部匹配也无法达到当前最优规模时停止。
func (s *solver) search(i int, assign []int, jobAssign [][]int, size int) {
	n := len(s.p.Applicants)
	if size+(n-i) < s.bestSize {
		return
	}
	if i == n {
		s.enumerated++
		if !hasBlocking(s.p, assign, jobAssign) {
			s.stableCount++
			if size > s.bestSize {
				s.bestSize = size
				s.bestAssign = append([]int(nil), assign...)
				s.maxCount = 1
			} else if size == s.bestSize {
				s.maxCount++
			}
		}
		return
	}
	for _, j := range s.options[i] {
		if len(jobAssign[j]) < s.p.Capacity[j] {
			assign[i] = j
			jobAssign[j] = append(jobAssign[j], i)
			s.search(i+1, assign, jobAssign, size+1)
			jobAssign[j] = jobAssign[j][:len(jobAssign[j])-1]
		}
	}
	assign[i] = -1
	s.search(i+1, assign, jobAssign, size)
}

// hasBlocking 报告当前匹配是否存在弱阻挡对。
func hasBlocking(p *Problem, assign []int, jobAssign [][]int) bool {
	for a := range p.Applicants {
		cur := assign[a]
		for j, rankAJ := range p.AppRank[a] {
			if !p.acceptable(a, j) || j == cur {
				continue
			}
			if cur != -1 && rankAJ >= p.AppRank[a][cur] {
				continue // 申请者侧不严格偏好
			}
			if len(jobAssign[j]) < p.Capacity[j] {
				return true // 岗位有空余名额
			}
			rankJA := p.JobRank[j][a]
			for _, b := range jobAssign[j] {
				if rankJA < p.JobRank[j][b] {
					return true // 岗位严格偏好 a 胜于在岗者 b
				}
			}
		}
	}
	return false
}
