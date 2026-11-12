// Package netsec evaluates a simplified in-toto layout: each step's
// material rules (MATCH ... WITH PRODUCTS FROM ..., ALLOW, DISALLOW)
// run in order against the recorded link metadata of a supply chain.
package netsec

import "fmt"

// Artifact is a recorded file: its path and digest must both match.
type Artifact struct {
	Path   string
	SHA256 string
}

func (a Artifact) equals(b Artifact) bool {
	return a.Path == b.Path && a.SHA256 == b.SHA256
}

// RuleKind identifies the three supported material rule types.
type RuleKind int

const (
	// RuleMatch consumes artifacts recorded as products of SrcStep whose
	// path matches Pattern; each must reappear, with identical path and
	// sha256, in the current step's materials.
	RuleMatch RuleKind = iota
	// RuleAllow consumes remaining materials matching Pattern.
	RuleAllow
	// RuleDisallow fails verification if any remaining material matches
	// Pattern.
	RuleDisallow
)

// Rule is a single material rule of a layout step.
type Rule struct {
	Kind    RuleKind
	Pattern string
	SrcStep string // only for RuleMatch: WITH PRODUCTS FROM <SrcStep>
}

// Match builds a MATCH <pattern> WITH PRODUCTS FROM <srcStep> rule.
func Match(pattern, srcStep string) Rule {
	return Rule{Kind: RuleMatch, Pattern: pattern, SrcStep: srcStep}
}

// Allow builds an ALLOW <pattern> rule.
func Allow(pattern string) Rule {
	return Rule{Kind: RuleAllow, Pattern: pattern}
}

// Disallow builds a DISALLOW <pattern> rule.
func Disallow(pattern string) Rule {
	return Rule{Kind: RuleDisallow, Pattern: pattern}
}

// Step is one layout step with its ordered material rules.
type Step struct {
	Name          string
	MaterialRules []Rule
}

// Layout is the supply-chain layout to verify.
type Layout struct {
	Steps []Step
}

// Link is the recorded metadata of one executed step.
type Link struct {
	StepName  string
	Materials []Artifact
	Products  []Artifact
}

// VerificationError describes a failed rule evaluation.
type VerificationError struct {
	Step   string
	Detail string
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("in-toto verification failed at step %q: %s", e.Step, e.Detail)
}

// Verify evaluates every step's material rules in order against the
// recorded links. Rules consume artifacts: an artifact already matched
// (or allowed) cannot be matched again. Artifacts left over after all
// rules are permitted unless a DISALLOW rule rejects them, which is why
// a trailing DISALLOW * is the safety net against smuggled artifacts.
func Verify(layout Layout, links map[string]Link) error {
	consumedProducts := map[string]map[int]bool{}
	for _, step := range layout.Steps {
		link, ok := links[step.Name]
		if !ok {
			return &VerificationError{Step: step.Name, Detail: "missing link metadata"}
		}
		queue := append([]Artifact(nil), link.Materials...)
		for _, rule := range step.MaterialRules {
			var err error
			switch rule.Kind {
			case RuleMatch:
				err = applyMatch(step, rule, links, consumedProducts, &queue)
			case RuleAllow:
				queue = filterOut(queue, rule.Pattern)
			case RuleDisallow:
				err = applyDisallow(step, rule, queue)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func applyMatch(step Step, rule Rule, links map[string]Link, consumed map[string]map[int]bool, queue *[]Artifact) error {
	src, ok := links[rule.SrcStep]
	if !ok {
		return &VerificationError{Step: step.Name, Detail: fmt.Sprintf("MATCH references unknown step %q", rule.SrcStep)}
	}
	used := consumed[rule.SrcStep]
	if used == nil {
		used = map[int]bool{}
		consumed[rule.SrcStep] = used
	}
	for i, product := range src.Products {
		if used[i] || !globMatch(rule.Pattern, product.Path) {
			continue
		}
		idx := indexOf(*queue, product)
		if idx < 0 {
			return &VerificationError{
				Step: step.Name,
				Detail: fmt.Sprintf("MATCH %q WITH PRODUCTS FROM %q: no material with both path %q and sha256 %q",
					rule.Pattern, rule.SrcStep, product.Path, product.SHA256),
			}
		}
		*queue = append((*queue)[:idx], (*queue)[idx+1:]...)
		used[i] = true
	}
	return nil
}

func applyDisallow(step Step, rule Rule, queue []Artifact) error {
	for _, a := range queue {
		if globMatch(rule.Pattern, a.Path) {
			return &VerificationError{
				Step:   step.Name,
				Detail: fmt.Sprintf("DISALLOW %q rejects leftover material %q (sha256 %q)", rule.Pattern, a.Path, a.SHA256),
			}
		}
	}
	return nil
}

func indexOf(queue []Artifact, want Artifact) int {
	for i, a := range queue {
		if a.equals(want) {
			return i
		}
	}
	return -1
}

func filterOut(queue []Artifact, pattern string) []Artifact {
	kept := queue[:0]
	for _, a := range queue {
		if !globMatch(pattern, a.Path) {
			kept = append(kept, a)
		}
	}
	return kept
}
