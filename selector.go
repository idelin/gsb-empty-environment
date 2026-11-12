package netsec

// Matches reports whether labels satisfy the selector. A nil selector is
// handled by ParamRef and is not passed to this method.
func (s *LabelSelector) Matches(labels map[string]string) bool {
	if s == nil {
		return false
	}
	for key, value := range s.MatchLabels {
		if labels[key] != value {
			return false
		}
	}
	for _, requirement := range s.MatchExpressions {
		if !requirement.Matches(labels) {
			return false
		}
	}
	return true
}

func (r LabelSelectorRequirement) Matches(labels map[string]string) bool {
	value, exists := labels[r.Key]
	switch r.Operator {
	case LabelSelectorIn:
		return exists && contains(r.Values, value)
	case LabelSelectorNotIn:
		return !exists || !contains(r.Values, value)
	case LabelSelectorExists:
		return exists
	case LabelSelectorDoesNotExist:
		return !exists
	default:
		return false
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
