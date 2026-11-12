package netsec

import (
	"sort"
	"strconv"
)

// MetadataNameLabel is the immutable label that Kubernetes adds to every
// Namespace. All other namespace labels are caller-supplied and must be
// treated as tenant-modifiable by this package.
const MetadataNameLabel = "kubernetes.io/metadata.name"

// Labels is a set of Kubernetes label key/value pairs.
type Labels map[string]string

// LabelSelectorOperator is the operator in a LabelSelectorRequirement.
type LabelSelectorOperator string

const (
	LabelSelectorOpIn           LabelSelectorOperator = "In"
	LabelSelectorOpNotIn        LabelSelectorOperator = "NotIn"
	LabelSelectorOpExists       LabelSelectorOperator = "Exists"
	LabelSelectorOpDoesNotExist LabelSelectorOperator = "DoesNotExist"
)

// LabelSelectorRequirement is the equivalent of metav1.LabelSelectorRequirement.
type LabelSelectorRequirement struct {
	Key      string                `json:"key"`
	Operator LabelSelectorOperator `json:"operator"`
	Values   []string              `json:"values,omitempty"`
}

// LabelSelector is the equivalent of metav1.LabelSelector. A zero value, or a
// selector with no matchLabels and no matchExpressions, matches every object.
type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// Matches reports whether labels satisfy all matchLabels and all
// matchExpressions. Empty selectors match all label sets.
func (s LabelSelector) Matches(labels map[string]string) bool {
	return s.matches(labels)
}

func (s LabelSelector) matches(labels map[string]string) bool {
	for key, value := range s.MatchLabels {
		if labels[key] != value {
			return false
		}
	}
	for _, requirement := range s.MatchExpressions {
		if !requirement.matches(labels) {
			return false
		}
	}
	return true
}

func (r LabelSelectorRequirement) matches(labels map[string]string) bool {
	value, exists := labels[r.Key]
	switch r.Operator {
	case LabelSelectorOpIn:
		return exists && contains(r.Values, value)
	case LabelSelectorOpNotIn:
		// As with Kubernetes label selectors, a missing key satisfies NotIn.
		return !exists || !contains(r.Values, value)
	case LabelSelectorOpExists:
		return exists
	case LabelSelectorOpDoesNotExist:
		return !exists
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Namespace is a namespace identity plus labels. Name is authoritative and
// takes precedence over a caller-supplied kubernetes.io/metadata.name label.
type Namespace struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Pod is a workload identity and its namespace.
type Pod struct {
	Name      string            `json:"name,omitempty"`
	Namespace Namespace         `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// NamespaceName returns the structural namespace name, falling back to the
// metadata.name label when Name was not populated by the caller.
func NamespaceName(namespace Namespace) string {
	if namespace.Name != "" {
		return namespace.Name
	}
	return namespace.Labels[MetadataNameLabel]
}

// NamespaceLabels returns an effective label set for a namespace. Kubernetes
// always exposes the namespace name through MetadataNameLabel, so the label is
// synthesized from Namespace.Name when needed and cannot be spoofed in Labels.
func NamespaceLabels(namespace Namespace) Labels {
	labels := make(Labels, len(namespace.Labels)+1)
	for key, value := range namespace.Labels {
		labels[key] = value
	}
	if name := NamespaceName(namespace); name != "" {
		labels[MetadataNameLabel] = name
	}
	return labels
}

func (s LabelSelector) mutableNamespaceLabels(namespace string) []MutableNamespaceLabel {
	var dependencies []MutableNamespaceLabel

	keys := make([]string, 0, len(s.MatchLabels))
	for key := range s.MatchLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == MetadataNameLabel {
			continue
		}
		dependencies = append(dependencies, MutableNamespaceLabel{
			Namespace: namespace,
			Key:       key,
			Operator:  LabelSelectorOpIn,
			Values:    []string{s.MatchLabels[key]},
		})
	}

	for _, requirement := range s.MatchExpressions {
		if requirement.Key == MetadataNameLabel {
			continue
		}
		values := append([]string(nil), requirement.Values...)
		dependencies = append(dependencies, MutableNamespaceLabel{
			Namespace: namespace,
			Key:       requirement.Key,
			Operator:  requirement.Operator,
			Values:    values,
		})
	}

	return dependencies
}

// String makes selector requirements readable in diagnostics and tests.
func (r LabelSelectorRequirement) String() string {
	switch r.Operator {
	case LabelSelectorOpExists, LabelSelectorOpDoesNotExist:
		return r.Key + " " + string(r.Operator)
	case LabelSelectorOpIn, LabelSelectorOpNotIn:
		return r.Key + " " + string(r.Operator) + " [" + joinValues(r.Values) + "]"
	default:
		return r.Key + " " + string(r.Operator) + " [" + joinValues(r.Values) + "]"
	}
}

func joinValues(values []string) string {
	result := ""
	for index, value := range values {
		if index != 0 {
			result += ","
		}
		result += strconv.Quote(value)
	}
	return result
}
