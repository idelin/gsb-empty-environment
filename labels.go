package netsec

import (
	"sort"
	"strings"
)

// Label identifies one secret, for example a secret input or a value that was
// influenced by that input. Labels are compared by their string value.
type Label string

// LabelSet is a canonical set of secret labels. Constructors and all
// operations return sorted, duplicate-free values. The zero value is the
// empty set. Treat a LabelSet as immutable and use the helpers below to
// derive a changed set.
type LabelSet []Label

// Labels returns a canonical label set.
func Labels(labels ...Label) LabelSet {
	return canonicalLabels(labels)
}

// NewLabelSet is an alias for Labels that accepts plain strings.
func NewLabelSet(labels ...string) LabelSet {
	set := make(LabelSet, 0, len(labels))
	for _, label := range labels {
		set = append(set, Label(label))
	}
	return canonicalLabels(set)
}

func canonicalLabels(labels []Label) LabelSet {
	if len(labels) == 0 {
		return nil
	}
	seen := make(map[Label]struct{}, len(labels))
	for _, label := range labels {
		if label != "" {
			seen[label] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	set := make(LabelSet, 0, len(seen))
	for label := range seen {
		set = append(set, label)
	}
	sort.Slice(set, func(i, j int) bool {
		return set[i] < set[j]
	})
	return set
}

// Clone returns an independent copy.
func (s LabelSet) Clone() LabelSet {
	if len(s) == 0 {
		return nil
	}
	return append(LabelSet(nil), s...)
}

// Empty reports whether the set contains no labels.
func (s LabelSet) Empty() bool {
	return len(s) == 0
}

// Contains reports whether label is present.
func (s LabelSet) Contains(label Label) bool {
	for _, candidate := range s {
		if candidate == label {
			return true
		}
	}
	return false
}

// Union returns the union of all supplied sets.
func (s LabelSet) Union(others ...LabelSet) LabelSet {
	all := make([]Label, 0, len(s))
	all = append(all, s...)
	for _, other := range others {
		all = append(all, other...)
	}
	return canonicalLabels(all)
}

// Intersect returns the labels present in both sets.
func (s LabelSet) Intersect(other LabelSet) LabelSet {
	if len(s) == 0 || len(other) == 0 {
		return nil
	}
	result := make(LabelSet, 0, len(s))
	for _, label := range s {
		if other.Contains(label) {
			result = append(result, label)
		}
	}
	return canonicalLabels(result)
}

// Minus returns the labels in s that are not present in other.
func (s LabelSet) Minus(other LabelSet) LabelSet {
	if len(s) == 0 {
		return nil
	}
	result := make(LabelSet, 0, len(s))
	for _, label := range s {
		if !other.Contains(label) {
			result = append(result, label)
		}
	}
	return canonicalLabels(result)
}

// Equal reports set equality, regardless of input ordering.
func (s LabelSet) Equal(other LabelSet) bool {
	left := canonicalLabels(s)
	right := canonicalLabels(other)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// Sorted returns a copy in canonical order.
func (s LabelSet) Sorted() []Label {
	return canonicalLabels(s)
}

// String renders the labels in a stable, human-readable form.
func (s LabelSet) String() string {
	if len(s) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(s))
	for _, label := range s {
		parts = append(parts, string(label))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
