// Package contentmatch implements streaming rule matching for an intrusion
// detection engine. Rules describe several content patterns plus relative
// position constraints (offset/depth relative to the payload start, and
// distance/within relative to the end of the previous pattern match).
// All rules share a single Aho-Corasick multi-pattern automaton, so a
// payload is scanned once regardless of the number of rules. Payloads may
// arrive in arbitrary segments; patterns may span segment boundaries.
// Per-flow state is bounded by a configurable candidate limit.
package contentmatch

import "errors"

// Pattern is one content of a rule together with its position constraints.
//
// Absolute constraints (Offset/Depth) are measured from the start of the
// payload. Relative constraints (Distance/Within) are measured from the end
// of the previous pattern's match; they only apply to non-first patterns.
//
// For a match spanning [start, end) with previous match end prevEnd:
//
//	start >= Offset                    (if Offset > 0)
//	end   <= Offset + Depth            (if Depth > 0)
//	start >= prevEnd + Distance        (non-first pattern)
//	end   <= prevEnd + Distance + Within (non-first pattern, if Within > 0)
type Pattern struct {
	Content  []byte
	Offset   int
	Depth    int // 0 means unlimited
	Distance int
	Within   int // 0 means unlimited
}

// Rule is an ordered list of patterns that must all match, each satisfying
// its constraints relative to the payload start and the previous match.
type Rule struct {
	Patterns []Pattern
}

// Span is a half-open byte range [Start, End) in the reassembled stream.
type Span struct {
	Start, End int
}

// Match reports one full rule match. Spans has one entry per pattern of the
// rule, in rule order.
type Match struct {
	Rule  int
	Spans []Span
}

// absOK checks the absolute (payload-relative) constraints.
func absOK(p *Pattern, start, end int) bool {
	if p.Offset > 0 && start < p.Offset {
		return false
	}
	if p.Depth > 0 && end > p.Offset+p.Depth {
		return false
	}
	return true
}

// relOK checks the relative (previous-match-relative) constraints.
func relOK(p *Pattern, start, end, prevEnd int) bool {
	if start < prevEnd+p.Distance {
		return false
	}
	if p.Within > 0 && end > prevEnd+p.Distance+p.Within {
		return false
	}
	return true
}

func validate(rules []Rule) error {
	if len(rules) == 0 {
		return errors.New("contentmatch: no rules")
	}
	for _, r := range rules {
		if len(r.Patterns) == 0 {
			return errors.New("contentmatch: rule without patterns")
		}
		for _, p := range r.Patterns {
			if len(p.Content) == 0 {
				return errors.New("contentmatch: empty content")
			}
			if p.Offset < 0 || p.Depth < 0 || p.Distance < 0 || p.Within < 0 {
				return errors.New("contentmatch: negative constraint")
			}
		}
	}
	return nil
}
