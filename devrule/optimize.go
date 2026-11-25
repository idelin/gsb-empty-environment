package devrule

import "fmt"

// OptimizerFunc is a pending rule-list optimizer under review.
type OptimizerFunc func([]Rule) []Rule

// CheckOptimizer compiles the rule list before and after optimization and
// compares the two symbolic region decision diagrams. It returns the first
// semantic difference, or nil when the optimizer preserves semantics.
func CheckOptimizer(rules []Rule, opt OptimizerFunc) (*SemDiff, error) {
	before, err := Compile(rules)
	if err != nil {
		return nil, fmt.Errorf("original rules: %w", err)
	}
	after, err := Compile(opt(rules))
	if err != nil {
		return nil, fmt.Errorf("optimized rules: %w", err)
	}
	return Compare(before, after), nil
}
