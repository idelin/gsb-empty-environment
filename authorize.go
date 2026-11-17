package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Authorize applies a structured patch to an approved, normalized template
// and returns the resulting template together with an authorization decision
// for every structural change. A single DENY makes the whole result denied;
// the returned template is still populated so callers can audit the attempt.
func Authorize(bound NormalizedTemplate, reg *fieldRegistry, ops []PatchOperation) (NormalizedTemplate, []ChangeDecision, error) {
	patched, opRecords, err := ApplyPatch(bound.Raw, ops)
	if err != nil {
		return NormalizedTemplate{}, nil, err
	}

	var candidate WorkloadTemplate
	if err := fromMap(patched, &candidate); err != nil {
		return NormalizedTemplate{}, nil, err
	}
	normalized, err := NormalizeTemplate(candidate)
	if err != nil {
		return NormalizedTemplate{}, nil, err
	}

	structural := structuralDiff(bound.Raw, patched, "")
	opByPath := indexOperations(ops)

	decisions := make([]ChangeDecision, 0, len(structural))
	denied := false
	for _, change := range structural {
		decision := reg.evaluate(change, bound, normalized, opByPath[change.Path])
		if decision.Verdict == VerdictDeny {
			denied = true
		}
		decisions = append(decisions, decision)
	}

	// Structural diffs are the complete authorization record: every add,
	// remove and replace reaches the evaluator. Operations whose value is
	// unchanged after normalization appear as no structural diff and are
	// reported explicitly as no-ops.
	effected := map[string]struct{}{}
	for _, change := range structural {
		effected[change.Path] = struct{}{}
	}
	for _, record := range opRecords {
		if record.Basis == BasisNoOp {
			record.Field = record.Path
			record.Class = reg.classify(record.Path)
			decisions = append(decisions, record)
			continue
		}
		if _, ok := effected[record.Path]; ok || record.Op == "move" {
			continue
		}
		if record.Op == "add" && strings.HasSuffix(record.Path, "/-") {
			continue // array append materializes at its concrete index
		}
		record.Field = record.Path
		record.Class = reg.classify(record.Path)
		record.Verdict = VerdictAllow
		record.Basis = BasisNoOp
		record.Detail = "normalized to identical value"
		decisions = append(decisions, record)
	}

	sortDecisions(decisions)
	if denied {
		return normalized, decisions, errDenied
	}
	return normalized, decisions, nil
}

type rawChange struct {
	Op       string
	Path     string
	OldValue any
	NewValue any
}

// structuralDiff reports every leaf-level difference between two raw trees.
func structuralDiff(a, b any, prefix string) []rawChange {
	am, aMap := a.(map[string]any)
	bm, bMap := b.(map[string]any)
	if aMap && bMap {
		keys := map[string]struct{}{}
		for k := range am {
			keys[k] = struct{}{}
		}
		for k := range bm {
			keys[k] = struct{}{}
		}
		var out []rawChange
		for k := range keys {
			out = append(out, structuralDiff(am[k], bm[k], prefix+"/"+escapeToken(k))...)
		}
		return out
	}

	aa, aArray := a.([]any)
	ba, bArray := b.([]any)
	if aArray && bArray {
		var out []rawChange
		maxLen := len(aa)
		if len(ba) > maxLen {
			maxLen = len(ba)
		}
		for i := 0; i < maxLen; i++ {
			out = append(out, structuralDiff(safeIndex(aa, i), safeIndex(ba, i), fmt.Sprintf("%s/%d", prefix, i))...)
		}
		return out
	}

	switch {
	case a == nil && b != nil:
		return []rawChange{{Op: "add", Path: prefix, NewValue: b}}
	case a != nil && b == nil:
		return []rawChange{{Op: "remove", Path: prefix, OldValue: a}}
	case !deepEqual(a, b):
		return []rawChange{{Op: "replace", Path: prefix, OldValue: a, NewValue: b}}
	}
	return nil
}

func safeIndex(s []any, i int) any {
	if i < len(s) {
		return s[i]
	}
	return nil
}

func escapeToken(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}

func indexOperations(ops []PatchOperation) map[string]PatchOperation {
	out := map[string]PatchOperation{}
	for _, op := range ops {
		out[op.Path] = op
		if op.Op == "move" {
			out[op.From] = op
		}
	}
	return out
}

// evaluate maps one structural difference to an authorization decision using
// the frozen approval and the normalized candidate.
func (r *fieldRegistry) evaluate(change rawChange, bound, candidate NormalizedTemplate, op PatchOperation) ChangeDecision {
	decision := ChangeDecision{
		Op:       change.Op,
		Path:     change.Path,
		Field:    change.Path,
		OldValue: change.OldValue,
		NewValue: change.NewValue,
	}
	if op.Op != "" {
		decision.Op = op.Op
		decision.From = op.From
	}

	class := r.classify(change.Path)
	decision.Class = class

	switch class {
	case classSecurity:
		return evaluateSecurity(decision, change, bound, candidate)
	case classMutable:
		decision.Verdict = VerdictAllow
		decision.Basis = BasisMutableAllowList
		decision.Detail = "path is on the approved non-security mutable allow-list"
		return decision
	case classUnregistered:
		decision.Verdict = VerdictDeny
		decision.Basis = BasisUnregisteredField
		decision.Detail = "field is not covered by the approval"
		return decision
	default:
		decision.Verdict = VerdictDeny
		decision.Basis = BasisImmutableField
		decision.Detail = "registered non-security field is not on the mutable allow-list"
		return decision
	}
}

func evaluateSecurity(decision ChangeDecision, change rawChange, bound, candidate NormalizedTemplate) ChangeDecision {
	// Image changes are authorized by digest identity only.
	if change.Path == "/spec/image" {
		return evaluateImage(decision, bound, candidate)
	}

	normalizedOld, _, _ := pointerGet(bound.Raw, change.Path)
	normalizedNew, _, _ := pointerGet(candidate.Raw, change.Path)
	if deepEqual(normalizedOld, normalizedNew) {
		decision.Verdict = VerdictAllow
		decision.Basis = BasisFrozenSecurity
		if change.Op == "remove" {
			decision.Detail = "deletion restored the approved default value"
			decision.NewValue = normalizedNew
		} else {
			decision.Detail = "security field is frozen by the approval; normalization kept the bound value"
		}
		return decision
	}

	decision.Verdict = VerdictDeny
	decision.Basis = BasisFrozenSecurity
	decision.Detail = "security-bound value differs from the approved snapshot"
	return decision
}

func evaluateImage(decision ChangeDecision, bound, candidate NormalizedTemplate) ChangeDecision {
	boundRef, err := ParseImage(bound.Template.Spec.Image)
	if err != nil {
		decision.Verdict = VerdictDeny
		decision.Basis = BasisDigestPinned
		decision.Detail = "bound image reference is invalid: " + err.Error()
		return decision
	}
	if candidate.Image.SameDigest(boundRef) {
		decision.Verdict = VerdictAllow
		decision.Basis = BasisDigestPinned
		decision.Detail = "image reference resolves to the approved digest"
		return decision
	}
	decision.Verdict = VerdictDeny
	decision.Basis = BasisDigestPinned
	decision.Detail = "image digest or repository differs from the approved binding"
	return decision
}

func sortDecisions(decisions []ChangeDecision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].Path == decisions[j].Path {
			return decisions[i].Op < decisions[j].Op
		}
		return decisions[i].Path < decisions[j].Path
	})
}
