package main

import (
	"fmt"
	"strings"
)

// Rule rewrites a label set. Rules are applied in an explicit, user-defined
// order (the order of the slice passed to the engine).
type Rule interface {
	ID() string
	// Apply returns the rewritten label set. If nothing changes it may
	// return the input unchanged.
	Apply(ls LabelSet) LabelSet
	// Targets lists every label the rule may create, modify or delete.
	// Rules may not target reserved labels.
	Targets() []string
	Describe() string
}

// RenameLabel moves the value of label From to label To.
type RenameLabel struct {
	RuleID string
	From   string
	To     string
}

func (r RenameLabel) ID() string          { return r.RuleID }
func (r RenameLabel) Targets() []string   { return []string{r.From, r.To} }
func (r RenameLabel) Describe() string    { return fmt.Sprintf("rename %s -> %s", r.From, r.To) }
func (r RenameLabel) Apply(ls LabelSet) LabelSet {
	v, ok := ls[r.From]
	if !ok {
		return ls
	}
	out := ls.Clone()
	delete(out, r.From)
	out[r.To] = v
	return out
}

// DeleteLabel drops a label. Deleting a label that distinguishes two series
// makes them collide; without explicit aggregation that is a conflict and
// neither series may overwrite the other.
type DeleteLabel struct {
	RuleID string
	Label  string
}

func (r DeleteLabel) ID() string        { return r.RuleID }
func (r DeleteLabel) Targets() []string { return []string{r.Label} }
func (r DeleteLabel) Describe() string  { return "delete " + r.Label }
func (r DeleteLabel) Apply(ls LabelSet) LabelSet {
	if _, ok := ls[r.Label]; !ok {
		return ls
	}
	out := ls.Clone()
	delete(out, r.Label)
	return out
}

// SynthLabel synthesizes a label from a template. "{name}" placeholders are
// expanded from the current label set.
type SynthLabel struct {
	RuleID   string
	Label    string
	Template string
}

func (r SynthLabel) ID() string        { return r.RuleID }
func (r SynthLabel) Targets() []string { return []string{r.Label} }
func (r SynthLabel) Describe() string {
	return fmt.Sprintf("synth %s=%q", r.Label, r.Template)
}
func (r SynthLabel) Apply(ls LabelSet) LabelSet {
	val := expandTemplate(r.Template, ls)
	if cur, ok := ls[r.Label]; ok && cur == val {
		return ls
	}
	out := ls.Clone()
	out[r.Label] = val
	return out
}

func expandTemplate(tpl string, ls LabelSet) string {
	var b strings.Builder
	i := 0
	for i < len(tpl) {
		if tpl[i] == '{' {
			if j := strings.IndexByte(tpl[i:], '}'); j > 0 {
				b.WriteString(ls[tpl[i+1:i+j]])
				i += j + 1
				continue
			}
		}
		b.WriteByte(tpl[i])
		i++
	}
	return b.String()
}

// ValidateRules checks a rule batch: unique IDs and no reserved targets.
func ValidateRules(rules []Rule, reserved map[string]bool) error {
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		if r.ID() == "" {
			return fmt.Errorf("rule %q has empty id", r.Describe())
		}
		if seen[r.ID()] {
			return fmt.Errorf("duplicate rule id %q", r.ID())
		}
		seen[r.ID()] = true
		for _, t := range r.Targets() {
			if reserved[t] {
				return fmt.Errorf("rule %q targets reserved label %q", r.ID(), t)
			}
		}
	}
	return nil
}
