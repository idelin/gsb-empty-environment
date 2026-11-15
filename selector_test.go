package netsec

import "testing"

func TestLabelSelectorMatches(t *testing.T) {
	cases := []struct {
		name     string
		selector LabelSelector
		labels   map[string]string
		want     bool
	}{
		{
			name:     "matchLabels 命中",
			selector: LabelSelector{MatchLabels: map[string]string{"env": "prod", "tier": "web"}},
			labels:   map[string]string{"env": "prod", "tier": "web", "extra": "1"},
			want:     true,
		},
		{
			name:     "matchLabels 值不一致",
			selector: LabelSelector{MatchLabels: map[string]string{"env": "prod"}},
			labels:   map[string]string{"env": "dev"},
			want:     false,
		},
		{
			name:     "matchLabels 键缺失",
			selector: LabelSelector{MatchLabels: map[string]string{"env": "prod"}},
			labels:   map[string]string{},
			want:     false,
		},
		{
			name: "In 命中",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "In", Values: []string{"prod", "staging"}},
			}},
			labels: map[string]string{"env": "staging"},
			want:   true,
		},
		{
			name: "In 不命中",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "In", Values: []string{"prod"}},
			}},
			labels: map[string]string{"env": "dev"},
			want:   false,
		},
		{
			name: "NotIn 键缺失视为满足",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "NotIn", Values: []string{"prod"}},
			}},
			labels: map[string]string{},
			want:   true,
		},
		{
			name: "NotIn 值被排除",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "NotIn", Values: []string{"prod"}},
			}},
			labels: map[string]string{"env": "prod"},
			want:   false,
		},
		{
			name: "Exists 满足",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "Exists"},
			}},
			labels: map[string]string{"env": ""},
			want:   true,
		},
		{
			name: "DoesNotExist 满足",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "DoesNotExist"},
			}},
			labels: map[string]string{"other": "1"},
			want:   true,
		},
		{
			name: "DoesNotExist 键存在则不满足",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "DoesNotExist"},
			}},
			labels: map[string]string{"env": "prod"},
			want:   false,
		},
		{
			name: "组合条件全部满足",
			selector: LabelSelector{
				MatchLabels: map[string]string{"app": "demo"},
				MatchExpressions: []LabelSelectorRequirement{
					{Key: "env", Operator: "In", Values: []string{"prod"}},
					{Key: "debug", Operator: "DoesNotExist"},
				},
			},
			labels: map[string]string{"app": "demo", "env": "prod"},
			want:   true,
		},
		{
			name: "未知操作符不满足",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "env", Operator: "Bogus"},
			}},
			labels: map[string]string{"env": "prod"},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.selector.Matches(tc.labels); got != tc.want {
				t.Fatalf("Matches() = %v, want %v", got, tc.want)
			}
		})
	}
}
