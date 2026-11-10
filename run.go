package main

import (
	"fmt"
	"sort"
)

func (e *Engine) run() (*Report, []Violation, error) {
	reqs := make([]RequestSpec, len(e.scenario.Requests))
	copy(reqs, e.scenario.Requests)
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].ValidateAt != reqs[j].ValidateAt {
			return reqs[i].ValidateAt < reqs[j].ValidateAt
		}
		return i < j
	})

	var violations []Violation

	for _, req := range reqs {
		hostCfg, ok := e.scenario.Hosts[req.Host]
		if !ok {
			return nil, nil, fmt.Errorf("request %q references unknown host %q", req.ID, req.Host)
		}
		port := req.Port
		if port == 0 {
			port = hostCfg.Port
		}
		dialAt := req.DialAt
		if dialAt == 0 {
			dialAt = req.ValidateAt
		}
		if dialAt < req.ValidateAt {
			return nil, nil, fmt.Errorf("request %q dial_at precedes validate_at", req.ID)
		}

		validated, validateChain, err := e.resolveAt(req.ID, req.Host, req.ValidateAt)
		if err != nil {
			return nil, nil, err
		}

		parentVCs := make([]map[string]int, 0, len(validateChain))
		for _, ev := range validateChain {
			parentVCs = append(parentVCs, ev.VC)
		}

		blocked := []string{}
		for _, addr := range validated {
			forbid, err := e.isForbidden(addr)
			if err != nil {
				return nil, nil, err
			}
			if forbid {
				blocked = append(blocked, addr)
			}
		}
		checkParents := []int{e.events[len(e.events)-1].Seq}
		check := e.emit(EvValidate, req.ValidateAt, req.ID, req.Host, checkParents, map[string]string{
			"validated_addresses": joinAddrs(validated),
			"result":              passOrBlock(len(blocked) > 0),
			"blocked_addresses":   joinAddrs(blocked),
		}, parentVCs...)

		if len(blocked) > 0 {
			continue
		}

		e.advanceTo(dialAt)
		key := fmt.Sprintf("%s:%d", req.Host, port)

		if pool := e.pools[key]; len(pool) > 0 {
			c := pool[0]
			e.pools[key] = pool[1:]
			get := e.emit(EvPoolGet, dialAt, req.ID, req.Host, []int{check.Seq}, map[string]string{
				"pool_key":     key,
				"reused_peer":  c.peer,
				"conn_id":      fmt.Sprintf("%d", c.id),
				"conn_born_at": fmt.Sprintf("%d", c.bornAt),
			}, check.VC)
			forbid, err := e.isForbidden(peerIP(c.peer))
			if err != nil {
				return nil, nil, err
			}
			if forbid {
				v := e.emit(EvViolation, dialAt, req.ID, req.Host, []int{get.Seq}, map[string]string{
					"kind":                ViolationPoolReuse,
					"validated_addresses": joinAddrs(validated),
					"reused_peer":         c.peer,
					"conn_id":             fmt.Sprintf("%d", c.id),
				}, get.VC)
				v.Violation = ViolationPoolReuse
				e.events[len(e.events)-1] = v
				violations = append(violations, e.buildViolation(ViolationPoolReuse, req, validated, "", c.peer, c.id, dialAt, v))
			}
			continue
		}

		miss := e.emit(EvPoolMiss, dialAt, req.ID, req.Host, []int{check.Seq}, map[string]string{
			"pool_key": key,
		}, check.VC)

		dialAddrs, dialChain, err := e.resolveAt(req.ID, req.Host, dialAt)
		if err != nil {
			return nil, nil, err
		}
		dialVCs := []map[string]int{miss.VC}
		for _, ev := range dialChain {
			dialVCs = append(dialVCs, ev.VC)
		}
		dialIP := dialAddrs[0]
		dialTarget := fmt.Sprintf("%s:%d", dialIP, port)
		chainParents := []int{miss.Seq}
		for _, ev := range dialChain {
			chainParents = append(chainParents, ev.Seq)
		}
		dial := e.emit(EvDial, dialAt, req.ID, req.Host, chainParents, map[string]string{
			"dial_address":        dialTarget,
			"resolved_at_dial":    joinAddrs(dialAddrs),
			"validated_addresses": joinAddrs(validated),
			"port":                fmt.Sprintf("%d", port),
		}, dialVCs...)

		forbid, err := e.isForbidden(dialIP)
		if err != nil {
			return nil, nil, err
		}
		e.connCount++
		connection := &conn{id: e.connCount, peer: dialTarget, key: key, bornAt: dialAt}
		e.emit(EvPoolPut, dialAt, req.ID, req.Host, []int{dial.Seq}, map[string]string{
			"pool_key": key,
			"conn_id":  fmt.Sprintf("%d", connection.id),
			"peer":     dialTarget,
		}, dial.VC)
		e.pools[key] = append(e.pools[key], connection)

		if forbid {
			v := e.emit(EvViolation, dialAt, req.ID, req.Host, []int{dial.Seq}, map[string]string{
				"kind":                ViolationDial,
				"validated_addresses": joinAddrs(validated),
				"dial_address":        dialTarget,
			}, dial.VC)
			v.Violation = ViolationDial
			e.events[len(e.events)-1] = v
			violations = append(violations, e.buildViolation(ViolationDial, req, validated, dialTarget, "", connection.id, dialAt, v))
		}

	}

	report := &Report{
		ForbiddenCIDRs: append([]string(nil), e.scenario.Forbidden...),
		Events:         e.events,
		Violations:     violations,
	}
	return report, violations, nil
}

func (e *Engine) buildViolation(kind string, req RequestSpec, validated []string, dialTarget, reusedPeer string, connID int, actionAt int64, v Event) Violation {
	closure := e.causalClosure(v.Seq)
	sub := e.buildSubScenario(closure)
	violation := Violation{
		ID:              fmt.Sprintf("%s/%s", kind, req.ID),
		Kind:            kind,
		RequestID:       req.ID,
		Host:            req.Host,
		ValidatedAddrs:  append([]string(nil), validated...),
		DialAddress:     dialTarget,
		ReusedPeer:      reusedPeer,
		ReusedConnID:    connID,
		ValidateAt:      req.ValidateAt,
		ActionAt:        actionAt,
		EventSeq:        v.Seq,
		CausalEventSeqs: closure,
		SubScenario:     sub,
	}
	return violation
}

// causalClosure returns the transitive parent set of an event, in global
// (topological) sequence order.
func (e *Engine) causalClosure(seq int) []int {
	reachable := map[int]bool{seq: true}
	stack := []int{seq}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, parent := range e.events[cur-1].Parents {
			if !reachable[parent] {
				reachable[parent] = true
				stack = append(stack, parent)
			}
		}
	}
	out := make([]int, 0, len(reachable))
	for id := range reachable {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// buildSubScenario projects the closure into a standalone Scenario: every
// request with an event in the closure is retained in original order, and
// every host's resolution script is trimmed to the steps actually used.
func (e *Engine) buildSubScenario(closure []int) Scenario {
	inClosure := map[int]bool{}
	for _, seq := range closure {
		inClosure[seq] = true
	}
	reqIDs := map[string]bool{}
	hosts := map[string]bool{}
	maxStep := map[string]int{}
	for _, seq := range closure {
		ev := e.events[seq-1]
		if ev.RequestID != "" {
			reqIDs[ev.RequestID] = true
		}
		if ev.Host != "" {
			hosts[ev.Host] = true
		}
		if ev.Type == EvResolve {
			idx := atoiSafe(ev.Detail["step"])
			if idx+1 > maxStep[ev.Host] {
				maxStep[ev.Host] = idx + 1
			}
		}
	}

	sub := Scenario{
		Forbidden: append([]string(nil), e.scenario.Forbidden...),
		Hosts:     map[string]Host{},
		Requests:  []RequestSpec{},
	}
	for name := range hosts {
		cfg := e.scenario.Hosts[name]
		need := maxStep[name]
		if need == 0 {
			need = 1
		}
		steps := make([][]string, 0, need)
		for i := 0; i < need && i < len(cfg.Steps); i++ {
			steps = append(steps, append([]string(nil), cfg.Steps[i]...))
		}
		sub.Hosts[name] = Host{Port: cfg.Port, TTL: cfg.TTL, Steps: steps}
	}
	for _, req := range e.scenario.Requests {
		if reqIDs[req.ID] {
			sub.Requests = append(sub.Requests, req)
		}
	}
	return sub
}

func passOrBlock(blocked bool) string {
	if blocked {
		return "BLOCK"
	}
	return "PASS"
}

func atoiSafe(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
