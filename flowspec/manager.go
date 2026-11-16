package flowspec

import (
	"errors"
	"sync"
)

// Manager validates learned FlowSpec rules against a unicast RIB and keeps
// the set of installed rules. It is safe for concurrent use.
type Manager struct {
	mu        sync.RWMutex
	rib       RIB
	alerter   Alerter
	installed map[string]Rule
}

// NewManager creates a Manager. A nil alerter means rejected rules are dropped
// silently (the Receive error still reports the failure).
func NewManager(rib RIB, alerter Alerter) *Manager {
	if alerter == nil {
		alerter = noopAlerter{}
	}
	return &Manager{
		rib:       rib,
		alerter:   alerter,
		installed: make(map[string]Rule),
	}
}

// Receive validates an advertised rule. On success the rule is installed and
// installed=true is returned. On failure nothing is installed, an alert is
// emitted, and the *ValidationError is returned.
func (m *Manager) Receive(rule Rule) (installed bool, err error) {
	validationErr := Validate(rule, m.rib)
	if validationErr != nil {
		var vErr *ValidationError
		if errors.As(validationErr, &vErr) {
			m.alerter.FlowSpecAlert(Alert{
				Code:       vErr.Code,
				Rule:       rule,
				Prefix:     vErr.Prefix,
				Reason:     vErr.Detail,
				NeighborAS: rule.NeighborAS,
			})
		}
		return false, validationErr
	}

	m.mu.Lock()
	m.installed[rule.Key] = rule
	m.mu.Unlock()
	return true, nil
}

// Withdraw removes an installed rule. It reports whether a matching rule was
// installed.
func (m *Manager) Withdraw(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.installed[key]; !ok {
		return false
	}
	delete(m.installed, key)
	return true
}

// Installed returns a snapshot copy of the currently installed rules.
func (m *Manager) Installed() []Rule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rules := make([]Rule, 0, len(m.installed))
	for _, rule := range m.installed {
		rules = append(rules, rule)
	}
	return rules
}

// IsInstalled reports whether a rule with the given key is installed.
func (m *Manager) IsInstalled(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.installed[key]
	return ok
}
