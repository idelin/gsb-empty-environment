package session

import "sync"

// Policy answers authorization questions for new connections. Implementations
// must be safe for concurrent use.
type Policy interface {
	Decide(flow FlowID, dir Direction) Decision
}

type flowDirection struct {
	flow FlowID
	dir  Direction
}

// RuleTable is an explicit, direction-aware Policy implementation. Exact
// flow/direction pairs are looked up in the table; everything else falls back
// to the configured default decision.
type RuleTable struct {
	mu       sync.RWMutex
	rules    map[flowDirection]Decision
	fallback Decision
}

// NewRuleTable builds a rule table whose fallback decision applies to every
// flow that has no explicit entry.
func NewRuleTable(fallback Decision) *RuleTable {
	return &RuleTable{
		rules:    make(map[flowDirection]Decision),
		fallback: fallback,
	}
}

// Set installs an explicit decision for a flow/direction pair.
func (r *RuleTable) Set(flow FlowID, dir Direction, decision Decision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[flowDirection{flow: flow, dir: dir}] = decision
}

// Allow installs an explicit allow entry.
func (r *RuleTable) Allow(flow FlowID, dir Direction) {
	r.Set(flow, dir, DecisionAllow)
}

// Deny installs an explicit deny entry.
func (r *RuleTable) Deny(flow FlowID, dir Direction) {
	r.Set(flow, dir, DecisionDeny)
}

// Decide implements Policy.
func (r *RuleTable) Decide(flow FlowID, dir Direction) Decision {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if decision, ok := r.rules[flowDirection{flow: flow, dir: dir}]; ok {
		return decision
	}
	return r.fallback
}
