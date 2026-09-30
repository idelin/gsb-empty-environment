// Package spaneval evaluates actual telemetry spans against an expected
// span structure declared as a finite rule graph, and reports missing,
// duplicate and unfinished ratios together with a public score formula.
package spaneval

import (
	"fmt"
	"strings"
)

// Rule is one node of the expected span structure graph.
//
//   - Kind "seq" (default): every child is expected, subject to its own
//     repeat count and condition.
//   - Kind "choice": exactly one of the children (mutually exclusive
//     branches) is expected.
//   - When is a condition on trace attributes, "key=value" or "key!=value".
//     A node whose condition is false is not expected at all.
//   - Min/Max give the repeat count; both default to 1. Min=0 marks an
//     optional node.
type Rule struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind,omitempty"`
	When     string  `json:"when,omitempty"`
	Min      *int    `json:"min,omitempty"`
	Max      *int    `json:"max,omitempty"`
	Children []*Rule `json:"children,omitempty"`
}

// MinN returns the minimum repeat count (default 1).
func (r *Rule) MinN() int {
	if r.Min == nil {
		return 1
	}
	return *r.Min
}

// MaxN returns the maximum repeat count (default 1).
func (r *Rule) MaxN() int {
	if r.Max == nil {
		return 1
	}
	return *r.Max
}

// Span is an actual telemetry span reconstructed from events.
type Span struct {
	TraceID  string            `json:"trace_id"`
	SpanID   string            `json:"span_id"`
	ParentID string            `json:"parent_id,omitempty"`
	Name     string            `json:"name"`
	Start    int64             `json:"start"`
	End      int64             `json:"end,omitempty"`
	Ended    bool              `json:"ended"`
	Attrs    map[string]string `json:"attrs,omitempty"`
}

// Event is a locally synthesized telemetry event. Event logs are produced
// by Synth or by fixtures, and folded into spans by Replay.
type Event struct {
	Type     string            `json:"type"` // span_start | span_end | trace_attrs
	TraceID  string            `json:"trace_id"`
	SpanID   string            `json:"span_id,omitempty"`
	ParentID string            `json:"parent_id,omitempty"`
	Name     string            `json:"name,omitempty"`
	TS       int64             `json:"ts"`
	Attrs    map[string]string `json:"attrs,omitempty"`
}

// Validate checks the rule graph for structural errors.
func Validate(r *Rule) error {
	if r == nil {
		return fmt.Errorf("spaneval: nil rule")
	}
	if r.Name == "" {
		return fmt.Errorf("spaneval: rule with empty name")
	}
	switch r.Kind {
	case "", "seq":
	case "choice":
		if len(r.Children) == 0 {
			return fmt.Errorf("spaneval: choice node %q needs branches", r.Name)
		}
		for _, b := range r.Children {
			if b.MinN() != 1 || b.MaxN() != 1 {
				return fmt.Errorf("spaneval: choice branch %q of %q must have min=max=1", b.Name, r.Name)
			}
		}
	default:
		return fmt.Errorf("spaneval: unknown kind %q on %q", r.Kind, r.Name)
	}
	if r.MinN() < 0 || r.MaxN() < 1 || r.MinN() > r.MaxN() {
		return fmt.Errorf("spaneval: bad repeat count min=%d max=%d on %q", r.MinN(), r.MaxN(), r.Name)
	}
	if err := checkCondition(r.When); err != nil {
		return fmt.Errorf("spaneval: rule %q: %w", r.Name, err)
	}
	for _, c := range r.Children {
		if err := Validate(c); err != nil {
			return err
		}
	}
	return nil
}

func checkCondition(when string) error {
	if when == "" {
		return nil
	}
	if i := strings.Index(when, "!="); i > 0 && i+2 < len(when) {
		return nil
	}
	if i := strings.Index(when, "="); i > 0 && i+1 <= len(when) {
		return nil
	}
	return fmt.Errorf("malformed condition %q (want key=value or key!=value)", when)
}

// condOK evaluates a "key=value" / "key!=value" condition against trace attrs.
func condOK(when string, attrs map[string]string) bool {
	if when == "" {
		return true
	}
	if i := strings.Index(when, "!="); i >= 0 {
		return attrs[when[:i]] != when[i+2:]
	}
	if i := strings.Index(when, "="); i >= 0 {
		return attrs[when[:i]] == when[i+1:]
	}
	return false
}
