package changeaccept

import "errors"

// ErrBudgetExhausted means that a definite yes/no answer was not reached. The
// caller must treat the corresponding outcome as Undetermined, never as a
// rejection or an approval.
var ErrBudgetExhausted = errors.New("verification budget exhausted")

const (
	defaultMaxStates      = 100_000
	defaultMaxTransitions = 1_000_000
	defaultMaxSubsets     = 100_000
)

// Budget bounds exhaustive exploration and switch-order search. A zero field
// selects its default. Limits are per verification operation, except
// MaxSubsets, which bounds the subset dynamic program used for reordering.
type Budget struct {
	MaxStates      int
	MaxTransitions int
	MaxSubsets     int
}

func (b Budget) normalized() Budget {
	if b.MaxStates <= 0 {
		b.MaxStates = defaultMaxStates
	}
	if b.MaxTransitions <= 0 {
		b.MaxTransitions = defaultMaxTransitions
	}
	if b.MaxSubsets <= 0 {
		b.MaxSubsets = defaultMaxSubsets
	}
	return b
}

type meter struct {
	budget      Budget
	states      int
	transitions int
	subsets     int
}

func newMeter(budget Budget) *meter {
	return &meter{budget: budget.normalized()}
}

func (m *meter) addState() error {
	m.states++
	if m.states > m.budget.MaxStates {
		return ErrBudgetExhausted
	}
	return nil
}

func (m *meter) addTransition() error {
	m.transitions++
	if m.transitions > m.budget.MaxTransitions {
		return ErrBudgetExhausted
	}
	return nil
}

func (m *meter) addSubset() error {
	m.subsets++
	if m.subsets > m.budget.MaxSubsets {
		return ErrBudgetExhausted
	}
	return nil
}
