package risk

import "fmt"

// Rule denies a transaction when BOTH conditions hold:
//   - the number of events for the same account inside the sliding window
//     (Window seconds, counting the current event) exceeds MaxCount, and
//   - the current transaction amount exceeds MaxAmount.
//
// Example: Rule{Window: 600, MaxCount: 3, MaxAmount: 10000} implements
// "10 分钟内转账超 3 次且金额超 1 万 -> 拦截".
type Rule struct {
	Name      string
	Window    int64   // sliding window length in seconds
	MaxCount  int     // frequency threshold (deny when count > MaxCount)
	MaxAmount float64 // single-transaction amount threshold (deny when amount > MaxAmount)
}

// RuleSet is an immutable, versioned bundle of rules. Hot-swapping replaces
// the whole set atomically; sessions started earlier keep their pinned set.
type RuleSet struct {
	Version string
	Rules   []Rule
}

// match reports whether the rule fires for ev given the account history
// (which already includes ev).
func (r Rule) match(history []Event, ev Event) (bool, string) {
	count := 0
	for _, h := range history {
		if h.Timestamp > ev.Timestamp-r.Window && h.Timestamp <= ev.Timestamp {
			count++
		}
	}
	if count > r.MaxCount && ev.Amount > r.MaxAmount {
		return true, fmt.Sprintf(
			"rule %q triggered: %d transfers in last %ds (> %d) and amount %.2f > %.2f",
			r.Name, count, r.Window, r.MaxCount, ev.Amount, r.MaxAmount)
	}
	return false, ""
}

// evaluate checks the event against every rule in the set.
func (rs *RuleSet) evaluate(history []Event, ev Event) (triggered *Rule, reason string) {
	for i := range rs.Rules {
		if ok, why := rs.Rules[i].match(history, ev); ok {
			return &rs.Rules[i], why
		}
	}
	return nil, ""
}
