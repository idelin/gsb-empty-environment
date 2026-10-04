package precheck

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FoldTable is the explicitly provided folding table used to normalize path
// segments before conflict detection. Nothing is folded implicitly: every
// mapping, reserved name and trim rule comes from this input.
type FoldTable struct {
	// Mappings maps source strings (one or more characters) to their folded
	// form. Longest match wins when several keys share a prefix.
	Mappings map[string]string `json:"mappings"`
	// ReservedNames lists names that are forbidden as path segments. They are
	// folded with Mappings at load time, so "CON" and "con" need not both be
	// listed. A name is also reserved when it is the stem before the first
	// dot of a segment (e.g. "CON.txt").
	ReservedNames []string `json:"reserved_names"`
	// TrimTrailingChars are stripped from the end of every folded segment
	// (Windows-style trailing space/dot rule).
	TrimTrailingChars string `json:"trim_trailing_chars"`

	maxKeyLen int
	reserved  map[string]bool
}

// NewFoldTable builds a FoldTable from explicit inputs.
func NewFoldTable(mappings map[string]string, reserved []string, trimTrailing string) *FoldTable {
	t := &FoldTable{
		Mappings:          mappings,
		ReservedNames:     reserved,
		TrimTrailingChars: trimTrailing,
	}
	t.compile()
	return t
}

// LoadFoldTable reads a FoldTable from a JSON file.
func LoadFoldTable(path string) (*FoldTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t FoldTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse fold table %s: %w", path, err)
	}
	if t.Mappings == nil {
		t.Mappings = map[string]string{}
	}
	t.compile()
	return &t, nil
}

func (t *FoldTable) compile() {
	t.maxKeyLen = 0
	for k := range t.Mappings {
		if len(k) > t.maxKeyLen {
			t.maxKeyLen = len(k)
		}
	}
	t.reserved = make(map[string]bool, len(t.ReservedNames))
	for _, name := range t.ReservedNames {
		t.reserved[t.foldString(name)] = true
	}
}

// foldString applies the mapping table with greedy longest-match semantics.
func (t *FoldTable) foldString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		matched := false
		maxLen := t.maxKeyLen
		if rem := len(s) - i; maxLen > rem {
			maxLen = rem
		}
		for l := maxLen; l > 0; l-- {
			if rep, ok := t.Mappings[s[i:i+l]]; ok {
				b.WriteString(rep)
				i += l
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// IssueKind classifies a per-segment normalization violation.
type IssueKind string

const (
	IssueTrailingTrim IssueKind = "trailing-trim"
	IssueEmptyName    IssueKind = "empty-name"
	IssueReservedName IssueKind = "reserved-name"
)

// SegmentIssue records a violation found while normalizing one segment.
type SegmentIssue struct {
	Segment string    `json:"segment"`
	Kind    IssueKind `json:"kind"`
}

func (i SegmentIssue) String() string {
	switch i.Kind {
	case IssueTrailingTrim:
		return fmt.Sprintf("segment %q ends with characters stripped by the trailing-trim rule", i.Segment)
	case IssueEmptyName:
		return fmt.Sprintf("segment %q is empty after normalization", i.Segment)
	case IssueReservedName:
		return fmt.Sprintf("segment %q folds to a reserved name", i.Segment)
	default:
		return fmt.Sprintf("segment %q: %s", i.Segment, i.Kind)
	}
}

// NormalizeSegment folds one path segment and applies the trailing-trim and
// reserved-name rules, returning the normalized form plus any violations.
func (t *FoldTable) NormalizeSegment(seg string) (string, []SegmentIssue) {
	folded := t.foldString(seg)
	trimmed := strings.TrimRight(folded, t.TrimTrailingChars)
	var issues []SegmentIssue
	if trimmed != folded {
		issues = append(issues, SegmentIssue{Segment: seg, Kind: IssueTrailingTrim})
	}
	if trimmed == "" {
		issues = append(issues, SegmentIssue{Segment: seg, Kind: IssueEmptyName})
		return trimmed, issues
	}
	if t.reserved[trimmed] || t.reserved[stemOf(trimmed)] {
		issues = append(issues, SegmentIssue{Segment: seg, Kind: IssueReservedName})
	}
	return trimmed, issues
}

func stemOf(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}
