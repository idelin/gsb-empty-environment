package gdict

import (
	"fmt"
	"strings"
)

// Observation is one concrete constraint that participates in an
// incompatibility certificate. A certificate always contains at least two
// observations or two fixed-code constraints.
type Observation struct {
	Kind         string `json:"kind"` // "collation-order" or "range-scan"
	Version      string `json:"version,omitempty"`
	Partition    string `json:"partition,omitempty"`
	Cut          string `json:"cut,omitempty"`
	OldCode      uint64 `json:"old_code,omitempty"`
	Bound        Bound  `json:"bound,omitempty"`
	LeftKey      Key    `json:"left_key"`
	RightKey     Key    `json:"right_key"`
	LeftOldCode  uint64 `json:"left_old_code,omitempty"`
	RightOldCode uint64 `json:"right_old_code,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

func (o Observation) String() string {
	if o.Kind == "reserved-hole" {
		return fmt.Sprintf("reserved hole [%d,%d] conflicts with fixed %s", o.OldCode, o.RightOldCode, o.LeftKey)
	}
	if o.Kind == "fixed-order" || o.Kind == "code-space" || o.Kind == "fixed-code" || o.Kind == "alias" || o.Kind == "fixed-order-observation" {
		if o.Detail != "" {
			return o.Detail
		}
		return fmt.Sprintf("%s: %s < %s", o.Kind, o.LeftKey, o.RightKey)
	}
	if o.Kind == "range-scan" {
		return fmt.Sprintf("range-scan %s/%s: code %s %d forces %s (old code %d) before %s (old code %d)",
			o.Partition, o.Cut, o.Bound, o.OldCode, o.LeftKey, o.LeftOldCode, o.RightKey, o.RightOldCode)
	}
	return fmt.Sprintf("collation %s: %s < %s", o.Version, o.LeftKey, o.RightKey)
}

// Certificate explains why no mapping can satisfy the listed old queries.
// Either Observations has at least two entries (a cycle of concrete range
// observations), or Fixed contains at least two fixed-code constraints.
type Certificate struct {
	Kind         string        `json:"kind"`
	Message      string        `json:"message"`
	Observations []Observation `json:"observations,omitempty"`
	Fixed        []Fixed       `json:"fixed,omitempty"`
	Requirement  string        `json:"requirement,omitempty"`
}

// IncompatibleError is returned when the input is well formed but no global
// dictionary can preserve all listed queries.
type IncompatibleError struct {
	Cert Certificate
}

func (e *IncompatibleError) Error() string {
	var b strings.Builder
	b.WriteString("gdict: incompatible: ")
	b.WriteString(e.Cert.Message)
	if len(e.Cert.Observations) > 0 {
		b.WriteString("; observations: ")
		for i, o := range e.Cert.Observations {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(o.String())
		}
	}
	if len(e.Cert.Fixed) > 0 {
		b.WriteString("; fixed constraints: ")
		for i, f := range e.Cert.Fixed {
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s pinned at %d", f.Key, f.Code)
		}
	}
	if e.Cert.Requirement != "" {
		b.WriteString("; ")
		b.WriteString(e.Cert.Requirement)
	}
	return b.String()
}

func incompatible(kind, msg string) error {
	return &IncompatibleError{Cert: Certificate{Kind: kind, Message: msg}}
}
