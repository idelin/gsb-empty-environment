package main

import (
	"fmt"
	"sort"
)

// MaxApplicants 是精确求解支持的申请者上限。
const MaxApplicants = 10

// ApplicantInput 描述一个申请者的 JSON 输入。
// Prefs 按偏好从强到弱排列,同一内层数组中的医院互为并列。
type ApplicantInput struct {
	Name  string     `json:"name"`
	Prefs [][]string `json:"prefs"`
}

// HospitalInput 描述一个岗位的 JSON 输入,语义同 ApplicantInput。
type HospitalInput struct {
	Name     string     `json:"name"`
	Capacity int        `json:"capacity"`
	Prefs    [][]string `json:"prefs"`
}

// Instance 是求解器的完整输入。
type Instance struct {
	Applicants []ApplicantInput `json:"applicants"`
	Hospitals  []HospitalInput  `json:"hospitals"`
}

// Problem 是编译后的内部表示:排名用整数表示,数值越小越受偏好,
// 并列双方共享同一排名值。未出现在对方列表中即互不可接受。
type Problem struct {
	NumA   int
	NumH   int
	ANames []string
	HNames []string
	Cap    []int
	ARank  []map[int]int // ARank[a][h] = a 对 h 的排名
	HRank  []map[int]int // HRank[h][a] = h 对 a 的排名
	AHosps [][]int       // 每个申请者可接受的医院(按偏好序)
	HApps  [][]int       // 每个医院可接受的申请者
}

// Build 校验输入并构造内部表示。
func Build(inst *Instance) (*Problem, error) {
	if len(inst.Applicants) == 0 {
		return nil, fmt.Errorf("至少需要一名申请者")
	}
	if len(inst.Applicants) > MaxApplicants {
		return nil, fmt.Errorf("申请者数量为 %d,超过精确求解上限 %d", len(inst.Applicants), MaxApplicants)
	}
	p := &Problem{
		NumA:   len(inst.Applicants),
		NumH:   len(inst.Hospitals),
		ARank:  make([]map[int]int, len(inst.Applicants)),
		HRank:  make([]map[int]int, len(inst.Hospitals)),
		Cap:    make([]int, len(inst.Hospitals)),
		AHosps: make([][]int, len(inst.Applicants)),
		HApps:  make([][]int, len(inst.Hospitals)),
	}
	hIndex := make(map[string]int)
	for i, h := range inst.Hospitals {
		if h.Name == "" {
			return nil, fmt.Errorf("第 %d 个医院名为空", i+1)
		}
		if _, dup := hIndex[h.Name]; dup {
			return nil, fmt.Errorf("医院名重复: %q", h.Name)
		}
		if h.Capacity < 1 {
			return nil, fmt.Errorf("医院 %q 容量必须 >= 1,得到 %d", h.Name, h.Capacity)
		}
		hIndex[h.Name] = i
		p.HNames = append(p.HNames, h.Name)
		p.Cap[i] = h.Capacity
	}
	aIndex := make(map[string]int)
	for i, a := range inst.Applicants {
		if a.Name == "" {
			return nil, fmt.Errorf("第 %d 个申请者名为空", i+1)
		}
		if _, dup := aIndex[a.Name]; dup {
			return nil, fmt.Errorf("申请者名重复: %q", a.Name)
		}
		aIndex[a.Name] = i
		p.ANames = append(p.ANames, a.Name)
	}
	rankOf := func(prefs [][]string, lookup map[string]int, owner string, side string) (map[int]int, []int, error) {
		rank := make(map[int]int)
		var order []int
		for r, group := range prefs {
			if len(group) == 0 {
				return nil, nil, fmt.Errorf("%s %q 的第 %d 档偏好为空", side, owner, r+1)
			}
			for _, name := range group {
				idx, ok := lookup[name]
				if !ok {
					return nil, nil, fmt.Errorf("%s %q 的偏好中引用了不存在的名字 %q", side, owner, name)
				}
				if _, dup := rank[idx]; dup {
					return nil, nil, fmt.Errorf("%s %q 的偏好中 %q 出现多次", side, owner, name)
				}
				rank[idx] = r
				order = append(order, idx)
			}
		}
		return rank, order, nil
	}
	for i, a := range inst.Applicants {
		rank, order, err := rankOf(a.Prefs, hIndex, a.Name, "申请者")
		if err != nil {
			return nil, err
		}
		p.ARank[i] = rank
		p.AHosps[i] = order
	}
	for j, h := range inst.Hospitals {
		rank, order, err := rankOf(h.Prefs, aIndex, h.Name, "医院")
		if err != nil {
			return nil, err
		}
		p.HRank[j] = rank
		p.HApps[j] = order
	}
	return p, nil
}

// Acceptable 报告 (a, h) 是否互可接受:只有互可接受的对才可能进入匹配或形成阻挡对。
func (p *Problem) Acceptable(a, h int) bool {
	if _, ok := p.ARank[a][h]; !ok {
		return false
	}
	_, ok := p.HRank[h][a]
	return ok
}

// APrefersStrict 报告 a 是否严格偏好 h1 胜于 h2(h2 为 -1 表示"不匹配",任何可接受医院都严格胜于它)。
func (p *Problem) APrefersStrict(a, h1, h2 int) bool {
	r1, ok := p.ARank[a][h1]
	if !ok {
		return false
	}
	if h2 < 0 {
		return true
	}
	r2, ok := p.ARank[a][h2]
	if !ok {
		return true
	}
	return r1 < r2
}

// Matching 表示一个匹配结果:Cur[a] 是 a 匹配到的医院,-1 表示未匹配。
type Matching struct {
	Cur []int
}

// Size 返回匹配规模(已匹配申请者数)。
func (m *Matching) Size() int {
	n := 0
	for _, h := range m.Cur {
		if h >= 0 {
			n++
		}
	}
	return n
}

// Occupants 计算每个医院的在院者列表。
func (p *Problem) Occupants(m *Matching) [][]int {
	occ := make([][]int, p.NumH)
	for a, h := range m.Cur {
		if h >= 0 {
			occ[h] = append(occ[h], a)
		}
	}
	return occ
}

// Validate 检查匹配本身的合法性:互可接受、容量不超限。
func (p *Problem) Validate(m *Matching) error {
	if len(m.Cur) != p.NumA {
		return fmt.Errorf("匹配向量长度为 %d,应为 %d", len(m.Cur), p.NumA)
	}
	occ := p.Occupants(m)
	for h, list := range occ {
		if len(list) > p.Cap[h] {
			return fmt.Errorf("医院 %s 超载: %d/%d", p.HNames[h], len(list), p.Cap[h])
		}
	}
	for a, h := range m.Cur {
		if h < 0 {
			continue
		}
		if h >= p.NumH {
			return fmt.Errorf("申请者 %s 匹配到非法医院编号 %d", p.ANames[a], h)
		}
		if !p.Acceptable(a, h) {
			return fmt.Errorf("申请者 %s 与医院 %s 并非互可接受", p.ANames[a], p.HNames[h])
		}
	}
	return nil
}

// orderApplicants 返回搜索顺序:可接受医院少的申请者先决策,以尽早剪枝。
func (p *Problem) orderApplicants() []int {
	order := make([]int, p.NumA)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return len(p.AHosps[order[i]]) < len(p.AHosps[order[j]])
	})
	return order
}
