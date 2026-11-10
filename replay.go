package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Input 描述一次简化 Kerberos 委派演练的全部输入。
type Input struct {
	// QueryTime 为复核时刻（RFC3339），只评估该时刻的可达性。
	QueryTime string        `json:"query_time"`
	User      UserSpec      `json:"user"`
	Tickets   []TicketEvent `json:"tickets"`
	// Services 为服务主体配置：委派模式与服务端委派名单。
	Services map[string]ServiceSpec `json:"services"`
	// ResourceAllow 为资源端允许名单：目标服务 -> 允许代表用户来访的中间服务。
	ResourceAllow map[string][]string `json:"resource_allow"`
}

type UserSpec struct {
	Name string `json:"name"`
	// DelegationDisallowed 对应用户账号的“账户敏感，不能被委派”标志。
	DelegationDisallowed bool `json:"delegation_disallowed"`
}

type TicketEvent struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Service     string `json:"service"`
	Kind        string `json:"kind"`
	Forwardable bool   `json:"forwardable"`
	NotBefore   string `json:"not_before"`
	ExpiresAt   string `json:"expires_at"`
}

type ServiceSpec struct {
	// Mode 取值 constrained / unconstrained。
	Mode       string   `json:"mode"`
	DelegateTo []string `json:"delegate_to"`
}

type Reach struct {
	Service string `json:"service"`
	Hops    int    `json:"hops"`
	// Chain 为身份链路，起点是用户，后续每跳一个服务主体。
	Chain []string `json:"chain"`
	// Tickets 为支撑该链路的票据 ID，委派跳使用 synth: 前缀的合成票据。
	Tickets []string `json:"tickets"`
}

type Blocked struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	Ticket string `json:"ticket,omitempty"`
}

type PolicyResult struct {
	Reachable []Reach   `json:"reachable"`
	Blocked   []Blocked `json:"blocked"`
	Cycles    []Blocked `json:"cycles"`
}

type IgnoredTicket struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type Result struct {
	QueryTime      string                  `json:"query_time"`
	User           string                  `json:"user"`
	IgnoredTickets []IgnoredTicket         `json:"ignored_tickets"`
	Policies       map[string]PolicyResult `json:"policies"`
}

type bfsState struct {
	service string
	fwd     bool
	chain   []string
	tickets []string
	nbf     time.Time
	exp     time.Time
}

// ParseInput 解析并基础校验输入 JSON。
func ParseInput(data []byte) (*Input, error) {
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("解析输入 JSON 失败: %w", err)
	}
	if in.QueryTime == "" {
		return nil, fmt.Errorf("缺少 query_time")
	}
	if in.User.Name == "" {
		return nil, fmt.Errorf("缺少 user.name")
	}
	if in.Services == nil {
		in.Services = map[string]ServiceSpec{}
	}
	if in.ResourceAllow == nil {
		in.ResourceAllow = map[string][]string{}
	}
	for name, svc := range in.Services {
		switch svc.Mode {
		case "constrained", "unconstrained":
		default:
			return nil, fmt.Errorf("服务 %s 的 mode 非法: %q", name, svc.Mode)
		}
	}
	return &in, nil
}

// Run 在给定策略（both/constrained/resource）下执行重放。
func Run(in *Input, policySel string) (*Result, error) {
	switch policySel {
	case "both", "constrained", "resource":
	default:
		return nil, fmt.Errorf("非法策略 %q，可选 both/constrained/resource", policySel)
	}

	queryAt, err := time.Parse(time.RFC3339, in.QueryTime)
	if err != nil {
		return nil, fmt.Errorf("query_time 解析失败: %w", err)
	}

	ignored, roots, direct := in.collectRoots(queryAt)
	targets := in.allTargets()

	result := &Result{
		QueryTime:      in.QueryTime,
		User:           in.User.Name,
		IgnoredTickets: ignored,
		Policies:       map[string]PolicyResult{},
	}

	policies := []string{"constrained", "resource"}
	if policySel != "both" {
		policies = []string{policySel}
	}
	for _, policy := range policies {
		pr := in.bfs(policy, queryAt, roots, targets)
		in.mergeDirectLeaves(&pr, direct)
		sort.Slice(pr.Reachable, func(i, j int) bool {
			if pr.Reachable[i].Hops != pr.Reachable[j].Hops {
				return pr.Reachable[i].Hops < pr.Reachable[j].Hops
			}
			return pr.Reachable[i].Service < pr.Reachable[j].Service
		})
		result.Policies[policy] = pr
	}
	return result, nil
}

// collectRoots 找出查询时刻用户直接持有的有效票据，并挑出可转发的作为 BFS 种子。
func (in *Input) collectRoots(at time.Time) ([]IgnoredTicket, map[string]*bfsState, map[string]Reach) {
	ignored := []IgnoredTicket{}
	roots := map[string]*bfsState{}
	direct := map[string]Reach{}

	for _, tk := range in.Tickets {
		if tk.Subject != in.User.Name {
			ignored = append(ignored, IgnoredTicket{tk.ID, "subject_mismatch"})
			continue
		}
		nbf, err := time.Parse(time.RFC3339, tk.NotBefore)
		if err != nil {
			ignored = append(ignored, IgnoredTicket{tk.ID, "bad_not_before"})
			continue
		}
		exp, err := time.Parse(time.RFC3339, tk.ExpiresAt)
		if err != nil {
			ignored = append(ignored, IgnoredTicket{tk.ID, "bad_expires_at"})
			continue
		}
		if !validAt(at, nbf, exp) {
			reason := "not_yet_valid"
			if !at.Before(exp) {
				reason = "expired"
			}
			ignored = append(ignored, IgnoredTicket{tk.ID, reason})
			continue
		}
		if _, ok := direct[tk.Service]; !ok {
			direct[tk.Service] = Reach{
				Service: tk.Service,
				Hops:    1,
				Chain:   []string{in.User.Name, tk.Service},
				Tickets: []string{tk.ID},
			}
		}
		// TGT 用于换发 TGS，不作为可发起委派的服务节点进入 BFS。
		if tk.Kind == "tgt" {
			continue
		}
		if _, ok := roots[tk.Service]; !ok {
			roots[tk.Service] = &bfsState{
				service: tk.Service,
				fwd:     tk.Forwardable,
				chain:   []string{in.User.Name, tk.Service},
				tickets: []string{tk.ID},
				nbf:     nbf,
				exp:     exp,
			}
		}
	}
	sort.Slice(ignored, func(i, j int) bool { return ignored[i].ID < ignored[j].ID })
	return ignored, roots, direct
}

// allTargets 汇总所有可能的委派目标，名单中出现但未定义的服务也保留以便诊断。
func (in *Input) allTargets() []string {
	set := map[string]struct{}{}
	for name := range in.Services {
		set[name] = struct{}{}
	}
	for _, svc := range in.Services {
		for _, target := range svc.DelegateTo {
			set[target] = struct{}{}
		}
	}
	for target, allowed := range in.ResourceAllow {
		set[target] = struct{}{}
		for _, src := range allowed {
			set[src] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func validAt(at, nbf, exp time.Time) bool {
	return !at.Before(nbf) && at.Before(exp)
}

func stateKey(s *bfsState) string {
	return fmt.Sprintf("%s|fwd=%t", s.service, s.fwd)
}

func synthID(from, to string, hop int) string {
	return fmt.Sprintf("synth:%s->%s#%d", from, to, hop)
}

func reachOf(s *bfsState) Reach {
	return Reach{
		Service: s.service,
		Hops:    len(s.tickets),
		Chain:   append([]string{}, s.chain...),
		Tickets: append([]string{}, s.tickets...),
	}
}

// bfs 以可转发证据票状态为节点做广度优先搜索，首次抵达即最短票据链。
func (in *Input) bfs(policy string, at time.Time, roots map[string]*bfsState, targets []string) PolicyResult {
	pr := PolicyResult{Reachable: []Reach{}, Blocked: []Blocked{}, Cycles: []Blocked{}}
	visited := map[string]bool{}

	seedNames := make([]string, 0, len(roots))
	for name := range roots {
		seedNames = append(seedNames, name)
	}
	sort.Strings(seedNames)

	queue := []*bfsState{}
	for _, name := range seedNames {
		state := roots[name]
		visited[stateKey(state)] = true
		if state.fwd {
			pr.Reachable = append(pr.Reachable, reachOf(state))
		}
		queue = append(queue, state)
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		evidence := cur.tickets[len(cur.tickets)-1]

		for _, target := range targets {
			if target == cur.service {
				continue
			}
			if !validAt(at, cur.nbf, cur.exp) {
				pr.Blocked = append(pr.Blocked, Blocked{cur.service, target, "evidence_expired", evidence})
				continue
			}
			if in.User.DelegationDisallowed {
				pr.Blocked = append(pr.Blocked, Blocked{cur.service, target, "user_delegation_disallowed", evidence})
				continue
			}
			if !cur.fwd {
				pr.Blocked = append(pr.Blocked, Blocked{cur.service, target, "evidence_not_forwardable", evidence})
				continue
			}
			if _, known := in.Services[target]; !known {
				pr.Blocked = append(pr.Blocked, Blocked{cur.service, target, "unknown_target_service", evidence})
				continue
			}
			if !in.authorized(policy, cur.service, target) {
				reason := "not_in_service_delegation_list"
				if policy == "resource" {
					reason = "not_in_resource_allow_list"
				}
				pr.Blocked = append(pr.Blocked, Blocked{cur.service, target, reason, evidence})
				continue
			}

			next := &bfsState{
				service: target,
				fwd:     cur.fwd,
				chain:   append(append([]string{}, cur.chain...), target),
				tickets: append(append([]string{}, cur.tickets...), synthID(cur.service, target, len(cur.chain))),
				nbf:     cur.nbf,
				exp:     cur.exp,
			}
			key := stateKey(next)
			if visited[key] {
				pr.Cycles = append(pr.Cycles, Blocked{cur.service, target, "cycle_suppressed", next.tickets[len(next.tickets)-1]})
				continue
			}
			visited[key] = true
			pr.Reachable = append(pr.Reachable, reachOf(next))
			queue = append(queue, next)
		}
	}

	sort.Slice(pr.Blocked, func(i, j int) bool {
		if pr.Blocked[i].From != pr.Blocked[j].From {
			return pr.Blocked[i].From < pr.Blocked[j].From
		}
		return pr.Blocked[i].To < pr.Blocked[j].To
	})
	sort.Slice(pr.Cycles, func(i, j int) bool {
		if pr.Cycles[i].From != pr.Cycles[j].From {
			return pr.Cycles[i].From < pr.Cycles[j].From
		}
		return pr.Cycles[i].To < pr.Cycles[j].To
	})
	return pr
}

// mergeDirectLeaves 把仅持有不可转发票据、无法进入 BFS 的直达服务补进结果。
func (in *Input) mergeDirectLeaves(pr *PolicyResult, direct map[string]Reach) {
	index := map[string]int{}
	for i, reach := range pr.Reachable {
		index[reach.Service] = i
	}
	for service, leaf := range direct {
		if pos, ok := index[service]; !ok {
			index[service] = len(pr.Reachable)
			pr.Reachable = append(pr.Reachable, leaf)
		} else if leaf.Hops < pr.Reachable[pos].Hops {
			// 直达链始终短于委派链，保留最短票据链。
			pr.Reachable[pos] = leaf
		}
	}
}

// authorized 按策略判断 from 是否可代表用户委派到 to。
func (in *Input) authorized(policy, from, to string) bool {
	src, ok := in.Services[from]
	if !ok {
		return false
	}
	if src.Mode == "unconstrained" {
		_, known := in.Services[to]
		return known
	}
	switch policy {
	case "constrained":
		return contains(src.DelegateTo, to)
	case "resource":
		allowed, ok := in.ResourceAllow[to]
		return ok && contains(allowed, from)
	default:
		return false
	}
}
