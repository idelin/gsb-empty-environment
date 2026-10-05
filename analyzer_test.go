package main

import (
	"strings"
	"testing"
)

func analyzeSpec(t *testing.T, specJSON string) (*model, *report) {
	t.Helper()
	spec, err := parseSpec([]byte(specJSON))
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}
	m, err := compileModel(spec, 1000000)
	if err != nil {
		t.Fatalf("compileModel: %v", err)
	}
	g := buildGraph(m)
	return m, analyze(m, g)
}

func TestSafeRouting(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["intake", "billing", "tech"]},
	    {"name": "topic", "values": ["invoice", "outage"]}
	  ],
	  "rules": [
	    {"name": "route-billing", "when": {"queue": "intake", "topic": "invoice"}, "set": {"queue": "billing"}},
	    {"name": "route-tech", "when": {"queue": "intake", "topic": "outage"}, "set": {"queue": "tech"}}
	  ]
	}`)
	if !rep.Terminates || !rep.Unique {
		t.Fatalf("expected SAFE, got terminates=%v unique=%v", rep.Terminates, rep.Unique)
	}
	if rep.States != 6 {
		t.Fatalf("expected 6 states, got %d", rep.States)
	}
}

func TestLoopDetected(t *testing.T) {
	m, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["triage", "ops", "support"]}
	  ],
	  "rules": [
	    {"name": "triage-to-ops", "when": {"queue": "triage"}, "set": {"queue": "ops"}},
	    {"name": "ops-to-support", "when": {"queue": "ops"}, "set": {"queue": "support"}},
	    {"name": "support-to-ops", "when": {"queue": "support"}, "set": {"queue": "ops"}}
	  ]
	}`)
	if rep.Terminates {
		t.Fatal("expected non-terminating")
	}
	if rep.Loop == nil {
		t.Fatal("expected loop witness")
	}
	if got := rep.Loop.Input["queue"]; got != "ops" {
		t.Fatalf("expected minimal loop input queue=ops, got %q", got)
	}
	if len(rep.Loop.Steps) != 2 {
		t.Fatalf("expected 2-step loop, got %d", len(rep.Loop.Steps))
	}
	if rep.Loop.Steps[0].Rule != "ops-to-support" || rep.Loop.Steps[1].Rule != "support-to-ops" {
		t.Fatalf("unexpected loop rules: %+v", rep.Loop.Steps)
	}
	if rep.Loop.Steps[1].Note == "" {
		t.Fatal("expected closing note on last loop step")
	}
	_ = m
}

func TestSelfLoopNoOpRule(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["a", "b"]}
	  ],
	  "rules": [
	    {"name": "stay", "when": {"queue": "a"}, "set": {"queue": "a"}}
	  ]
	}`)
	if rep.Terminates {
		t.Fatal("expected non-terminating self-loop")
	}
	if len(rep.Loop.Steps) != 1 {
		t.Fatalf("expected 1-step self-loop, got %d", len(rep.Loop.Steps))
	}
}

func TestNonUniqueFinalQueue(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["intake", "vip", "standard"]},
	    {"name": "customer", "values": ["gold", "silver"]}
	  ],
	  "rules": [
	    {"name": "gold-to-vip", "when": {"queue": "intake", "customer": "gold"}, "set": {"queue": "vip"}},
	    {"name": "any-to-standard", "when": {"queue": "intake"}, "set": {"queue": "standard"}}
	  ]
	}`)
	if !rep.Terminates {
		t.Fatal("expected terminating")
	}
	if rep.Unique {
		t.Fatal("expected non-unique final queue")
	}
	w := rep.Divergence
	if w == nil {
		t.Fatal("expected divergence witness")
	}
	if w.Input["queue"] != "intake" || w.Input["customer"] != "gold" {
		t.Fatalf("unexpected minimal input: %v", w.Input)
	}
	if w.QueueA != "vip" || w.QueueB != "standard" {
		t.Fatalf("unexpected queues: %q vs %q", w.QueueA, w.QueueB)
	}
	if len(w.PathA) != 1 || len(w.PathB) != 1 {
		t.Fatalf("expected 1-step paths, got %d and %d", len(w.PathA), len(w.PathB))
	}
	if w.PathA[0].Rule != "gold-to-vip" || w.PathB[0].Rule != "any-to-standard" {
		t.Fatalf("unexpected path rules: %+v / %+v", w.PathA, w.PathB)
	}
}

// TestStaticCycleButTerminates has a static rule-reference cycle
// (send-to-review enables retry-intake, which re-enables send-to-review),
// yet every execution terminates because the "attempt" field bounds the
// number of round trips. A static cycle check would falsely reject this.
func TestStaticCycleButTerminates(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["intake", "review", "done"]},
	    {"name": "attempt", "values": [0, 1]}
	  ],
	  "rules": [
	    {"name": "send-to-review", "when": {"queue": "intake"}, "set": {"queue": "review"}},
	    {"name": "retry-intake", "when": {"queue": "review", "attempt": 0}, "set": {"queue": "intake", "attempt": 1}},
	    {"name": "finish", "when": {"queue": "review", "attempt": 1}, "set": {"queue": "done"}}
	  ]
	}`)
	if !rep.Terminates || !rep.Unique {
		t.Fatalf("expected SAFE despite static reference cycle, got terminates=%v unique=%v",
			rep.Terminates, rep.Unique)
	}
}

// TestMinimalLoopInput pins the witness to the shortest cycle, not just any
// state that can reach one.
func TestMinimalLoopInput(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["a", "b", "c"]},
	    {"name": "n", "values": [0, 1]}
	  ],
	  "rules": [
	    {"name": "ab", "when": {"queue": "a", "n": 0}, "set": {"queue": "b"}},
	    {"name": "ba", "when": {"queue": "b", "n": 0}, "set": {"queue": "a"}},
	    {"name": "ac", "when": {"queue": "a", "n": 1}, "set": {"queue": "c"}}
	  ]
	}`)
	if rep.Terminates {
		t.Fatal("expected non-terminating")
	}
	if rep.Loop.Input["queue"] != "a" || rep.Loop.Input["n"] != "0" {
		t.Fatalf("expected minimal input queue=a, n=0; got %v", rep.Loop.Input)
	}
	if len(rep.Loop.Steps) != 2 {
		t.Fatalf("expected 2-step loop, got %d", len(rep.Loop.Steps))
	}
}

// TestMinimalDivergenceInput ensures the reported input minimizes the
// combined length of the two divergent paths.
func TestMinimalDivergenceInput(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["start", "mid", "x", "y"]}
	  ],
	  "rules": [
	    {"name": "start-to-mid", "when": {"queue": "start"}, "set": {"queue": "mid"}},
	    {"name": "mid-to-x", "when": {"queue": "mid"}, "set": {"queue": "x"}},
	    {"name": "mid-to-y", "when": {"queue": "mid"}, "set": {"queue": "y"}}
	  ]
	}`)
	if rep.Unique {
		t.Fatal("expected non-unique")
	}
	w := rep.Divergence
	// "mid" diverges in 1+1 steps; "start" needs 2+2. Minimal input is "mid".
	if w.Input["queue"] != "mid" {
		t.Fatalf("expected minimal input queue=mid, got %v", w.Input)
	}
	if len(w.PathA) != 1 || len(w.PathB) != 1 {
		t.Fatalf("expected 1-step paths, got %d and %d", len(w.PathA), len(w.PathB))
	}
}

func TestValueListCondition(t *testing.T) {
	_, rep := analyzeSpec(t, `{
	  "fields": [
	    {"name": "queue", "values": ["a", "b", "c", "done"]}
	  ],
	  "rules": [
	    {"name": "sweep", "when": {"queue": ["a", "b", "c"]}, "set": {"queue": "done"}}
	  ]
	}`)
	if !rep.Terminates || !rep.Unique {
		t.Fatalf("expected SAFE, got terminates=%v unique=%v", rep.Terminates, rep.Unique)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{"no fields", `{"rules": []}`, "at least one field"},
		{"missing queue field", `{
		  "fields": [{"name": "state", "values": ["a"]}],
		  "rules": []
		}`, "queue field"},
		{"unknown when field", `{
		  "fields": [{"name": "queue", "values": ["a"]}],
		  "rules": [{"name": "r", "when": {"nope": "a"}, "set": {}}]
		}`, "unknown field"},
		{"unknown when value", `{
		  "fields": [{"name": "queue", "values": ["a"]}],
		  "rules": [{"name": "r", "when": {"queue": "zzz"}, "set": {}}]
		}`, "not in domain"},
		{"unknown set value", `{
		  "fields": [{"name": "queue", "values": ["a"]}],
		  "rules": [{"name": "r", "when": {}, "set": {"queue": "zzz"}}]
		}`, "not in domain"},
		{"duplicate field", `{
		  "fields": [{"name": "queue", "values": ["a"]}, {"name": "queue", "values": ["b"]}],
		  "rules": []
		}`, "duplicate field"},
		{"duplicate rule name", `{
		  "fields": [{"name": "queue", "values": ["a"]}],
		  "rules": [
		    {"name": "r", "when": {}, "set": {"queue": "a"}},
		    {"name": "r", "when": {}, "set": {"queue": "a"}}
		  ]
		}`, "duplicate rule name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := parseSpec([]byte(tc.spec))
			if err != nil {
				t.Fatalf("parseSpec: %v", err)
			}
			_, err = compileModel(spec, 1000000)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestMaxStatesCap(t *testing.T) {
	spec, err := parseSpec([]byte(`{
	  "fields": [
	    {"name": "queue", "values": ["a", "b"]},
	    {"name": "n", "values": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]}
	  ],
	  "rules": []
	}`))
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}
	if _, err := compileModel(spec, 5); err == nil {
		t.Fatal("expected max-states cap error")
	}
}
