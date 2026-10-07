// Package audit compares two structured snapshots (JSON-like trees of
// map[string]interface{}, []interface{} and scalars) against a set of path
// rules and reports the shallowest violations.
//
// Rules
//
//   - Immutable: paths whose value must be identical in both snapshots. A
//     matched path protects its whole subtree: any addition, removal or
//     modification at or below it is a violation.
//   - AllowAdd: paths where new object members or array elements may appear.
//     A pattern matches the root of an added subtree exactly; everything
//     below that root comes in wholesale.
//   - AllowRemove: mirror of AllowAdd for removed members/elements.
//
// Modifications of existing scalar values are allowed unless the path is
// covered by Immutable. Only member additions and removals require allow
// rules.
//
// Patterns are dot-separated paths with optional "$" prefix; "[N]" addresses
// array elements and "*" (or "[*]") matches exactly one segment (a single
// level), never more. "" or "$" denotes the document root.
//
// Parent replacement and protected descendants
//
// When a parent object is replaced or removed wholesale, the check still
// expands the subtree leaf by leaf: any Immutable path inside the replaced
// or removed subtree whose value changed is reported as a violation, even
// if the replacement/removal itself was permitted.
//
// Output
//
// Violations are collapsed to the shallowest paths: a violation covered by
// another violation on an ancestor path is dropped, and duplicate reports on
// the same path keep the strongest kind (immutable > remove > add). Each
// violation carries the old and new values at its path (Absent when the
// value does not exist on that side).
package audit

import (
	"encoding/json"
	"fmt"
)

// Kind classifies a violation.
type Kind string

const (
	// KindImmutable marks a change at or below an Immutable path.
	KindImmutable Kind = "immutable"
	// KindAdd marks an addition not matched by any AllowAdd pattern.
	KindAdd Kind = "add"
	// KindRemove marks a removal not matched by any AllowRemove pattern.
	KindRemove Kind = "remove"
)

type absent struct{}

// Absent is the sentinel used for Violation.Old / Violation.New when the
// value does not exist on that side of the comparison.
var Absent interface{} = absent{}

// IsAbsent reports whether v is the Absent sentinel.
func IsAbsent(v interface{}) bool {
	_, ok := v.(absent)
	return ok
}

// Violation describes one broken rule at the shallowest affected path.
type Violation struct {
	Path string      // JSONPath-like location, e.g. "$.spec.containers[0].image"; "$" is the root
	Kind Kind        // which rule category was violated
	Old  interface{} // value at Path in the old snapshot, or Absent
	New  interface{} // value at Path in the new snapshot, or Absent
}

func (v Violation) String() string {
	return fmt.Sprintf("%s violation at %s: old=%s new=%s", v.Kind, v.Path, render(v.Old), render(v.New))
}

func render(v interface{}) string {
	if IsAbsent(v) {
		return "<absent>"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// Rules declares the path patterns governing a comparison.
type Rules struct {
	Immutable   []string // paths that must not change (protects whole subtrees)
	AllowAdd    []string // paths where additions are permitted
	AllowRemove []string // paths where removals are permitted
}

// Checker holds compiled rules and can compare many snapshot pairs.
type Checker struct {
	immutable   []pattern
	allowAdd    []pattern
	allowRemove []pattern
}

// Compile validates and compiles the rule patterns.
func Compile(r Rules) (*Checker, error) {
	c := &Checker{}
	var err error
	if c.immutable, err = parseAll(r.Immutable); err != nil {
		return nil, fmt.Errorf("audit: immutable: %v", err)
	}
	if c.allowAdd, err = parseAll(r.AllowAdd); err != nil {
		return nil, fmt.Errorf("audit: allowAdd: %v", err)
	}
	if c.allowRemove, err = parseAll(r.AllowRemove); err != nil {
		return nil, fmt.Errorf("audit: allowRemove: %v", err)
	}
	return c, nil
}

func parseAll(patterns []string) ([]pattern, error) {
	out := make([]pattern, 0, len(patterns))
	for _, s := range patterns {
		p, err := parsePattern(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Compare diffes oldV against newV and returns the shallowest violations,
// sorted by path depth then lexicographically. A nil result means the new
// snapshot complies with the rules. Inputs must be JSON-like values:
// map[string]interface{}, []interface{}, string, float64, bool or nil.
func (c *Checker) Compare(oldV, newV interface{}) []Violation {
	w := &walker{checker: c}
	w.walk(nil, oldV, true, newV, true)
	return finalize(w.violations)
}

// Compare compiles rules and compares oldV against newV in one call.
func Compare(oldV, newV interface{}, r Rules) ([]Violation, error) {
	c, err := Compile(r)
	if err != nil {
		return nil, err
	}
	return c.Compare(oldV, newV), nil
}

// CompareJSON unmarshals both JSON documents and compares them. Numbers
// decode to float64, objects to map[string]interface{}.
func CompareJSON(oldJSON, newJSON []byte, r Rules) ([]Violation, error) {
	var oldV, newV interface{}
	if err := json.Unmarshal(oldJSON, &oldV); err != nil {
		return nil, fmt.Errorf("audit: old snapshot: %v", err)
	}
	if err := json.Unmarshal(newJSON, &newV); err != nil {
		return nil, fmt.Errorf("audit: new snapshot: %v", err)
	}
	return Compare(oldV, newV, r)
}
