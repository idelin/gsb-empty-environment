package admission

import (
	"errors"
	"fmt"
	"strings"
)

// MaxDecisionPoints is the hard cap on public decision variables. With at
// most 14 binary points the complete enumeration is at most 2^14 = 16384
// deterministic admission rules.
const MaxDecisionPoints = 14

// MaxRetriesAllowed is the protocol cap on retries.
const MaxRetriesAllowed = 2

// decisionKey identifies indistinguishable decision nodes.
type decisionKey struct {
	subject Identity
	obs     string
}

// conditionVar is one public decision variable. Several DAG nodes share one
// variable when the same subject sees the same public prefix, or when several
// subjects belong to one failure domain at the same observation.
type conditionVar struct {
	id        string
	domain    string
	subjects  []Identity
	obs       []MessageType
	actions   []string
	abstain   string
	key       decisionKey
	hostFixed bool
	hostRule  string
	hostAct   string
}

// Analyzer is a compiled model ready for exhaustive evaluation.
type Analyzer struct {
	model *Model
	nodes map[string]*Node
	order []string
	idx   map[string]int
	vars  []*conditionVar
	// nodeVar maps each decision node ID to its condition variable index.
	nodeVar map[string]int
}

// Condition describes one public decision variable back to the host.
type Condition struct {
	// ID is the stable identifier of the variable within this model.
	ID string
	// Domain is empty for an independent subject.
	Domain string
	// Subjects are the subjects governed by this variable.
	Subjects []Identity
	// ObservedMessages is the public prefix at which the choice is made.
	ObservedMessages []MessageType
	// Actions are the admissible actions.
	Actions []string
	// AbstainAction is the retry-eligible action.
	AbstainAction string
	// HostFixed reports whether the host already fixed this variable through
	// Model.HostConditions because it belongs to a failure domain.
	HostFixed bool
	// HostAction carries the host-mandated action when HostFixed is true.
	HostAction string
}

// Conditions returns the public decision variables in canonical order.
func (a *Analyzer) Conditions() []Condition {
	out := make([]Condition, len(a.vars))
	for i, v := range a.vars {
		out[i] = Condition{
			ID:               v.id,
			Domain:           v.domain,
			Subjects:         append([]Identity(nil), v.subjects...),
			ObservedMessages: append([]MessageType(nil), v.obs...),
			Actions:          append([]string(nil), v.actions...),
			AbstainAction:    v.abstain,
			HostFixed:        v.hostFixed,
			HostAction:       v.hostAct,
		}
	}
	return out
}

// Compile validates a model and builds the analyzer used for enumeration.
func (m *Model) Compile() (*Analyzer, error) {
	if m == nil {
		return nil, errors.New("admission: nil model")
	}
	a := &Analyzer{
		model:   m,
		nodes:   make(map[string]*Node),
		idx:     make(map[string]int),
		nodeVar: make(map[string]int),
	}
	if err := m.validateTablesAndIdeal(); err != nil {
		return nil, err
	}
	if err := m.validateParticipants(); err != nil {
		return nil, err
	}
	if m.MaxRetries < 0 || m.MaxRetries > MaxRetriesAllowed {
		return nil, fmt.Errorf("admission: MaxRetries must be in [0,%d], got %d", MaxRetriesAllowed, m.MaxRetries)
	}
	if err := a.indexAndTopo(); err != nil {
		return nil, err
	}
	if err := a.validateNodes(); err != nil {
		return nil, err
	}
	if err := a.buildVariables(); err != nil {
		return nil, err
	}
	if err := a.validateRetries(); err != nil {
		return nil, err
	}
	return a, nil
}

func observationKey(obs []MessageType) string {
	parts := make([]string, len(obs))
	for i, msg := range obs {
		parts[i] = string(msg)
	}
	return strings.Join(parts, "\x00")
}
