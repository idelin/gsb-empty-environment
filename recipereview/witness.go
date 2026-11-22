package recipereview

import (
	"bytes"
	"errors"
)

// WitnessResult is the independent re-verification of a claimed witness.
type WitnessResult struct {
	// Valid is true only when the witness genuinely demonstrates its claimed
	// violation under the current recipe definitions.
	Valid bool
	// Reason explains why a witness was rejected.
	Reason string
	// RecomputedInfo holds the independently re-encoded bytes when valid.
	RecomputedInfo []byte
}

// VerifyWitness independently re-checks a witness against the recipe set rather
// than trusting any byte carried inside it. It re-validates the assignments,
// re-encodes with both independent encoders (recursive and opcode), and for
// read-only claims re-decodes through the accepting recipe. The compatibility
// table decides whether same-purpose versions are actually coexisting.
func VerifyWitness(recipes []*Recipe, compat *CompatTable, w *Witness) WitnessResult {
	if w == nil {
		return WitnessResult{Reason: "nil witness"}
	}
	r1 := findRecipe(recipes, w.FirstRecipeID)
	r2 := findRecipe(recipes, w.SecondRecipeID)
	if r1 == nil || r2 == nil {
		return WitnessResult{Reason: "witness references unknown recipe ID"}
	}
	if err := r1.ValidateAssignment(w.First); err != nil {
		return WitnessResult{Reason: "first assignment is invalid: " + err.Error()}
	}
	if err := r2.ValidateAssignment(w.Second); err != nil {
		return WitnessResult{Reason: "second assignment is invalid: " + err.Error()}
	}
	info1, err := encodeBothWays(r1, w.First)
	if err != nil {
		return WitnessResult{Reason: "first recipe encoding failed: " + err.Error()}
	}
	info2, err := encodeBothWays(r2, w.Second)
	if err != nil {
		return WitnessResult{Reason: "second recipe encoding failed: " + err.Error()}
	}
	if w.FirstInfo != nil && !bytes.Equal(w.FirstInfo, info1) {
		return WitnessResult{Reason: "witness FirstInfo does not match re-encoded bytes"}
	}
	if w.SecondInfo != nil && !bytes.Equal(w.SecondInfo, info2) {
		return WitnessResult{Reason: "witness SecondInfo does not match re-encoded bytes"}
	}

	switch w.Kind {
	case WitnessCollision:
		if r1.ID == r2.ID && assignmentsEqual(r1, w.First, w.Second) {
			return WitnessResult{Reason: "collision witness uses identical assignments"}
		}
		if !bytes.Equal(info1, info2) {
			return WitnessResult{Reason: "re-encoded info strings differ; no collision"}
		}
		if !coexists(r1, r2, compat) {
			return WitnessResult{Reason: "the two recipes never coexist under the same root key and salt"}
		}
		return WitnessResult{Valid: true, RecomputedInfo: info1}
	case WitnessReadOnly:
		if r1.ID == r2.ID {
			return WitnessResult{Reason: "read-only witness must span two recipes"}
		}
		var legacy, active *Recipe
		var legacyInfo, activeInfo []byte
		switch {
		case r1.ReadOnly && !r2.ReadOnly:
			legacy, active = r1, r2
			legacyInfo, activeInfo = info1, info2
		case r2.ReadOnly && !r1.ReadOnly:
			legacy, active = r2, r1
			legacyInfo, activeInfo = info2, info1
		default:
			return WitnessResult{Reason: "read-only witness needs exactly one read-only recipe"}
		}
		if legacy.Purpose == active.Purpose {
			return WitnessResult{Reason: "read-only rule only applies across different purposes"}
		}
		if !bytes.Equal(legacyInfo, activeInfo) {
			return WitnessResult{Reason: "read-only encoding does not match the active recipe bytes"}
		}
		decoded, err := active.Decode(legacyInfo)
		if err != nil {
			return WitnessResult{Reason: "active recipe rejects the legacy encoding"}
		}
		if !assignmentsEqual(active, decoded, w.assignmentFor(active.ID)) {
			return WitnessResult{Reason: "active recipe decodes the legacy bytes to a different assignment"}
		}
		return WitnessResult{Valid: true, RecomputedInfo: legacyInfo}
	default:
		return WitnessResult{Reason: "unknown witness kind"}
	}
}

func (w *Witness) assignmentFor(recipeID int) Assignment {
	if w.FirstRecipeID == recipeID {
		return w.First
	}
	return w.Second
}

func findRecipe(recipes []*Recipe, id int) *Recipe {
	for _, r := range recipes {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// encodeBothWays runs the recursive encoder and the independent opcode program
// and rejects any divergence between them.
func encodeBothWays(r *Recipe, in Assignment) ([]byte, error) {
	info, err := r.Encode(in)
	if err != nil {
		return nil, err
	}
	prog, err := r.CompileProgram()
	if err != nil {
		return nil, err
	}
	alt, err := prog.Encode(in)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(info, alt) {
		return nil, errors.New("recipereview: independent encoders disagree")
	}
	return info, nil
}

func coexists(r1, r2 *Recipe, compat *CompatTable) bool {
	if r1.Purpose != r2.Purpose {
		return true
	}
	if compat == nil {
		compat = NewCompatTable()
	}
	return compat.Allows(r1.ref(), r2.ref())
}
