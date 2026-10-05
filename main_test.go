package main

import (
	"encoding/json"
	"math/rand"
	"testing"
)

func buildFromJSON(t *testing.T, js string) *Problem {
	t.Helper()
	var inst Instance
	if err := json.Unmarshal([]byte(js), &inst); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	p, err := Build(&inst)
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	return p
}

// 核心反例:任意打破并列会丢失最优规模。
// h1 并列看待 a1/a2;若按 h1: a1>a2 打破并列,稳定匹配只有 {a1-h1}(规模 1),
// 但最大规模弱稳定匹配是 {a1-h2, a2-h1}(规模 2)。
const tieTradeoff = `{
  "applicants": [
    {"name": "a1", "prefs": [["h1"], ["h2"]]},
    {"name": "a2", "prefs": [["h1"]]}
  ],
  "hospitals": [
    {"name": "h1", "capacity": 1, "prefs": [["a1", "a2"]]},
    {"name": "h2", "capacity": 1, "prefs": [["a1"]]}
  ]
}`

func TestTieBreakWouldLoseOptimality(t *testing.T) {
	p := buildFromJSON(t, tieTradeoff)
	s := NewSolver(p)
	best := s.Solve()
	if best.Size() != 2 {
		t.Fatalf("最大弱稳定匹配规模应为 2,得到 %d", best.Size())
	}
	if bp := BlockingPairs(VerifyWeakStability(p, best)); len(bp) > 0 {
		t.Fatalf("最优匹配不应有阻挡对: %v", bp)
	}
	// 并列打破后得到的规模 1 匹配 {a1-h1} 确实是弱稳定的(但不是最大):
	// a2 未匹配且想要 h1,但 h1 对 a1/a2 并列,不构成阻挡对。
	small := &Matching{Cur: []int{0, -1}}
	if bp := BlockingPairs(VerifyWeakStability(p, small)); len(bp) > 0 {
		t.Fatalf("{a1-h1} 应为弱稳定(规模 1),却报告阻挡对: %v", bp)
	}
}

// 阻挡对必须双方严格更优:医院一侧并列即不阻挡。
func TestBlockingRequiresBothSidesStrict(t *testing.T) {
	p := buildFromJSON(t, tieTradeoff)
	// {a1-h2, a2-h1}: a1 严格偏好 h1,但 h1 对 a1/a2 并列 -> 不阻挡。
	m := &Matching{Cur: []int{1, 0}}
	ev := VerifyWeakStability(p, m)
	if bp := BlockingPairs(ev); len(bp) > 0 {
		t.Fatalf("不应存在阻挡对: %v", bp)
	}
	found := false
	for _, e := range ev {
		if e.A == 0 && e.H == 0 {
			found = true
			if !e.AEager || e.Blocking {
				t.Fatalf("(a1,h1) 应为申请者渴望但非阻挡: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("缺少 (a1,h1) 的检查证据")
	}
}

// 双方严格更优时必须检出阻挡对。
func TestStrictBlockingDetected(t *testing.T) {
	p := buildFromJSON(t, `{
	  "applicants": [
	    {"name": "a1", "prefs": [["h1"], ["h2"]]},
	    {"name": "a2", "prefs": [["h1"]]}
	  ],
	  "hospitals": [
	    {"name": "h1", "capacity": 1, "prefs": [["a1"], ["a2"]]},
	    {"name": "h2", "capacity": 1, "prefs": [["a1"]]}
	  ]
	}`)
	// {a1-h2, a2-h1}: a1 严格偏好 h1,h1 严格偏好 a1 胜于 a2 -> 阻挡。
	m := &Matching{Cur: []int{1, 0}}
	bp := BlockingPairs(VerifyWeakStability(p, m))
	if len(bp) != 1 || bp[0].A != 0 || bp[0].H != 0 {
		t.Fatalf("应检出唯一阻挡对 (a1,h1),得到 %v", bp)
	}
}

// 容量约束:cap=2,3 名申请者最多匹配 2 人。
func TestCapacityLimit(t *testing.T) {
	p := buildFromJSON(t, `{
	  "applicants": [
	    {"name": "a1", "prefs": [["h1"]]},
	    {"name": "a2", "prefs": [["h1"]]},
	    {"name": "a3", "prefs": [["h1"]]}
	  ],
	  "hospitals": [
	    {"name": "h1", "capacity": 2, "prefs": [["a1", "a2", "a3"]]}
	  ]
	}`)
	s := NewSolver(p)
	if got := s.Solve().Size(); got != 2 {
		t.Fatalf("容量 2 时最大规模应为 2,得到 %d", got)
	}
}

// 无并列的经典情形退化为标准稳定匹配规模。
func TestNoTiesClassical(t *testing.T) {
	p := buildFromJSON(t, `{
	  "applicants": [
	    {"name": "a1", "prefs": [["h1"], ["h2"], ["h3"]]},
	    {"name": "a2", "prefs": [["h2"], ["h1"], ["h3"]]},
	    {"name": "a3", "prefs": [["h1"], ["h3"], ["h2"]]}
	  ],
	  "hospitals": [
	    {"name": "h1", "capacity": 1, "prefs": [["a2"], ["a1"], ["a3"]]},
	    {"name": "h2", "capacity": 1, "prefs": [["a1"], ["a3"], ["a2"]]},
	    {"name": "h3", "capacity": 1, "prefs": [["a1"], ["a2"], ["a3"]]}
	  ]
	}`)
	s := NewSolver(p)
	best := s.Solve()
	if best.Size() != 3 {
		t.Fatalf("完备列表下最大规模应为 3,得到 %d", best.Size())
	}
}

// 输入校验。
func TestBuildValidation(t *testing.T) {
	cases := []string{
		`{"applicants": [], "hospitals": []}`, // 无申请者
		`{"applicants": [{"name":"a1","prefs":[]}], "hospitals": [{"name":"h1","capacity":0,"prefs":[]}]}`, // 容量 0
		`{"applicants": [{"name":"a1","prefs":[["hx"]]}], "hospitals": []}`,                                // 引用不存在的医院
		`{"applicants": [{"name":"a1","prefs":[]},{"name":"a1","prefs":[]}], "hospitals": []}`,             // 重名
	}
	for _, js := range cases {
		var inst Instance
		if err := json.Unmarshal([]byte(js), &inst); err != nil {
			t.Fatalf("JSON 解析失败: %v", err)
		}
		if _, err := Build(&inst); err == nil {
			t.Fatalf("应拒绝非法输入: %s", js)
		}
	}
	// 超过 10 名申请者应拒绝。
	js := `{"applicants": [`
	for i := 0; i < 11; i++ {
		if i > 0 {
			js += ","
		}
		js += `{"name": "a` + string(rune('0'+i)) + `", "prefs": []}`
	}
	js += `], "hospitals": []}`
	var inst Instance
	if err := json.Unmarshal([]byte(js), &inst); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if _, err := Build(&inst); err == nil {
		t.Fatal("应拒绝超过 10 名申请者")
	}
}

// bruteForceBest 是不带任何剪枝的暴力枚举,用于交叉验证求解器。
// 返回最大规模及达到该规模的弱稳定匹配个数。
func bruteForceBest(p *Problem) (int, int) {
	cur := make([]int, p.NumA)
	occ := make([]int, p.NumH)
	best, count := 0, 0
	var rec func(a, matched int)
	rec = func(a, matched int) {
		if a == p.NumA {
			m := &Matching{Cur: append([]int(nil), cur...)}
			if len(BlockingPairs(VerifyWeakStability(p, m))) > 0 {
				return
			}
			if matched > best {
				best = matched
				count = 1
			} else if matched == best {
				count++
			}
			return
		}
		for _, h := range p.AHosps[a] {
			if !p.Acceptable(a, h) || occ[h] >= p.Cap[h] {
				continue
			}
			cur[a] = h
			occ[h]++
			rec(a+1, matched+1)
			occ[h]--
			cur[a] = -1
		}
		cur[a] = -1
		rec(a+1, matched)
	}
	rec(0, 0)
	return best, count
}

// 随机实例上与暴力枚举交叉验证(含并列与容量)。
func TestSolverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		nA := 1 + rng.Intn(6)
		nH := 1 + rng.Intn(4)
		inst := Instance{}
		for i := 0; i < nA; i++ {
			inst.Applicants = append(inst.Applicants, ApplicantInput{
				Name:  string(rune('a' + i)),
				Prefs: randomPrefs(rng, nH, 'h'),
			})
		}
		for j := 0; j < nH; j++ {
			inst.Hospitals = append(inst.Hospitals, HospitalInput{
				Name:     string(rune('h' + j)),
				Capacity: 1 + rng.Intn(3),
				Prefs:    randomPrefs(rng, nA, 'a'),
			})
		}
		p, err := Build(&inst)
		if err != nil {
			t.Fatalf("trial %d: Build 失败: %v", trial, err)
		}
		s := NewSolver(p)
		got := s.Solve().Size()
		want, wantCount := bruteForceBest(p)
		if got != want {
			t.Fatalf("trial %d: 求解器得到 %d,暴力枚举得到 %d\n实例: %+v", trial, got, want, inst)
		}
		if s.BestCount != wantCount {
			t.Fatalf("trial %d: 最优匹配个数求解器得到 %d,暴力枚举得到 %d\n实例: %+v", trial, s.BestCount, wantCount, inst)
		}
	}
}

// randomPrefs 生成随机分档偏好(含并列、含截断列表)。
func randomPrefs(rng *rand.Rand, n int, prefix rune) [][]string {
	perm := rng.Perm(n)
	keep := 1 + rng.Intn(n) // 列表长度(截断)
	var prefs [][]string
	for _, idx := range perm[:keep] {
		name := string(prefix + rune(idx))
		if len(prefs) > 0 && rng.Intn(3) == 0 {
			// 以 1/3 概率并入上一档,形成并列
			prefs[len(prefs)-1] = append(prefs[len(prefs)-1], name)
		} else {
			prefs = append(prefs, []string{name})
		}
	}
	return prefs
}
