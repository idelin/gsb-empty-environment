package main

import "fmt"

// ApplicantInput 描述一位申请者。Preferences 按偏好从强到弱排列,
// 同一内层切片中的岗位并列(名次相同)。
type ApplicantInput struct {
	Name        string     `json:"name"`
	Preferences [][]string `json:"preferences"`
}

// JobInput 描述一个岗位。Capacity 为名额(省略时默认为 1)。
// Preferences 中同一内层切片的申请者并列。
type JobInput struct {
	Name        string     `json:"name"`
	Capacity    int        `json:"capacity"`
	Preferences [][]string `json:"preferences"`
}

// Input 是求解器的 JSON 输入格式。
type Input struct {
	Applicants []ApplicantInput `json:"applicants"`
	Jobs       []JobInput       `json:"jobs"`
}

// Problem 是编译后的匹配问题。名次从 0 开始,越小越偏好;
// 不在对方偏好表中即视为不可接受。
type Problem struct {
	Applicants []string
	Jobs       []string
	Capacity   []int
	AppRank    []map[int]int // 申请者 -> 岗位 -> 名次
	JobRank    []map[int]int // 岗位 -> 申请者 -> 名次
}

// BuildProblem 校验输入并构造名次表。
func BuildProblem(in Input) (*Problem, error) {
	p := &Problem{}
	appIndex := make(map[string]int)
	for _, a := range in.Applicants {
		if a.Name == "" {
			return nil, fmt.Errorf("存在无名申请者")
		}
		if _, dup := appIndex[a.Name]; dup {
			return nil, fmt.Errorf("申请者 %q 重复", a.Name)
		}
		appIndex[a.Name] = len(p.Applicants)
		p.Applicants = append(p.Applicants, a.Name)
	}
	jobIndex := make(map[string]int)
	for _, j := range in.Jobs {
		if j.Name == "" {
			return nil, fmt.Errorf("存在无名岗位")
		}
		if _, dup := jobIndex[j.Name]; dup {
			return nil, fmt.Errorf("岗位 %q 重复", j.Name)
		}
		jobIndex[j.Name] = len(p.Jobs)
		p.Jobs = append(p.Jobs, j.Name)
		cap := j.Capacity
		if cap == 0 {
			cap = 1
		}
		if cap < 0 {
			return nil, fmt.Errorf("岗位 %q 容量为负", j.Name)
		}
		p.Capacity = append(p.Capacity, cap)
	}
	p.AppRank = make([]map[int]int, len(p.Applicants))
	for i, a := range in.Applicants {
		rank := make(map[int]int)
		for r, group := range a.Preferences {
			if len(group) == 0 {
				return nil, fmt.Errorf("申请者 %q 的第 %d 组偏好为空", a.Name, r)
			}
			for _, jn := range group {
				j, ok := jobIndex[jn]
				if !ok {
					return nil, fmt.Errorf("申请者 %q 的偏好表含未知岗位 %q", a.Name, jn)
				}
				if _, dup := rank[j]; dup {
					return nil, fmt.Errorf("申请者 %q 的偏好表中岗位 %q 重复出现", a.Name, jn)
				}
				rank[j] = r
			}
		}
		p.AppRank[i] = rank
	}
	p.JobRank = make([]map[int]int, len(p.Jobs))
	for j, jd := range in.Jobs {
		rank := make(map[int]int)
		for r, group := range jd.Preferences {
			if len(group) == 0 {
				return nil, fmt.Errorf("岗位 %q 的第 %d 组偏好为空", jd.Name, r)
			}
			for _, an := range group {
				a, ok := appIndex[an]
				if !ok {
					return nil, fmt.Errorf("岗位 %q 的偏好表含未知申请者 %q", jd.Name, an)
				}
				if _, dup := rank[a]; dup {
					return nil, fmt.Errorf("岗位 %q 的偏好表中申请者 %q 重复出现", jd.Name, an)
				}
				rank[a] = r
			}
		}
		p.JobRank[j] = rank
	}
	return p, nil
}

// acceptable 报告 (a, j) 是否为可接受对: 必须互相出现在对方偏好表中。
func (p *Problem) acceptable(a, j int) bool {
	if _, ok := p.AppRank[a][j]; !ok {
		return false
	}
	_, ok := p.JobRank[j][a]
	return ok
}
