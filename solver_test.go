package main

import (
	"math/rand"
	"testing"
)

func mustBuild(t *testing.T, in Input) *Problem {
	t.Helper()
	p, err := BuildProblem(in)
	if err != nil {
		t.Fatalf("BuildProblem: %v", err)
	}
	return p
}

func solveInput(t *testing.T, in Input) (*Problem, Result) {
	t.Helper()
	p := mustBuild(t, in)
	return p, Solve(p)
}

// 无并列的经典情形: 应得到全匹配。
func TestNoTies(t *testing.T) {
	_, res := solveInput(t, Input{
		Applicants: []ApplicantInput{
			{Name: "a1", Preferences: [][]string{{"j1"}, {"j2"}, {"j3"}}},
			{Name: "a2", Preferences: [][]string{{"j2"}, {"j1"}, {"j3"}}},
			{Name: "a3", Preferences: [][]string{{"j1"}, {"j3"}, {"j2"}}},
		},
		Jobs: []JobInput{
			{Name: "j1", Preferences: [][]string{{"a2"}, {"a1"}, {"a3"}}},
			{Name: "j2", Preferences: [][]string{{"a1"}, {"a3"}, {"a2"}}},
			{Name: "j3", Preferences: [][]string{{"a1"}, {"a2"}, {"a3"}}},
		},
	})
	if res.BestSize != 3 {
		t.Fatalf("BestSize = %d, want 3", res.BestSize)
	}
	if res.BlockingHits != 0 {
		t.Fatalf("BlockingHits = %d, want 0", res.BlockingHits)
	}
}

// 关键用例: 任意打破并列(按列出顺序)的 Gale-Shapley 只得规模 1,
// 但最大弱稳定匹配规模为 2。求解器不得依赖打破并列来声称最优。
func TestTiesNotBrokenArbitrarily(t *testing.T) {
	in := Input{
		Applicants: []ApplicantInput{
			{Name: "a1", Preferences: [][]string{{"j1"}, {"j2"}}},
			{Name: "a2", Preferences: [][]string{{"j1"}}},
		},
		Jobs: []JobInput{
			{Name: "j1", Preferences: [][]string{{"a1", "a2"}}},
			{Name: "j2", Preferences: [][]string{{"a1"}}},
		},
	}
	p, res := solveInput(t, in)
	if res.BestSize != 2 {
		t.Fatalf("BestSize = %d, want 2 (任意打破并列只得 1)", res.BestSize)
	}
	if got := breakTiesGS(p); got != 1 {
		t.Fatalf("breakTiesGS size = %d, want 1 (打破并列确实更差)", got)
	}
	// 最大匹配应为 a1-j2, a2-j1。
	if res.BestAssign[0] != 1 || res.BestAssign[1] != 0 {
		t.Fatalf("BestAssign = %v, want [1 0]", res.BestAssign)
	}
}

// 岗位容量: 容量 2 的岗位可同时接纳两人。
func TestCapacity(t *testing.T) {
	_, res := solveInput(t, Input{
		Applicants: []ApplicantInput{
			{Name: "a1", Preferences: [][]string{{"j1"}}},
			{Name: "a2", Preferences: [][]string{{"j1"}}},
			{Name: "a3", Preferences: [][]string{{"j1"}}},
		},
		Jobs: []JobInput{
			{Name: "j1", Capacity: 2, Preferences: [][]string{{"a1", "a2", "a3"}}},
		},
	})
	if res.BestSize != 2 {
		t.Fatalf("BestSize = %d, want 2", res.BestSize)
	}
}

// 非互相可接受的对不得进入匹配。
func TestIncompleteLists(t *testing.T) {
	p, res := solveInput(t, Input{
		Applicants: []ApplicantInput{
			{Name: "a1", Preferences: [][]string{{"j1"}}},
			{Name: "a2", Preferences: [][]string{{"j1"}}},
		},
		Jobs: []JobInput{
			{Name: "j1", Preferences: [][]string{{"a1"}}}, // a2 不可接受
		},
	})
	if res.BestSize != 1 {
		t.Fatalf("BestSize = %d, want 1", res.BestSize)
	}
	if res.BestAssign[0] != 0 || res.BestAssign[1] != -1 {
		t.Fatalf("BestAssign = %v, want [0 -1]", res.BestAssign)
	}
	_ = p
}

// 证据必须覆盖所有互相可接受但未配对的组合, 且最终匹配无阻挡对。
func TestEvidenceCoverage(t *testing.T) {
	p, res := solveInput(t, Input{
		Applicants: []ApplicantInput{
			{Name: "a1", Preferences: [][]string{{"j1"}, {"j2"}}},
			{Name: "a2", Preferences: [][]string{{"j1", "j2"}}},
		},
		Jobs: []JobInput{
			{Name: "j1", Preferences: [][]string{{"a1", "a2"}}},
			{Name: "j2", Preferences: [][]string{{"a2"}, {"a1"}}},
		},
	})
	if res.BlockingHits != 0 {
		t.Fatalf("BlockingHits = %d, want 0", res.BlockingHits)
	}
	want := 0
	for a := range p.Applicants {
		for j := range p.AppRank[a] {
			if p.acceptable(a, j) && res.BestAssign[a] != j {
				want++
			}
		}
	}
	if len(res.Evidence) != want {
		t.Fatalf("evidence covers %d pairs, want %d", len(res.Evidence), want)
	}
}

// 随机小实例上与独立的暴力枚举交叉验证最大规模与稳定性。
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		in := randomInput(rng, 1+rng.Intn(5), 1+rng.Intn(4))
		p := mustBuild(t, in)
		res := Solve(p)
		if res.BlockingHits != 0 {
			t.Fatalf("trial %d: result has %d blocking pairs", trial, res.BlockingHits)
		}
		if got := bruteForceMaxStable(p); got != res.BestSize {
			t.Fatalf("trial %d: BestSize = %d, brute force = %d", trial, res.BestSize, got)
		}
	}
}

// randomInput 生成带并列与容量的随机实例。
func randomInput(rng *rand.Rand, na, nj int) Input {
	names := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + string(rune('1'+i))
		}
		return out
	}
	apps, jobs := names("a", na), names("j", nj)
	tiedGroups := func(pool []string) [][]string {
		perm := rng.Perm(len(pool))
		var groups [][]string
		i := 0
		for i < len(pool) && rng.Intn(4) > 0 { // 可能截断 -> 非完全偏好表
			size := 1 + rng.Intn(2)
			if i+size > len(pool) {
				size = len(pool) - i
			}
			var g []string
			for ; size > 0; size-- {
				g = append(g, pool[perm[i]])
				i++
			}
			groups = append(groups, g)
		}
		return groups
	}
	in := Input{}
	for _, a := range apps {
		in.Applicants = append(in.Applicants, ApplicantInput{Name: a, Preferences: tiedGroups(jobs)})
	}
	for _, j := range jobs {
		in.Jobs = append(in.Jobs, JobInput{Name: j, Capacity: 1 + rng.Intn(2), Preferences: tiedGroups(apps)})
	}
	return in
}

// bruteForceMaxStable 是测试内独立的暴力实现: 枚举所有匹配并做弱稳定判定。
func bruteForceMaxStable(p *Problem) int {
	n := len(p.Applicants)
	assign := make([]int, n)
	jobAssign := make([][]int, len(p.Jobs))
	best := 0
	var rec func(i, size int)
	rec = func(i, size int) {
		if i == n {
			if size > best && !hasBlocking(p, assign, jobAssign) {
				best = size
			}
			return
		}
		for j := range p.AppRank[i] {
			if p.acceptable(i, j) && len(jobAssign[j]) < p.Capacity[j] {
				assign[i] = j
				jobAssign[j] = append(jobAssign[j], i)
				rec(i+1, size+1)
				jobAssign[j] = jobAssign[j][:len(jobAssign[j])-1]
			}
		}
		assign[i] = -1
		rec(i+1, size)
	}
	rec(0, 0)
	return best
}

// breakTiesGS 按列出顺序任意打破并列后运行申请者提议的 Gale-Shapley,
// 用于在测试中展示"任意打破并列"可能得到更小的匹配。
func breakTiesGS(p *Problem) int {
	na := len(p.Applicants)
	// 打平后的严格名次: 按 map 之外的确定顺序展开名次表。
	flatApp := make([][]int, na)
	for a := range p.Applicants {
		type jr struct{ j, r int }
		var list []jr
		for j, r := range p.AppRank[a] {
			if p.acceptable(a, j) {
				list = append(list, jr{j, r})
			}
		}
		for i := 0; i+1 < len(list); i++ {
			for k := i + 1; k < len(list); k++ {
				if list[k].r < list[i].r || (list[k].r == list[i].r && list[k].j < list[i].j) {
					list[i], list[k] = list[k], list[i]
				}
			}
		}
		for _, e := range list {
			flatApp[a] = append(flatApp[a], e.j)
		}
	}
	jobRank := make([]map[int]int, len(p.Jobs))
	for j := range p.Jobs {
		jobRank[j] = map[int]int{}
		next := 0
		for r := 0; ; r++ {
			found := false
			for a, rank := range p.JobRank[j] {
				if rank == r {
					if _, ok := jobRank[j][a]; !ok {
						jobRank[j][a] = next
						next++
						found = true
					}
				}
			}
			if !found && len(jobRank[j]) == len(p.JobRank[j]) {
				break
			}
			if !found && r > len(p.JobRank[j]) {
				break
			}
		}
	}
	nextIdx := make([]int, na)
	matchJob := make([][]int, len(p.Jobs))
	free := make([]int, na)
	for a := range free {
		free[a] = a
	}
	for len(free) > 0 {
		a := free[len(free)-1]
		if nextIdx[a] >= len(flatApp[a]) {
			free = free[:len(free)-1]
			continue
		}
		j := flatApp[a][nextIdx[a]]
		nextIdx[a]++
		if len(matchJob[j]) < p.Capacity[j] {
			matchJob[j] = append(matchJob[j], a)
			free = free[:len(free)-1]
			continue
		}
		worst, worstRank := -1, -1
		for _, b := range matchJob[j] {
			if jobRank[j][b] > worstRank {
				worstRank, worst = jobRank[j][b], b
			}
		}
		if jobRank[j][a] < worstRank {
			for k, b := range matchJob[j] {
				if b == worst {
					matchJob[j][k] = a
				}
			}
			free = free[:len(free)-1]
			free = append(free, worst)
		}
	}
	size := 0
	for _, list := range matchJob {
		size += len(list)
	}
	return size
}
