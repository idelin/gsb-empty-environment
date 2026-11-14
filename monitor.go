package netsec

import (
	"errors"
	"sync"
	"time"
)

// ViolationKind categorizes a surveillance finding.
type ViolationKind int

const (
	// ViolationSelfTrade is a would-be trade between same-beneficial-owner
	// accounts; blocked at submission.
	ViolationSelfTrade ViolationKind = iota + 1
	// ViolationWashTrade is an executed trade between same-owner accounts.
	ViolationWashTrade
	// ViolationRateSubmission is an above-threshold order submission rate.
	ViolationRateSubmission
	// ViolationRateCancel is an above-threshold cancel rate.
	ViolationRateCancel
)

// String returns a stable textual name for the kind.
func (k ViolationKind) String() string {
	switch k {
	case ViolationSelfTrade:
		return "self_trade"
	case ViolationWashTrade:
		return "wash_trade"
	case ViolationRateSubmission:
		return "rate_submission"
	case ViolationRateCancel:
		return "rate_cancel"
	default:
		return "unknown"
	}
}

// Surveillance errors returned by OrderBook.Submit.
var (
	// ErrSelfTrade is returned when an order would cross an order from the
	// same beneficial owner.
	ErrSelfTrade = errors.New("netsec: self-trade prevented")
	// ErrRateLimited is returned when a frequency rule trips.
	ErrRateLimited = errors.New("netsec: order rate limit exceeded")
)

// Violation is one recorded surveillance event.
type Violation struct {
	Kind   ViolationKind
	Owner  string
	Ref    string
	At     time.Time
	Window time.Duration
	Count  int
	Limit  int
}

// FrequencyRule configures per-owner sliding-window thresholds. A zero limit
// disables that rule.
type FrequencyRule struct {
	// Window is the look-back length measured on the engine clock.
	Window time.Duration
	// MaxSubmissions rejects submissions once this many already sit inside
	// the window (the rejected request is not admitted).
	MaxSubmissions int
	// MaxCancels records a violation once cancels in the window exceed it.
	// Cancels are never blocked.
	MaxCancels int
}

type activityEvent struct {
	at     time.Time
	cancel bool
}

// Monitor performs self-trade prevention, wash trade detection and abnormal
// frequency detection. All timing comes from engine-supplied time values;
// client-declared timestamps are never consulted.
type Monitor struct {
	mu         sync.Mutex
	rule       FrequencyRule
	groups     map[string]string
	members    map[string]map[string]struct{}
	activity   map[string][]activityEvent
	violations []Violation
}

// MonitorOption configures a new Monitor.
type MonitorOption func(*Monitor)

// WithFrequencyRule sets the sliding-window frequency thresholds.
func WithFrequencyRule(rule FrequencyRule) MonitorOption {
	return func(m *Monitor) { m.rule = rule }
}

// NewMonitor builds a Monitor. With no rule, frequency detection is disabled.
func NewMonitor(opts ...MonitorOption) *Monitor {
	m := &Monitor{
		groups:   make(map[string]string),
		members:  make(map[string]map[string]struct{}),
		activity: make(map[string][]activityEvent),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// LinkAccounts marks all accounts as belonging to one beneficial owner, so
// trades between them are treated as self/wash trades. Repeated and chained
// calls merge groups transitively into one equivalence class.
func (m *Monitor) LinkAccounts(accounts ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(accounts) == 0 {
		return
	}

	roots := make(map[string]struct{})
	for _, account := range accounts {
		roots[m.canonical(account)] = struct{}{}
	}

	var root string
	for candidate := range roots {
		root = candidate
		break
	}

	merged, ok := m.members[root]
	if !ok {
		merged = make(map[string]struct{})
		m.members[root] = merged
	}
	for other := range roots {
		if other == root {
			continue
		}
		for account := range m.members[other] {
			merged[account] = struct{}{}
		}
		delete(m.members, other)
	}
	for _, account := range accounts {
		merged[account] = struct{}{}
	}
	for account := range merged {
		m.groups[account] = root
	}
}

// SameOwner reports whether two accounts are the same beneficial owner,
// including explicitly linked groups.
func (m *Monitor) SameOwner(a, b string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.canonical(a) == m.canonical(b)
}

// CheckSubmission enforces the submission frequency rule. It is called by the
// book on every Submit and is safe for concurrent use.
func (m *Monitor) CheckSubmission(owner string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.rule.Window > 0 && m.rule.MaxSubmissions > 0 {
		events := m.prune(owner, now)
		if len(events) >= m.rule.MaxSubmissions {
			m.violations = append(m.violations, Violation{
				Kind:   ViolationRateSubmission,
				Owner:  owner,
				At:     now,
				Window: m.rule.Window,
				Count:  len(events) + 1,
				Limit:  m.rule.MaxSubmissions,
			})
			return ErrRateLimited
		}
	}
	m.activity[owner] = append(m.activity[owner], activityEvent{at: now})
	return nil
}

// RecordCancel appends a cancel event and flags cancel-rate violations.
func (m *Monitor) RecordCancel(owner string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.activity[owner] = append(m.activity[owner], activityEvent{at: now, cancel: true})
	if m.rule.Window > 0 && m.rule.MaxCancels > 0 {
		events := m.prune(owner, now)
		cancels := 0
		for _, event := range events {
			if event.cancel {
				cancels++
			}
		}
		if cancels > m.rule.MaxCancels {
			m.violations = append(m.violations, Violation{
				Kind:   ViolationRateCancel,
				Owner:  owner,
				At:     now,
				Window: m.rule.Window,
				Count:  cancels,
				Limit:  m.rule.MaxCancels,
			})
		}
	}
}

// RecordTrade applies wash trade detection to an executed trade. It returns
// the recorded violation, or nil when taker and maker are independent owners.
func (m *Monitor) RecordTrade(takerOwner, takerOrderID, makerOwner, makerOrderID string, at time.Time) *Violation {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.canonical(takerOwner) != m.canonical(makerOwner) {
		return nil
	}
	violation := Violation{
		Kind:  ViolationWashTrade,
		Owner: takerOwner,
		Ref:   takerOrderID + "->" + makerOrderID,
		At:    at,
	}
	m.violations = append(m.violations, violation)
	return &m.violations[len(m.violations)-1]
}

// Violations returns a copy of every recorded violation in occurrence order.
func (m *Monitor) Violations() []Violation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Violation, len(m.violations))
	copy(out, m.violations)
	return out
}

// RecordViolation appends a generic surveillance finding.
func (m *Monitor) RecordViolation(kind ViolationKind, owner, ref string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.violations = append(m.violations, Violation{
		Kind:  kind,
		Owner: owner,
		Ref:   ref,
		At:    at,
	})
}

// prune drops events outside the window and returns the retained slice.
// Caller must hold m.mu.
func (m *Monitor) prune(owner string, now time.Time) []activityEvent {
	events := m.activity[owner]
	cutoff := now.Add(-m.rule.Window)
	kept := events[:0]
	for _, event := range events {
		if event.at.After(cutoff) {
			kept = append(kept, event)
		}
	}
	m.activity[owner] = kept
	return kept
}

// canonical resolves an account to its linked-group root. Caller must hold m.mu.
func (m *Monitor) canonical(account string) string {
	if root, ok := m.groups[account]; ok {
		return root
	}
	return account
}
