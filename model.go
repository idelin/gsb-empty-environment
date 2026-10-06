package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

const eps = 1e-9

type Scenario struct {
	Name    string    `json:"name"`
	Volumes []float64 `json:"volumes"`
	Caps    []float64 `json:"caps,omitempty"`
}

type Instance struct {
	Name          string      `json:"name"`
	Items         int         `json:"items"`
	Bins          int         `json:"bins"`
	Capacity      []float64   `json:"capacity"`
	Handle        [][]float64 `json:"handle"`
	MoveCost      []float64   `json:"moveCost"`
	MoveLimit     int         `json:"moveLimit"`
	MoveCostLimit float64     `json:"moveCostLimit"`
	Observe       []int       `json:"observe"`
	Scenarios     []Scenario  `json:"scenarios"`
}

func loadInstance(path string) (*Instance, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in Instance
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func (in *Instance) validate() error {
	if in.Bins != 3 {
		return fmt.Errorf("仓位数必须为 3，当前为 %d", in.Bins)
	}
	if in.Items < 1 || in.Items > 10 {
		return fmt.Errorf("物品数必须在 1..10，当前为 %d", in.Items)
	}
	if len(in.Capacity) != in.Bins {
		return fmt.Errorf("capacity 长度必须为 %d", in.Bins)
	}
	if len(in.Handle) != in.Items {
		return fmt.Errorf("handle 行数必须等于物品数")
	}
	for i, row := range in.Handle {
		if len(row) != in.Bins {
			return fmt.Errorf("handle[%d] 长度必须为 %d", i, in.Bins)
		}
	}
	if len(in.MoveCost) != in.Items {
		return fmt.Errorf("moveCost 长度必须等于物品数")
	}
	if len(in.Scenarios) == 0 {
		return fmt.Errorf("至少需要一个场景")
	}
	for s := range in.Scenarios {
		if len(in.Scenarios[s].Volumes) != in.Items {
			return fmt.Errorf("场景 %s 的体积向量长度必须等于物品数", in.Scenarios[s].Name)
		}
		if in.Scenarios[s].Caps != nil && len(in.Scenarios[s].Caps) != in.Bins {
			return fmt.Errorf("场景 %s 的 caps 长度必须为 %d", in.Scenarios[s].Name, in.Bins)
		}
	}
	for _, it := range in.Observe {
		if it < 0 || it >= in.Items {
			return fmt.Errorf("observe 中的物品编号 %d 越界", it)
		}
	}
	return nil
}

func (in *Instance) caps(s int) []float64 {
	if in.Scenarios[s].Caps != nil {
		return in.Scenarios[s].Caps
	}
	return in.Capacity
}

func (in *Instance) loads(a []int, s int) []float64 {
	l := make([]float64, in.Bins)
	for i, b := range a {
		l[b] += in.Scenarios[s].Volumes[i]
	}
	return l
}

func (in *Instance) handleCost(a []int, s int) float64 {
	c := 0.0
	for i, b := range a {
		c += in.Handle[i][b] * in.Scenarios[s].Volumes[i]
	}
	return c
}

func (in *Instance) fits(a []int, scen []int) bool {
	for _, s := range scen {
		l := in.loads(a, s)
		cp := in.caps(s)
		for b := 0; b < in.Bins; b++ {
			if l[b] > cp[b]+eps {
				return false
			}
		}
	}
	return true
}

func (in *Instance) fitsAll(a []int) bool {
	scen := make([]int, len(in.Scenarios))
	for s := range scen {
		scen[s] = s
	}
	return in.fits(a, scen)
}

func pow(base, n int) int {
	p := 1
	for i := 0; i < n; i++ {
		p *= base
	}
	return p
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func binName(b int) string {
	return string(rune('A' + b))
}

func fmtAssign(a []int) string {
	s := "["
	for i, b := range a {
		if i > 0 {
			s += " "
		}
		s += binName(b)
	}
	return s + "]"
}

func fmtLoads(loads, caps []float64) string {
	s := ""
	for b := range loads {
		s += fmt.Sprintf(" 仓%s %.1f/%.1f", binName(b), loads[b], caps[b])
	}
	return s
}
