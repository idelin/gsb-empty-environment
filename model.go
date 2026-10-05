package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"time"
)

// MatchRule 描述一个配捐资金池的配捐规则。
// 每当某项目获得 1 单位“非配捐来源”的基础资金，本池按 Ratio 配捐，
// 单项目配捐不超过 PerProjectCap（0 表示不限），全池合计不超过池总额。
// 配捐资金本身不会再次触发任何配捐。
type MatchRule struct {
	Ratio         float64 `json:"ratio"`
	PerProjectCap float64 `json:"per_project_cap"`
}

// Source 一笔资金来源。Match 非空时表示该来源为配捐池，只能通过配捐投出。
type Source struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Amount     float64    `json:"amount"`
	Categories []string   `json:"categories"` // 限定用途；空表示不限
	Expires    string     `json:"expires"`    // 有效期 YYYY-MM-DD；空表示长期有效
	Match      *MatchRule `json:"match"`
}

// Project 一个待资助项目，整笔达标才计收益。
type Project struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Goal     float64 `json:"goal"`    // 整笔目标金额
	Benefit  float64 `json:"benefit"` // 达标后计入的收益
	Deadline string  `json:"deadline"`
}

// Input 求解器输入。
type Input struct {
	MaxProjects int       `json:"max_projects"` // 0 表示默认 15
	Sources     []Source  `json:"sources"`
	Projects    []Project `json:"projects"`
}

const defaultMaxProjects = 15

func loadInput(r io.Reader) (*Input, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("解析输入 JSON 失败: %v", err)
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	return &in, nil
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (in *Input) validate() error {
	if in.MaxProjects == 0 {
		in.MaxProjects = defaultMaxProjects
	}
	if in.MaxProjects < 0 {
		return fmt.Errorf("max_projects 不能为负")
	}
	seen := map[string]bool{}
	for i := range in.Sources {
		s := &in.Sources[i]
		if s.ID == "" {
			return fmt.Errorf("来源 #%d 缺少 id", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("来源 id 重复: %s", s.ID)
		}
		seen[s.ID] = true
		if s.Amount < 0 {
			return fmt.Errorf("来源 %s 金额为负", s.ID)
		}
		if s.Expires != "" && !validDate(s.Expires) {
			return fmt.Errorf("来源 %s 有效期格式错误: %s", s.ID, s.Expires)
		}
		if s.Match != nil {
			if s.Match.Ratio < 0 {
				return fmt.Errorf("来源 %s 配捐比例为负", s.ID)
			}
			if s.Match.PerProjectCap < 0 {
				return fmt.Errorf("来源 %s 单项目配捐上限为负", s.ID)
			}
		}
	}
	seen = map[string]bool{}
	for i := range in.Projects {
		p := &in.Projects[i]
		if p.ID == "" {
			return fmt.Errorf("项目 #%d 缺少 id", i)
		}
		if seen[p.ID] {
			return fmt.Errorf("项目 id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if p.Goal <= 0 {
			return fmt.Errorf("项目 %s 目标金额必须为正", p.ID)
		}
		if p.Benefit < 0 {
			return fmt.Errorf("项目 %s 收益不能为负", p.ID)
		}
		if p.Deadline != "" && !validDate(p.Deadline) {
			return fmt.Errorf("项目 %s 截止日格式错误: %s", p.ID, p.Deadline)
		}
	}
	return nil
}

// eligible 判断来源 s 是否可投向项目 p：用途限定匹配，且来源有效期不早于项目截止日。
func eligible(s *Source, p *Project) bool {
	if len(s.Categories) > 0 {
		ok := false
		for _, c := range s.Categories {
			if c == p.Category {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if s.Expires != "" && p.Deadline != "" && p.Deadline > s.Expires {
		return false
	}
	return true
}

func (in *Input) totalExpiring() float64 {
	total := 0.0
	for i := range in.Sources {
		if in.Sources[i].Expires != "" {
			total += in.Sources[i].Amount
		}
	}
	return total
}
