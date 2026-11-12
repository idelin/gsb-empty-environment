package netsec

import "strings"

// ShadowedDeny describes a deny rule whose effect is completely overridden by
// later allow rules.
//
// Index is the zero-based position of the deny in the policy. CoveredBy lists
// later allow rules that individually cover the deny's operation and path
// domains. The last entry is the rule that ultimately shadows the deny.
type ShadowedDeny struct {
	Index     int
	Rule      Rule
	CoveredBy []int
}

// FindShadowedDenies returns a report for every deny that is fully shadowed by
// a later allow rule.
//
// A deny is fully shadowed when a later allow rule matches every operation and
// path that the deny matches. The earlier deny can then never be the last
// matching rule, so removing it would not change any decision. Partial
// coverage is deliberately not reported: a partially covered deny can still
// reject paths and remains security relevant.
func FindShadowedDenies(rules []Rule) []ShadowedDeny {
	var reports []ShadowedDeny
	for i, deny := range rules {
		if deny.Action != Deny || !validRule(deny) {
			continue
		}

		coverers := make([]int, 0)
		latestCoverer := -1
		for j := i + 1; j < len(rules); j++ {
			allow := rules[j]
			if allow.Action != Allow || !validRule(allow) {
				continue
			}
			if ruleCovers(allow, deny) {
				coverers = append(coverers, j)
				latestCoverer = j
			}
		}
		if latestCoverer < 0 {
			continue
		}

		reports = append(reports, ShadowedDeny{
			Index:     i,
			Rule:      deny,
			CoveredBy: append([]int(nil), coverers...),
		})
	}
	return reports
}

// ShadowedDenyReports is an alias for FindShadowedDenies.
func ShadowedDenyReports(rules []Rule) []ShadowedDeny {
	return FindShadowedDenies(rules)
}

// ShadowedDenyIndices returns the indexes of deny rules that are fully
// shadowed by later allow rules.
func ShadowedDenyIndices(rules []Rule) []int {
	reports := FindShadowedDenies(rules)
	if len(reports) == 0 {
		return nil
	}
	indices := make([]int, len(reports))
	for i, report := range reports {
		indices[i] = report.Index
	}
	return indices
}

// ShadowedDenies is an alias for ShadowedDenyIndices.
func ShadowedDenies(rules []Rule) []int {
	return ShadowedDenyIndices(rules)
}

// ShadowedDenyRules returns the deny rules that are fully shadowed by later
// allow rules, in policy order.
func ShadowedDenyRules(rules []Rule) []Rule {
	reports := FindShadowedDenies(rules)
	if len(reports) == 0 {
		return nil
	}
	rulesOut := make([]Rule, len(reports))
	for i, report := range reports {
		rulesOut[i] = report.Rule
	}
	return rulesOut
}

// FindShadowedDenies returns detailed shadowing reports for the policy.
func (p *Policy) FindShadowedDenies() []ShadowedDeny {
	if p == nil {
		return nil
	}
	return FindShadowedDenies(p.Rules)
}

// ShadowedDenyReports returns detailed shadowing reports for the policy.
func (p *Policy) ShadowedDenyReports() []ShadowedDeny {
	return p.FindShadowedDenies()
}

// ShadowedDenyIndices returns the indexes of fully shadowed deny rules.
func (p *Policy) ShadowedDenyIndices() []int {
	if p == nil {
		return nil
	}
	return ShadowedDenyIndices(p.Rules)
}

// ShadowedDenies returns the indexes of fully shadowed deny rules.
func (p *Policy) ShadowedDenies() []int {
	return p.ShadowedDenyIndices()
}

// ShadowedDenyRules returns the fully shadowed deny rules in policy order.
func (p *Policy) ShadowedDenyRules() []Rule {
	if p == nil {
		return nil
	}
	return ShadowedDenyRules(p.Rules)
}

func validRule(rule Rule) bool {
	return rule.Validate() == nil
}

// ruleCovers reports whether every operation and path selected by covered is
// also selected by covering. Action is not considered here.
func ruleCovers(covering, covered Rule) bool {
	return operationCovers(covering.Operation, covered.Operation) &&
		filterCovers(covering.Filter, covered.Filter)
}

func operationCovers(covering, covered string) bool {
	coveringStar := strings.HasSuffix(covering, "*")
	coveredStar := strings.HasSuffix(covered, "*")
	coveringPrefix := strings.TrimSuffix(covering, "*")
	coveredPrefix := strings.TrimSuffix(covered, "*")

	switch {
	case coveringStar && coveredStar:
		return strings.HasPrefix(coveredPrefix, coveringPrefix)
	case coveringStar:
		return strings.HasPrefix(covered, coveringPrefix)
	case coveredStar:
		return false
	default:
		return covering == covered
	}
}

func filterCovers(covering, covered Filter) bool {
	switch covered.Kind {
	case FilterLiteral:
		return covering.Match(covered.Value)

	case FilterSubpath:
		switch covering.Kind {
		case FilterLiteral:
			return false
		case FilterSubpath:
			return subpathContains(covering.Value, covered.Value)
		case FilterRegex:
			shape := describeRegex(covering.Value)
			if !shape.valid {
				return false
			}
			if shape.universal {
				return true
			}
			if !shape.anySuffix {
				return false
			}
			// The expression accepts every string with this literal prefix.
			if !strings.HasPrefix(covered.Value, shape.literal) {
				return false
			}
			// "prefix.+" does not match the bare prefix itself.
			return !shape.requiresMore || len(covered.Value) > len(shape.literal)
		default:
			return false
		}

	case FilterRegex:
		switch covering.Kind {
		case FilterLiteral:
			return false
		case FilterSubpath:
			target := describeRegex(covered.Value)
			if !target.valid {
				return false
			}
			if target.isLiteral {
				return subpathContains(covering.Value, target.literal)
			}
			if !target.anySuffix {
				return false
			}
			root := cleanSubpathRoot(covering.Value)
			if root == "/" {
				return strings.HasPrefix(target.literal, "/")
			}
			// Every string starting at target.literal must stay below root.
			// It is not enough for target.literal to equal root: an arbitrary
			// suffix would produce "/root-other", which is outside the
			// subtree.
			return strings.HasPrefix(target.literal, root+"/")
		case FilterRegex:
			return regexCovers(covering.Value, covered.Value)
		default:
			return false
		}
	default:
		return false
	}
}

func regexCovers(covering, covered string) bool {
	if covering == covered {
		return true
	}
	coverShape := describeRegex(covering)
	targetShape := describeRegex(covered)
	if !coverShape.valid || !targetShape.valid {
		return false
	}
	if coverShape.universal {
		// An expression accepting every non-empty path covers every practical
		// sandbox path. Literal targets are checked exactly below.
		return !targetShape.isLiteral || regexPathMatches(covering, targetShape.literal)
	}
	if targetShape.isLiteral {
		return regexPathMatches(covering, targetShape.literal)
	}

	// Compare "all strings with prefix P" languages. The expression covers the
	// target only when its literal prefix is a prefix of the target prefix.
	if coverShape.anySuffix && targetShape.anySuffix {
		if !strings.HasPrefix(targetShape.literal, coverShape.literal) {
			return false
		}
		if !coverShape.requiresMore {
			return true
		}
		if len(targetShape.literal) > len(coverShape.literal) {
			return true
		}
		return len(targetShape.literal) == len(coverShape.literal) && targetShape.requiresMore
	}
	return false
}

func regexPathMatches(expression, path string) bool {
	re, err := compileWholePathRegexp(expression)
	return err == nil && re.MatchString(path)
}
