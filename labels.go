package delegation

import "slices"

func validTagSets(labels []string, rules LabelConstraints) [][]string {
	var out [][]string
	maskCount := 1 << len(labels)
	for mask := 0; mask < maskCount; mask++ {
		tags := tagsFromMask(labels, mask)
		if validTagSet(tags, rules) {
			out = append(out, tags)
		}
	}
	slices.SortFunc(out, compareStringSlices)
	return out
}

func tagsFromMask(labels []string, mask int) []string {
	var tags []string
	for i, label := range labels {
		if mask&(1<<i) != 0 {
			tags = append(tags, label)
		}
	}
	return tags
}

func validTagSet(tags []string, rules LabelConstraints) bool {
	for _, required := range rules.Required {
		if !slices.Contains(tags, required) {
			return false
		}
	}
	for _, pair := range rules.MutexPairs {
		if slices.Contains(tags, pair[0]) && slices.Contains(tags, pair[1]) {
			return false
		}
	}
	for _, implication := range rules.Implications {
		if slices.Contains(tags, implication.IfLabel) && !slices.Contains(tags, implication.ThenLabel) {
			return false
		}
	}
	return true
}

func predicateMatches(predicate TagPredicate, tags []string) bool {
	for _, required := range predicate.RequireAll {
		if !slices.Contains(tags, required) {
			return false
		}
	}
	for _, forbidden := range predicate.ForbidAll {
		if slices.Contains(tags, forbidden) {
			return false
		}
	}
	return true
}

func compareStringSlices(a, b []string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
