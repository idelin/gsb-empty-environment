package solver

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Cents 以“分”为单位的金额，避免浮点误差。
type Cents int64

func (c *Cents) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("金额必须是数字: %s", string(b))
	}
	*c = Cents(math.Round(f * 100))
	return nil
}

func (c Cents) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(c)/100, 'f', 2, 64)), nil
}

// Source 一笔资金来源。Purpose 非空（且非 "*"）时只能投向同用途项目；
// Expires 为到期日（YYYY-MM-DD），空串表示永不过期。
type Source struct {
	ID      string `json:"id"`
	Amount  Cents  `json:"amount"`
	Purpose string `json:"purpose"`
	Expires string `json:"expires"`
}

// Project 一个待资助项目。只有累计到账（直接拨款+配捐）达到 Target 才计 Benefit。
type Project struct {
	ID      string `json:"id"`
	Purpose string `json:"purpose"`
	Target  Cents  `json:"target"`
	Benefit Cents  `json:"benefit"`
}

// MatchRule 配捐规则：当来源 SourceID（空=任意来源）向用途 Purpose（空=任意用途）
// 的项目拨款时，出资方 Funder 按 Ratio 追加配捐，总配捐额不超过 Cap。
// 配捐资金来自出资方预算，本身不再触发新的配捐。
type MatchRule struct {
	ID       string  `json:"id"`
	Funder   string  `json:"funder"`
	SourceID string  `json:"source_id"`
	Purpose  string  `json:"purpose"`
	Ratio    float64 `json:"ratio"`
	Cap      Cents   `json:"cap"`
	Expires  string  `json:"expires"`
}

// Input 求解器输入。
type Input struct {
	AsOf        string      `json:"as_of"` // 决策基准日 YYYY-MM-DD，空则取今天
	MaxProjects int         `json:"max_projects"`
	Sources     []Source    `json:"sources"`
	Projects    []Project   `json:"projects"`
	MatchRules  []MatchRule `json:"match_rules"`
}

const DefaultMaxProjects = 15

// ParseInput 解析并校验输入。
func ParseInput(data []byte) (*Input, error) {
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if in.AsOf == "" {
		in.AsOf = time.Now().Format("2006-01-02")
	}
	if err := checkDate("as_of", in.AsOf); err != nil {
		return nil, err
	}
	if in.MaxProjects <= 0 {
		in.MaxProjects = DefaultMaxProjects
	}
	seen := map[string]bool{}
	for i, s := range in.Sources {
		if s.ID == "" {
			return nil, fmt.Errorf("sources[%d] 缺少 id", i)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("来源 id 重复: %s", s.ID)
		}
		seen[s.ID] = true
		if s.Amount < 0 {
			return nil, fmt.Errorf("来源 %s 金额为负", s.ID)
		}
		if err := checkDate("来源 "+s.ID+" 的 expires", s.Expires); err != nil {
			return nil, err
		}
	}
	seen = map[string]bool{}
	for i, p := range in.Projects {
		if p.ID == "" {
			return nil, fmt.Errorf("projects[%d] 缺少 id", i)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("项目 id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if p.Target <= 0 {
			return nil, fmt.Errorf("项目 %s 的 target 必须为正", p.ID)
		}
		if p.Benefit < 0 {
			return nil, fmt.Errorf("项目 %s 的 benefit 不能为负", p.ID)
		}
	}
	seen = map[string]bool{}
	for i, r := range in.MatchRules {
		if r.ID == "" {
			return nil, fmt.Errorf("match_rules[%d] 缺少 id", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("配捐规则 id 重复: %s", r.ID)
		}
		seen[r.ID] = true
		if r.Funder == "" {
			return nil, fmt.Errorf("配捐规则 %s 缺少 funder", r.ID)
		}
		if r.Ratio < 0 {
			return nil, fmt.Errorf("配捐规则 %s 的 ratio 不能为负", r.ID)
		}
		if r.Cap < 0 {
			return nil, fmt.Errorf("配捐规则 %s 的 cap 不能为负", r.ID)
		}
		if err := checkDate("配捐规则 "+r.ID+" 的 expires", r.Expires); err != nil {
			return nil, err
		}
	}
	return &in, nil
}

func checkDate(field, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("%s: 日期格式应为 YYYY-MM-DD，得到 %q", field, v)
	}
	return nil
}

// purposeMatch pat 为空或 "*" 表示不限用途。
func purposeMatch(pat, purpose string) bool {
	return pat == "" || pat == "*" || pat == purpose
}
