package rpz

import "strings"

// SplitLabels normalizes a (possibly fully qualified) domain name into its
// lower-case, un-wire-encoded labels. The root name and empty strings yield a
// nil slice. A leading wildcard label ("*") is preserved so callers can tell
// wildcard owners apart from exact owners.
func SplitLabels(name string) []string {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return nil
	}
	raw := strings.Split(name, ".")
	labels := make([]string, 0, len(raw))
	for _, label := range raw {
		if label == "" {
			continue
		}
		labels = append(labels, strings.ToLower(label))
	}
	return labels
}

// JoinLabels renders labels back into a fully qualified domain name.
func JoinLabels(labels []string) string {
	if len(labels) == 0 {
		return "."
	}
	return strings.Join(labels, ".") + "."
}

// IsWildcard reports whether labels begin with the wildcard label.
func IsWildcard(labels []string) bool {
	return len(labels) > 0 && labels[0] == "*"
}

// IsSubDomain reports whether sub is strictly below parent: the parent name
// itself does not count. Both inputs are split with SplitLabels semantics.
func IsSubDomain(sub, parent []string) bool {
	if len(sub) <= len(parent) || len(parent) == 0 {
		return false
	}
	off := len(sub) - len(parent)
	for i, label := range parent {
		if sub[off+i] != label {
			return false
		}
	}
	return true
}

// dottedCount returns the number of dots in a domain name, i.e. the number of
// name labels minus one for every non-root name (BIND's ndots convention).
func dottedCount(labels []string) int {
	if len(labels) <= 1 {
		return 0
	}
	return len(labels) - 1
}
