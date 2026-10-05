package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

// Rat is a JSON-friendly exact rational: accepts 3, 1.5, "1.5", "3/2".
type Rat struct{ *big.Rat }

func (r Rat) MarshalJSON() ([]byte, error) {
	if r.Rat == nil {
		return []byte("0"), nil
	}
	if r.Denom().Cmp(big.NewInt(1)) == 0 {
		return []byte(r.Num().String()), nil
	}
	return json.Marshal(r.RatString())
}

func (r *Rat) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), "\"")
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational number: %s", s)
	}
	r.Rat = v
	return nil
}

type materialInput struct {
	Name        string `json:"name"`
	PackageSize *Rat   `json:"packageSize"`
	PackageCost *Rat   `json:"packageCost"`
}

type ruleInput struct {
	Name    string         `json:"name"`
	Inputs  map[string]Rat `json:"inputs"`
	Outputs map[string]Rat `json:"outputs"`
}

type problemInput struct {
	Materials []materialInput `json:"materials"`
	Demands   map[string]Rat  `json:"demands"`
	Rules     []ruleInput     `json:"rules"`
}

func toProblem(in problemInput) *Problem {
	p := &Problem{Demands: map[string]*big.Rat{}}
	for _, m := range in.Materials {
		mat := Material{Name: m.Name}
		if m.PackageSize != nil || m.PackageCost != nil {
			mat.Purchasable = true
			if m.PackageSize != nil {
				mat.PackageSize = m.PackageSize.Rat
			}
			if m.PackageCost != nil {
				mat.PackageCost = m.PackageCost.Rat
			}
		}
		p.Materials = append(p.Materials, mat)
	}
	for name, d := range in.Demands {
		p.Demands[name] = d.Rat
	}
	for i, r := range in.Rules {
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("rule-%d", i+1)
		}
		rule := Rule{
			Name:    name,
			Inputs:  map[string]*big.Rat{},
			Outputs: map[string]*big.Rat{},
		}
		for k, v := range r.Inputs {
			rule.Inputs[k] = v.Rat
		}
		for k, v := range r.Outputs {
			rule.Outputs[k] = v.Rat
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
}

type purchaseOut struct {
	Packages Rat `json:"packages"`
	Quantity Rat `json:"quantity"`
}

type materialOut struct {
	Purchased Rat `json:"purchased"`
	Produced  Rat `json:"produced"`
	Consumed  Rat `json:"consumed"`
	Demand    Rat `json:"demand"`
	Surplus   Rat `json:"surplus"`
}

type report struct {
	Status    string                 `json:"status"`
	Error     string                 `json:"error,omitempty"`
	Cycle     []string               `json:"cycleRules,omitempty"`
	TotalCost *Rat                   `json:"totalCost,omitempty"`
	Purchases map[string]purchaseOut `json:"purchases,omitempty"`
	RuleUsage map[string]Rat         `json:"ruleUsage,omitempty"`
	Materials map[string]materialOut `json:"materials,omitempty"`
}

func emit(w io.Writer, r report) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(r)
}

func main() {
	var src io.Reader = os.Stdin
	if len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "usage: %s [problem.json]  (reads stdin when no file given)\n", os.Args[0])
		os.Exit(64)
	}
	if len(os.Args) == 2 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot open %s: %v\n", os.Args[1], err)
			os.Exit(66)
		}
		defer f.Close()
		src = f
	}
	var in problemInput
	if err := json.NewDecoder(src).Decode(&in); err != nil {
		emit(os.Stdout, report{Status: "error", Error: fmt.Sprintf("invalid JSON: %v", err)})
		os.Exit(65)
	}
	plan, cycle, feasible, err := SolveProblem(toProblem(in))
	switch {
	case err != nil:
		emit(os.Stdout, report{Status: "error", Error: err.Error()})
		os.Exit(65)
	case cycle != nil:
		emit(os.Stdout, report{
			Status: "rejected",
			Error:  "productive substitution cycle detected: rules can create available quantity out of nothing",
			Cycle:  cycle,
		})
		os.Exit(2)
	case !feasible:
		emit(os.Stdout, report{Status: "infeasible", Error: "demands cannot be met with the given materials and rules"})
		os.Exit(1)
	}
	out := report{
		Status:    "optimal",
		TotalCost: &Rat{plan.TotalCost},
		Purchases: map[string]purchaseOut{},
		RuleUsage: map[string]Rat{},
		Materials: map[string]materialOut{},
	}
	for _, pu := range plan.Purchases {
		out.Purchases[pu.Material] = purchaseOut{Packages: Rat{pu.Packages}, Quantity: Rat{pu.Quantity}}
	}
	for _, ru := range plan.RuleUsage {
		out.RuleUsage[ru.Rule] = Rat{ru.Amount}
	}
	for _, mp := range plan.Materials {
		out.Materials[mp.Name] = materialOut{
			Purchased: Rat{mp.Purchased},
			Produced:  Rat{mp.Produced},
			Consumed:  Rat{mp.Consumed},
			Demand:    Rat{mp.Demand},
			Surplus:   Rat{mp.Surplus},
		}
	}
	emit(os.Stdout, out)
}
