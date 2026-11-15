package netsec

// Matches 判断标签集合是否满足 selector，语义对齐 metav1.LabelSelector。
func (s *LabelSelector) Matches(labels map[string]string) bool {
	for key, value := range s.MatchLabels {
		if labels[key] != value {
			return false
		}
	}
	for _, requirement := range s.MatchExpressions {
		value, exists := labels[requirement.Key]
		switch requirement.Operator {
		case "In":
			if !exists || !contains(requirement.Values, value) {
				return false
			}
		case "NotIn":
			if exists && contains(requirement.Values, value) {
				return false
			}
		case "Exists":
			if !exists {
				return false
			}
		case "DoesNotExist":
			if exists {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
