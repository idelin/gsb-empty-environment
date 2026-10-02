package risk

import (
	"errors"
	"fmt"
	"sync"
)

type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionDeny  Decision = "DENY"
)

// Event is a single streaming transaction.
type Event struct {
	TxID      string
	Account   string
	Amount    float64
	Timestamp int64
}

// Result is the risk decision for one event.
type Result struct {
	Decision    Decision
	RuleVersion string
	RuleName    string // triggering rule, empty when allowed
	Reason      string
}

var ErrSessionExists = errors.New("session already exists")

// session pins the rule set that was current when the session began, so a
// long-running transaction is judged by the old rules even after a hot swap.
type session struct {
	id      string
	account string
	rules   *RuleSet
}

// Engine is a streaming risk-control decision component with sliding-window
// frequency rules and smooth hot-swappable rule versions.
type Engine struct {
	mu       sync.Mutex
	clock    Clock
	current  *RuleSet
	history  map[string][]Event // per-account event log for window evaluation
	sessions map[string]*session
	audit    *AuditLog
}

func NewEngine(clock Clock, rules *RuleSet, audit *AuditLog) *Engine {
	return &Engine{
		clock:    clock,
		current:  rules,
		history:  make(map[string][]Event),
		sessions: make(map[string]*session),
		audit:    audit,
	}
}

// UpdateRules atomically hot-swaps the active rule set. Existing sessions keep
// their pinned (old) version; only new sessions and session-less requests
// pick up the new version.
func (e *Engine) UpdateRules(rules *RuleSet) {
	e.audit.Append(Entry{
		Timestamp:   e.clock.Now(),
		TxID:        "-",
		SessionID:   "-",
		Account:     "-",
		Decision:    DecisionAllow,
		RuleVersion: rules.Version,
		Reason:      fmt.Sprintf("rule set hot-swapped to version %s", rules.Version),
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current = rules
}

// BeginSession starts a long transaction and pins the current rule version
// to it. All events evaluated under this session use that pinned version.
func (e *Engine) BeginSession(id, account string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.sessions[id]; ok {
		return ErrSessionExists
	}
	e.sessions[id] = &session{id: id, account: account, rules: e.current}
	return nil
}

// EndSession closes a session; later events with its id fall back to the
// current rule version.
func (e *Engine) EndSession(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sessions, id)
}

// Evaluate decides on one event. If sessionID names an active session, the
// session's pinned rule version is used; otherwise the current version is.
// Every event is appended to the account history (attempts count toward the
// frequency window) and recorded in the audit log with its rule version.
func (e *Engine) Evaluate(sessionID string, ev Event) Result {
	e.mu.Lock()
	rules := e.current
	if sessionID != "" {
		if s, ok := e.sessions[sessionID]; ok {
			rules = s.rules
		}
	}
	e.history[ev.Account] = append(e.history[ev.Account], ev)
	triggered, reason := rules.evaluate(e.history[ev.Account], ev)
	e.mu.Unlock()

	res := Result{Decision: DecisionAllow, RuleVersion: rules.Version}
	if triggered != nil {
		res.Decision = DecisionDeny
		res.RuleName = triggered.Name
		res.Reason = reason
	}
	e.audit.Append(Entry{
		Timestamp:   evTimestamp(ev, e.clock),
		TxID:        ev.TxID,
		SessionID:   dashIfEmpty(sessionID),
		Account:     ev.Account,
		Amount:      ev.Amount,
		Decision:    res.Decision,
		RuleVersion: res.RuleVersion,
		Reason:      res.Reason,
	})
	return res
}

func evTimestamp(ev Event, c Clock) int64 {
	if ev.Timestamp != 0 {
		return ev.Timestamp
	}
	return c.Now()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
