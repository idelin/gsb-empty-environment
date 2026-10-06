package solver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
)

// ParseRat 解析有理数：支持 "p/q"、小数与整数形式。
func ParseRat(s string) (*Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational number %q", s)
	}
	return r, nil
}

type jsonWorld struct {
	ID     string `json:"id"`
	Prob   string `json:"prob"`
	Signal string `json:"signal"`
}

type jsonCapacity struct {
	Name     string   `json:"name"`
	Limit    string   `json:"limit"`
	Actions  []string `json:"actions"` // 简写：列出的动作在所有世界权重为 1
	WeightOf string   `json:"weight"`  // 简写：上述动作的权重（缺省 "1"）
	// 精细形式：逐 (world, action) 权重，覆盖简写。
	Entries []struct {
		World  string `json:"world"`
		Action string `json:"action"`
		Weight string `json:"weight"`
	} `json:"entries"`
}

type jsonProblem struct {
	Worlds     []jsonWorld                  `json:"worlds"`
	Actions    []string                     `json:"actions"`
	Losses     map[string]map[string]string `json:"losses"`
	Capacities []jsonCapacity               `json:"capacities"`
}

// Load 从 JSON 文件加载问题。
func Load(path string) (*Problem, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 从 JSON 字节流解析问题。
func Parse(data []byte) (*Problem, error) {
	var jp jsonProblem
	if err := json.Unmarshal(data, &jp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	p := &Problem{Actions: jp.Actions, Loss: make(map[string]map[string]*Rat)}
	for _, jw := range jp.Worlds {
		prob, err := ParseRat(jw.Prob)
		if err != nil {
			return nil, fmt.Errorf("world %q: %v", jw.ID, err)
		}
		p.Worlds = append(p.Worlds, World{ID: jw.ID, Prob: prob, Signal: jw.Signal})
	}
	for wid, row := range jp.Losses {
		p.Loss[wid] = make(map[string]*Rat)
		for a, s := range row {
			r, err := ParseRat(s)
			if err != nil {
				return nil, fmt.Errorf("loss[%q][%q]: %v", wid, a, err)
			}
			p.Loss[wid][a] = r
		}
	}
	for _, jc := range jp.Capacities {
		limit, err := ParseRat(jc.Limit)
		if err != nil {
			return nil, fmt.Errorf("capacity %q: %v", jc.Name, err)
		}
		cap := Capacity{Name: jc.Name, Limit: limit, Weight: make(map[string]map[string]*Rat)}
		weightOf := big.NewRat(1, 1)
		if jc.WeightOf != "" {
			weightOf, err = ParseRat(jc.WeightOf)
			if err != nil {
				return nil, fmt.Errorf("capacity %q: %v", jc.Name, err)
			}
		}
		for _, jw := range jp.Worlds {
			for _, a := range jc.Actions {
				setWeight(cap.Weight, jw.ID, a, new(Rat).Set(weightOf))
			}
		}
		for _, e := range jc.Entries {
			w, err := ParseRat(e.Weight)
			if err != nil {
				return nil, fmt.Errorf("capacity %q entry (%q,%q): %v", jc.Name, e.World, e.Action, err)
			}
			setWeight(cap.Weight, e.World, e.Action, w)
		}
		p.Capacities = append(p.Capacities, cap)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func setWeight(m map[string]map[string]*Rat, world, action string, w *Rat) {
	if m[world] == nil {
		m[world] = make(map[string]*Rat)
	}
	m[world][action] = w
}

// LoadStrategy 从 "signal=action,signal=action" 形式解析策略（供 check 使用）。
func LoadStrategy(s string) (Strategy, error) {
	st := make(Strategy)
	if s == "" {
		return st, nil
	}
	for _, pair := range splitComma(s) {
		eq := -1
		for i := 0; i < len(pair); i++ {
			if pair[i] == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 || eq == len(pair)-1 {
			return nil, fmt.Errorf("invalid assignment %q, want signal=action", pair)
		}
		st[pair[:eq]] = pair[eq+1:]
	}
	return st, nil
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
