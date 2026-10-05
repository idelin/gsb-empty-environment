package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"sort"
	"strings"
)

// ---------- input ----------

type ratJSON struct{ r *big.Rat }

func (rj *ratJSON) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), "\"")
	if s == "" {
		s = "0"
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational %q", s)
	}
	rj.r = r
	return nil
}

type inConstraint struct {
	LHS map[string]ratJSON `json:"lhs"`
	Op  string             `json:"op"`
	RHS ratJSON            `json:"rhs"`
}

type inSystem struct {
	Variables   []string       `json:"variables"`
	Constraints []inConstraint `json:"constraints"`
}

// ---------- output ----------

type outConstraint struct {
	Coeffs map[string]string `json:"coeffs"`
	Const  string            `json:"const"`
	Op     string            `json:"op"`
	Source []int             `json:"source"`
}

type outRemoved struct {
	Constraint *outConstraint `json:"constraint,omitempty"`
	Reason     string         `json:"reason"`
}

type outStats struct {
	Ops    int64 `json:"ops"`
	Rounds int   `json:"rounds"`
}

type outResult struct {
	Status               string          `json:"status"`
	EquivalentProjection bool            `json:"equivalent_projection"`
	Feasible             bool            `json:"feasible"`
	KeptVariables        []string        `json:"kept_variables"`
	EliminatedVariables  []string        `json:"eliminated_variables"`
	UneliminatedVars     []string        `json:"uneliminated_variables,omitempty"`
	Constraints          []outConstraint `json:"constraints"`
	Removed              []outRemoved    `json:"removed_redundant,omitempty"`
	Stats                outStats        `json:"stats"`
	Verification         *VerifyReport   `json:"verification,omitempty"`
	Note                 string          `json:"note,omitempty"`
}

func parseOp(op string) (Kind, bool, error) {
	switch op {
	case "<=":
		return Le, false, nil
	case "<":
		return Lt, false, nil
	case ">=":
		return Le, true, nil
	case ">":
		return Lt, true, nil
	case "=", "==":
		return Eq, false, nil
	}
	return Le, false, fmt.Errorf("unknown op %q", op)
}

func buildSystem(in *inSystem) ([]*Constraint, error) {
	varIndex := map[string]int{}
	for i, v := range in.Variables {
		varIndex[v] = i
	}
	n := len(in.Variables)
	sys := make([]*Constraint, 0, len(in.Constraints))
	for i, ic := range in.Constraints {
		kind, negate, err := parseOp(ic.Op)
		if err != nil {
			return nil, fmt.Errorf("constraint %d: %v", i, err)
		}
		c := newConstraint(n, kind, []int{i})
		for name, coeff := range ic.LHS {
			j, ok := varIndex[name]
			if !ok {
				return nil, fmt.Errorf("constraint %d: unknown variable %q", i, name)
			}
			c.Coeff[j] = cloneRat(coeff.r)
		}
		// lhs op rhs  ==>  lhs - rhs (op) 0
		c.Const = new(big.Rat).Neg(ic.RHS.r)
		if negate {
			for j := range c.Coeff {
				c.Coeff[j].Neg(c.Coeff[j])
			}
			c.Const.Neg(c.Const)
		}
		sys = append(sys, c)
	}
	return sys, nil
}

func toOutConstraint(c *Constraint, names []string) outConstraint {
	oc := outConstraint{
		Coeffs: map[string]string{},
		Const:  c.Const.RatString(),
		Op:     c.Kind.String(),
		Source: append([]int(nil), c.Source...),
	}
	for j, r := range c.Coeff {
		if r.Sign() != 0 {
			oc.Coeffs[names[j]] = r.RatString()
		}
	}
	return oc
}

func main() {
	inPath := flag.String("in", "", "input JSON file (default: stdin)")
	elimFlag := flag.String("eliminate", "", "comma-separated variables to eliminate")
	budget := flag.Int("budget", 10000, "max constraints in the working set")
	maxOps := flag.Int64("maxops", 1000000, "max combination operations")
	prune := flag.Bool("prune", true, "remove redundant constraints (evidence retained)")
	verify := flag.Bool("verify", true, "cross-check the projection with an independent solver")
	outPath := flag.String("out", "", "output JSON file (default: stdout)")
	flag.Parse()

	var data []byte
	var err error
	if *inPath == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*inPath)
	}
	if err != nil {
		fatal(err)
	}
	var in inSystem
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("parsing input: %v", err))
	}
	sys, err := buildSystem(&in)
	if err != nil {
		fatal(err)
	}
	varIndex := map[string]int{}
	for i, v := range in.Variables {
		varIndex[v] = i
	}
	var elim []int
	if *elimFlag != "" {
		for _, name := range strings.Split(*elimFlag, ",") {
			name = strings.TrimSpace(name)
			idx, ok := varIndex[name]
			if !ok {
				fatal(fmt.Errorf("unknown variable to eliminate: %q", name))
			}
			elim = append(elim, idx)
		}
	}
	sort.Ints(elim)

	eng := &Engine{
		NumVars: len(in.Variables),
		Budget:  Budget{MaxConstraints: *budget, MaxOps: *maxOps},
		Prune:   *prune,
	}
	res := eng.Eliminate(sys, elim)

	elimSet := map[int]bool{}
	for _, v := range elim {
		elimSet[v] = true
	}
	var kept []int
	var keptNames, elimNames []string
	for i, name := range in.Variables {
		if elimSet[i] {
			elimNames = append(elimNames, name)
		} else {
			kept = append(kept, i)
			keptNames = append(keptNames, name)
		}
	}

	out := outResult{
		Status:               string(res.Status),
		EquivalentProjection: res.Status == StatusOK,
		Feasible:             res.Feasible,
		KeptVariables:        keptNames,
		EliminatedVariables:  elimNames,
		Stats:                outStats{Ops: res.Ops, Rounds: res.Rounds},
	}
	if res.Status == StatusBudgetExceeded {
		out.Note = "budget exhausted: constraints below are an exact intermediate " +
			"system, NOT a completed projection; no equivalence is claimed"
		for _, v := range res.Uneliminated {
			out.UneliminatedVars = append(out.UneliminatedVars, in.Variables[v])
		}
	}
	for _, c := range res.Constraints {
		out.Constraints = append(out.Constraints, toOutConstraint(c, in.Variables))
	}
	if out.Constraints == nil {
		out.Constraints = []outConstraint{}
	}
	for _, rm := range res.Removed {
		or := outRemoved{Reason: rm.Reason}
		if rm.C != nil {
			oc := toOutConstraint(rm.C, in.Variables)
			or.Constraint = &oc
		}
		out.Removed = append(out.Removed, or)
	}

	if *verify && res.Status == StatusOK {
		out.Verification = Verify(sys, len(in.Variables), kept, res.Constraints, res.Feasible)
	}

	var buf bytes.Buffer
	je := json.NewEncoder(&buf)
	je.SetEscapeHTML(false)
	je.SetIndent("", "  ")
	if err := je.Encode(out); err != nil {
		fatal(err)
	}
	if *outPath == "" {
		fmt.Print(buf.String())
	} else if err := ioutil.WriteFile(*outPath, buf.Bytes(), 0644); err != nil {
		fatal(err)
	}
	if res.Status != StatusOK {
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fmproj:", err)
	os.Exit(1)
}
