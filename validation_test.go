package main

import "testing"

func goodHostMap() map[string]Host {
	emptyStep := []string{}
	return map[string]Host{
		"h": {Port: 1, TTL: 1, Steps: [][]string{{"1.1.1.1"}}},
		"e": {Port: 1, TTL: 1, Steps: [][]string{emptyStep}},
	}
}

func TestInputValidation(t *testing.T) {
	cases := []struct {
		name string
		sc   Scenario
	}{
		{
			name: "empty resolution step",
			sc: Scenario{
				Hosts:    map[string]Host{"e": goodHostMap()["e"]},
				Requests: []RequestSpec{{ID: "r", Host: "e"}},
			},
		},
		{
			name: "duplicate request id",
			sc: Scenario{
				Hosts: goodHostMap(),
				Requests: []RequestSpec{
					{ID: "dup", Host: "h"},
					{ID: "dup", Host: "h"},
				},
			},
		},
		{
			name: "unknown host",
			sc: Scenario{
				Hosts:    goodHostMap(),
				Requests: []RequestSpec{{ID: "r", Host: "missing"}},
			},
		},
		{
			name: "dial before validate",
			sc: Scenario{
				Hosts:    goodHostMap(),
				Requests: []RequestSpec{{ID: "r", Host: "h", ValidateAt: 5, DialAt: 4}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := execute(tc.sc); err == nil {
				t.Fatalf("%s must fail validation", tc.name)
			}
		})
	}
}
