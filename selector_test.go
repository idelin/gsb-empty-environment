package netsec

import "testing"

func TestLabelSelectorMatches(t *testing.T) {
	tests := []struct {
		name     string
		selector LabelSelector
		labels   map[string]string
		want     bool
	}{
		{
			name:     "empty selector matches everything",
			selector: LabelSelector{},
			labels:   nil,
			want:     true,
		},
		{
			name: "match labels are ANDed",
			selector: LabelSelector{MatchLabels: map[string]string{
				"app":  "web",
				"role": "client",
			}},
			labels: map[string]string{"app": "web", "role": "worker"},
			want:   false,
		},
		{
			name: "In expression",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key:      "role",
				Operator: LabelSelectorOpIn,
				Values:   []string{"client", "worker"},
			}}},
			labels: map[string]string{"role": "client"},
			want:   true,
		},
		{
			name: "NotIn expression matches a missing key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key:      "role",
				Operator: LabelSelectorOpNotIn,
				Values:   []string{"client"},
			}}},
			labels: map[string]string{},
			want:   true,
		},
		{
			name: "Exists expression",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key:      "role",
				Operator: LabelSelectorOpExists,
			}}},
			labels: map[string]string{"role": "client"},
			want:   true,
		},
		{
			name: "DoesNotExist expression",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key:      "role",
				Operator: LabelSelectorOpDoesNotExist,
			}}},
			labels: map[string]string{"role": "client"},
			want:   false,
		},
		{
			name: "unknown operator does not match",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{{
				Key:      "role",
				Operator: LabelSelectorOperator("Unknown"),
				Values:   []string{"client"},
			}}},
			labels: map[string]string{"role": "client"},
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.selector.Matches(test.labels); got != test.want {
				t.Fatalf("Matches() = %v, want %v", got, test.want)
			}
		})
	}
}
