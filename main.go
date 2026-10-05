package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
)

type InputSpec struct {
	Vars        []string          `json:"vars"`
	Eliminate   []string          `json:"eliminate"`
	Budget      int               `json:"budget"`
	Samples     int               `json:"samples"`
	LPReduce    bool              `json:"lp_reduce"`
	Constraints []InputConstraint `json:"constraints"`
}

type InputConstraint struct {
	ID     string            `json:"id"`
	Coeffs map[string]string `json:"coeffs"`
	Op     string            `json:"op"`
	RHS    string            `json:"rhs"`
}

type OutputProvTerm struct {
	ID   string `json:"id"`
	Mult string `json:"mult"`
}

type OutputConstraint struct {
	Coeffs     map[string]string  `json:"coeffs"`
	Op         string             `json:"op"`
	RHS        string             `json:"rhs"`
	Provenance [][]OutputProvTerm `json:"provenance"`
}

type OutputRemoved struct {
	Constraint OutputConstraint `json:"constraint"`
	Reason     string           `json:"reason"`
}

type Report struct {
	Status        string             `json:"status"`
	Detail        string             `json:"detail,omitempty"`
	RemainingVars []string           `json:"remaining_vars,omitempty"`
	Constraints   []OutputConstraint `json:"constraints,omitempty"`
	Removed       []OutputRemoved    `json:"removed,omitempty"`
	Contradiction *OutputConstraint  `json:"contradiction,omitempty"`
	Verification  *VerifyReport      `json:"verification,omitempty"`
}

const (
	exitOK = iota
	exitUsage
	exitBudget
	exitVerifyFailed
)

func main() {
	budgetFlag := flag.Int("budget", 0, "maximum Fourier-Motzkin combinations (overrides spec)")
	samplesFlag := flag.Int("samples", 0, "maximum verification sample points (overrides spec)")
	lpReduceFlag := flag.Bool("lp-reduce", false, "enable LP-based redundancy removal")
	flag.Parse()
	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = os.ReadFile(flag.Arg(0))
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	var spec InputSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		fatal(fmt.Errorf("invalid input JSON: %w", err))
	}
	budget := spec.Budget
	if *budgetFlag > 0 {
		budget = *budgetFlag
	}
	if budget <= 0 {
		budget = 10000
	}
	samples := spec.Samples
	if *samplesFlag > 0 {
		samples = *samplesFlag
	}
	if samples <= 0 {
		samples = 500
	}
	report, code, err := run(&spec, budget, samples, spec.LPReduce || *lpReduceFlag)
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(report); err != nil {
		fatal(err)
	}
	os.Exit(code)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fmproj:", err)
	os.Exit(exitUsage)
}

func run(spec *InputSpec, budget, samples int, lpReduceOn bool) (*Report, int, error) {
	if len(spec.Vars) == 0 {
		return nil, 0, fmt.Errorf("no variables declared")
	}
	varIndex := map[string]int{}
	for i, v := range spec.Vars {
		if _, dup := varIndex[v]; dup {
			return nil, 0, fmt.Errorf("duplicate variable %q", v)
		}
		varIndex[v] = i
	}
	nvars := len(spec.Vars)
	ineqs, err := parseConstraints(spec, varIndex, nvars)
	if err != nil {
		return nil, 0, err
	}
	elimSet := map[int]bool{}
	var elimIdx []int
	for _, name := range spec.Eliminate {
		idx, ok := varIndex[name]
		if !ok {
			return nil, 0, fmt.Errorf("eliminate: unknown variable %q", name)
		}
		if !elimSet[idx] {
			elimSet[idx] = true
			elimIdx = append(elimIdx, idx)
		}
	}
	var remIdx []int
	var remNames []string
	for i, v := range spec.Vars {
		if !elimSet[i] {
			remIdx = append(remIdx, i)
			remNames = append(remNames, v)
		}
	}
	res, err := eliminateAll(ineqs, elimIdx, budget)
	if err != nil {
		if be, ok := err.(*BudgetExceededError); ok {
			return &Report{
				Status: "budget_exceeded",
				Detail: be.Error() + "; no partial projection is returned because it would not be equivalent to the input system",
			}, exitBudget, nil
		}
		return nil, 0, err
	}
	rep := &Report{RemainingVars: remNames}
	if res.Contradiction != nil {
		rep.Status = "infeasible"
		c := toOutput(res.Contradiction, spec.Vars)
		rep.Contradiction = &c
	} else {
		rep.Status = "ok"
		kept := res.Kept
		if lpReduceOn {
			var lpRemoved []RemovedIneq
			kept, lpRemoved = lpReduce(kept, 200)
			res.Removed = append(res.Removed, lpRemoved...)
		}
		for _, in := range kept {
			rep.Constraints = append(rep.Constraints, toOutput(in, spec.Vars))
		}
	}
	for _, r := range res.Removed {
		rep.Removed = append(rep.Removed, OutputRemoved{
			Constraint: toOutput(r.Ineq, spec.Vars),
			Reason:     r.Reason,
		})
	}
	var projForVerify []*Ineq
	if res.Contradiction == nil {
		projForVerify = constraintsFromReport(rep, nvars, varIndex)
	}
	rep.Verification = verify(ineqs, nvars, projForVerify, remIdx, res.Contradiction != nil, samples, remNames)
	code := exitOK
	if !rep.Verification.FeasibilityAgree || len(rep.Verification.Mismatches) > 0 {
		rep.Status = "verification_failed"
		rep.Detail = "independent feasibility check disagrees with the projection; the result must not be trusted"
		code = exitVerifyFailed
	}
	return rep, code, nil
}

func constraintsFromReport(rep *Report, nvars int, varIndex map[string]int) []*Ineq {
	var out []*Ineq
	for _, oc := range rep.Constraints {
		coeffs := zeroVector(nvars)
		for name, s := range oc.Coeffs {
			r, _ := new(big.Rat).SetString(s)
			coeffs[varIndex[name]] = r
		}
		rhs, _ := new(big.Rat).SetString(oc.RHS)
		out = append(out, &Ineq{Coeffs: coeffs, RHS: rhs, Strict: oc.Op == "<"})
	}
	return out
}

func parseConstraints(spec *InputSpec, varIndex map[string]int, nvars int) ([]*Ineq, error) {
	var out []*Ineq
	for _, ic := range spec.Constraints {
		if ic.ID == "" {
			return nil, fmt.Errorf("constraint missing id")
		}
		rhs, ok := new(big.Rat).SetString(ic.RHS)
		if !ok {
			return nil, fmt.Errorf("constraint %s: invalid rhs %q", ic.ID, ic.RHS)
		}
		base := zeroVector(nvars)
		for v, s := range ic.Coeffs {
			idx, ok := varIndex[v]
			if !ok {
				return nil, fmt.Errorf("constraint %s: unknown variable %q", ic.ID, v)
			}
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				return nil, fmt.Errorf("constraint %s: invalid coefficient %q", ic.ID, s)
			}
			base[idx] = r
		}
		switch ic.Op {
		case "<=":
			out = append(out, newIneq(base, rhs, false, ic.ID))
		case "<":
			out = append(out, newIneq(base, rhs, true, ic.ID))
		case ">=":
			out = append(out, newIneq(negCoeffs(base), new(big.Rat).Neg(rhs), false, ic.ID))
		case ">":
			out = append(out, newIneq(negCoeffs(base), new(big.Rat).Neg(rhs), true, ic.ID))
		case "==", "=":
			out = append(out, newIneq(base, rhs, false, ic.ID+"_le"))
			out = append(out, newIneq(negCoeffs(base), new(big.Rat).Neg(rhs), false, ic.ID+"_ge"))
		default:
			return nil, fmt.Errorf("constraint %s: unsupported op %q", ic.ID, ic.Op)
		}
	}
	return out, nil
}

func toOutput(in *Ineq, vars []string) OutputConstraint {
	coeffs := map[string]string{}
	for i, c := range in.Coeffs {
		if c.Sign() != 0 {
			coeffs[vars[i]] = c.RatString()
		}
	}
	prov := make([][]OutputProvTerm, len(in.Prov))
	for i, d := range in.Prov {
		for _, t := range d {
			prov[i] = append(prov[i], OutputProvTerm{ID: t.ID, Mult: t.Mult.RatString()})
		}
	}
	return OutputConstraint{
		Coeffs:     coeffs,
		Op:         in.opString(),
		RHS:        in.RHS.RatString(),
		Provenance: prov,
	}
}
