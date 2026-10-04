// Package judge classifies a pair of outcomes. Its core contract:
//
//   - timeout is infrastructure, never a value inequality;
//   - run error is infrastructure, never a value inequality;
//   - parse failure (output violates the problem-given format) is distinct
//     from a semantic difference between two well-formed values;
//   - only normalized values are ever compared.
package judge

import (
	"fmt"
	"strings"

	"difftest/internal/normalize"
	"difftest/internal/runner"
)

// Verdict is the final classification of a differential run.
type Verdict string

const (
	Equal        Verdict = "equal"         // normalized values match
	SemanticDiff Verdict = "semantic_diff" // well-formed values differ
	ParseError   Verdict = "parse_error"   // output violates format rules
	RunError     Verdict = "run_error"     // non-zero exit / spawn failure
	Timeout      Verdict = "timeout"       // deadline exceeded
)

// Result describes the judged pair.
type Result struct {
	Verdict Verdict          `json:"verdict"`
	Sides   []string         `json:"sides,omitempty"` // "A"/"B" implicated in infra verdicts
	NormA   normalize.Result `json:"norm_a"`
	NormB   normalize.Result `json:"norm_b"`
	Detail  string           `json:"detail,omitempty"`
}

// Judge classifies the pair (a, b) using only the given rules.
func Judge(a, b runner.Outcome, rules normalize.Rules) Result {
	res := Result{}

	// 1. Timeout anywhere => timeout. A timeout says nothing about the
	//    value the executor would have produced, so it can never be
	//    evidence of a value inequality.
	if a.Kind == runner.KindTimeout || b.Kind == runner.KindTimeout {
		res.Verdict = Timeout
		res.Sides = timeoutSides(a, b)
		res.Detail = "timeout is not a value inequality"
		return res
	}

	// 2. Run error anywhere => run_error. Crashes are infrastructure
	//    failures, not semantic differences.
	if a.Kind == runner.KindError || b.Kind == runner.KindError {
		res.Verdict = RunError
		res.Sides = errorSides(a, b)
		res.Detail = "run error is not a semantic difference"
		return res
	}

	// 3. Both produced values: normalize, then compare. Parse failure is
	//    reported separately from semantic difference.
	normA, errA := normalize.Normalize(a.Stdout, rules)
	normB, errB := normalize.Normalize(b.Stdout, rules)
	res.NormA = normA
	res.NormB = normB

	if errA != nil || errB != nil {
		res.Verdict = ParseError
		var sides, msgs []string
		if errA != nil {
			sides = append(sides, "A")
			msgs = append(msgs, "A: "+errA.Error())
		}
		if errB != nil {
			sides = append(sides, "B")
			msgs = append(msgs, "B: "+errB.Error())
		}
		res.Sides = sides
		res.Detail = strings.Join(msgs, "; ")
		return res
	}

	if normalize.Equal(normA, normB, rules) {
		res.Verdict = Equal
		return res
	}
	res.Verdict = SemanticDiff
	res.Detail = fmt.Sprintf("normalized values differ: A=%q B=%q",
		normA.Canonical, normB.Canonical)
	return res
}

func timeoutSides(a, b runner.Outcome) []string {
	var sides []string
	if a.Kind == runner.KindTimeout {
		sides = append(sides, "A")
	}
	if b.Kind == runner.KindTimeout {
		sides = append(sides, "B")
	}
	return sides
}

func errorSides(a, b runner.Outcome) []string {
	var sides []string
	if a.Kind == runner.KindError {
		sides = append(sides, "A")
	}
	if b.Kind == runner.KindError {
		sides = append(sides, "B")
	}
	return sides
}
