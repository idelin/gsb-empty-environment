package main

import (
	"fmt"
	"sort"
)

// Event 是单轮内的输入事件：request 表示某组新增一个许可请求，cancel 表示撤销一个尚未批准的请求。
type Event struct {
	Type  string `json:"type"` // "request" | "cancel"
	Group string `json:"group,omitempty"`
	ID    string `json:"id"`
}

// Round 是一轮的输入事件列表，事件按顺序生效后再进行本轮分配。
type Round struct {
	Events []Event `json:"events"`
}

// Scenario 描述完整输入：总容量、各组保底额度、逐轮事件。
type Scenario struct {
	Capacity int            `json:"capacity"`
	Groups   map[string]int `json:"groups"` // 组名 -> 保底额度
	Rounds   []Round        `json:"rounds"`
}

// Grant 表示一张已发出的许可。Borrowed 为 true 表示该许可使用了其他组让出的空闲份额。
type Grant struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Borrowed bool   `json:"borrowed"`
}

// RoundStat 记录本轮分配时各组的需求与获批数量，供验证保底使用。
type RoundStat struct {
	Group   string `json:"group"`
	Min     int    `json:"min"`
	Demand  int    `json:"demand"`
	Granted int    `json:"granted"`
}

// RoundResult 是一轮的输出：许可序列与统计。
type RoundResult struct {
	Round  int         `json:"round"`
	Grants []Grant     `json:"grants"`
	Stats  []RoundStat `json:"stats"`
}

// Output 是模拟器的完整输出。
type Output struct {
	Rounds []RoundResult `json:"rounds"`
	Report Report        `json:"report"`
}

// Validate 在运行前检查场景配置是否合法。
func Validate(sc Scenario) error {
	if sc.Capacity < 0 {
		return fmt.Errorf("capacity must be >= 0, got %d", sc.Capacity)
	}
	sum := 0
	for g, m := range sc.Groups {
		if m < 0 {
			return fmt.Errorf("group %q: negative min quota %d", g, m)
		}
		sum += m
	}
	if sum > sc.Capacity {
		return fmt.Errorf("sum of min quotas (%d) exceeds capacity (%d)", sum, sc.Capacity)
	}
	seen := map[string]bool{}
	for ri, r := range sc.Rounds {
		for ei, e := range r.Events {
			switch e.Type {
			case "request":
				if _, ok := sc.Groups[e.Group]; !ok {
					return fmt.Errorf("round %d event %d: unknown group %q", ri+1, ei+1, e.Group)
				}
				if e.ID == "" {
					return fmt.Errorf("round %d event %d: empty request id", ri+1, ei+1)
				}
				if seen[e.ID] {
					return fmt.Errorf("round %d event %d: duplicate request id %q", ri+1, ei+1, e.ID)
				}
				seen[e.ID] = true
			case "cancel":
				if e.ID == "" {
					return fmt.Errorf("round %d event %d: empty cancel id", ri+1, ei+1)
				}
			default:
				return fmt.Errorf("round %d event %d: unknown event type %q", ri+1, ei+1, e.Type)
			}
		}
	}
	return nil
}

// Simulator 是按轮分配的分组限流模拟器。
type Simulator struct {
	capacity int
	minQuota map[string]int
	groups   []string            // 排序后的组名，保证分配顺序确定
	queues   map[string][]string // 各组待批准请求（FIFO）
	pending  map[string]bool     // 未批准请求集合
	owner    map[string]string   // 请求 id -> 所属组
}

func NewSimulator(sc Scenario) (*Simulator, error) {
	if err := Validate(sc); err != nil {
		return nil, err
	}
	groups := make([]string, 0, len(sc.Groups))
	for g := range sc.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return &Simulator{
		capacity: sc.Capacity,
		minQuota: sc.Groups,
		groups:   groups,
		queues:   map[string][]string{},
		pending:  map[string]bool{},
		owner:    map[string]string{},
	}, nil
}

// Run 依次执行各轮，返回每轮的许可序列。
func (s *Simulator) Run(sc Scenario) []RoundResult {
	results := make([]RoundResult, 0, len(sc.Rounds))
	for i, r := range sc.Rounds {
		results = append(results, s.runRound(i+1, r))
	}
	return results
}

func (s *Simulator) runRound(n int, r Round) RoundResult {
	// 1. 按顺序应用本轮事件。取消只影响待批准请求；已发出的许可不撤回。
	for _, e := range r.Events {
		switch e.Type {
		case "request":
			s.queues[e.Group] = append(s.queues[e.Group], e.ID)
			s.pending[e.ID] = true
			s.owner[e.ID] = e.Group
		case "cancel":
			if !s.pending[e.ID] {
				continue // 已批准或不存在的请求：忽略
			}
			delete(s.pending, e.ID)
			g := s.owner[e.ID]
			delete(s.owner, e.ID)
			q := s.queues[g]
			for i, id := range q {
				if id == e.ID {
					s.queues[g] = append(q[:i], q[i+1:]...)
					break
				}
			}
		}
	}

	// 2. 为每个有需求的组保留保底额度；无需求组的份额自动空闲。
	res := RoundResult{Round: n}
	reserved := map[string]int{}
	used := 0
	for _, g := range s.groups {
		demand := len(s.queues[g])
		base := min(demand, s.minQuota[g])
		reserved[g] = base
		used += base
		res.Stats = append(res.Stats, RoundStat{Group: g, Min: s.minQuota[g], Demand: demand})
	}

	// 3. 第一阶段：发放各组保底额度（组内按 FIFO 出队）。
	granted := map[string]int{}
	for _, g := range s.groups {
		for k := 0; k < reserved[g]; k++ {
			id := s.pop(g)
			res.Grants = append(res.Grants, Grant{ID: id, Group: g})
			granted[g]++
		}
	}

	// 4. 第二阶段：空闲份额（含未用保底）按轮询暂借给仍有需求的组。
	leftover := s.capacity - used
	for leftover > 0 {
		progressed := false
		for _, g := range s.groups {
			if leftover == 0 {
				break
			}
			if len(s.queues[g]) > 0 {
				id := s.pop(g)
				res.Grants = append(res.Grants, Grant{ID: id, Group: g, Borrowed: true})
				granted[g]++
				leftover--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	for i := range res.Stats {
		res.Stats[i].Granted = granted[res.Stats[i].Group]
	}
	return res
}

func (s *Simulator) pop(g string) string {
	id := s.queues[g][0]
	s.queues[g] = s.queues[g][1:]
	delete(s.pending, id)
	delete(s.owner, id)
	return id
}
