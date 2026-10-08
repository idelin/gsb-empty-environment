// Command facilitycover solves tree-constrained facility coverage instances.
//
// Usage:
//
//	facilitycover [-mode auto|det|robust] instance.json
//
// The JSON instance has the form:
//
//	{
//	  "budget": 10,
//	  "facilities": [{"id": 0, "parent": -1, "cost": 3}, ...],
//	  "demands": [5, 7, ...],                 // revenue per demand point
//	  "coverage": {"0": [0, 1], ...},         // facility ID -> covered demands
//	  "emergencyLimit": 1,                     // robust mode only
//	  "scenarios": [{"group": 0, "probability": 0.5,
//	                 "coverage": {"0": [0], ...}}, ...]
//	}
//
// With scenarios present (or -mode robust) the two-stage robust problem is
// solved; otherwise the deterministic problem is solved. Every solution is
// verified before printing; verification failure exits non-zero.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"facilitycover"
)

type jsonFacility struct {
	ID     int   `json:"id"`
	Parent int   `json:"parent"`
	Cost   int64 `json:"cost"`
}

type jsonScenario struct {
	Group       int              `json:"group"`
	Probability float64          `json:"probability"`
	Coverage    map[string][]int `json:"coverage"`
}

type jsonInstance struct {
	Budget         int64            `json:"budget"`
	Facilities     []jsonFacility   `json:"facilities"`
	Demands        []int64          `json:"demands"`
	Coverage       map[string][]int `json:"coverage"`
	EmergencyLimit int              `json:"emergencyLimit"`
	Scenarios      []jsonScenario   `json:"scenarios"`
}

func convCoverage(m map[string][]int) (map[int][]int, error) {
	out := make(map[int][]int, len(m))
	for k, v := range m {
		var id int
		if _, err := fmt.Sscanf(k, "%d", &id); err != nil {
			return nil, fmt.Errorf("bad facility ID key %q: %v", k, err)
		}
		out[id] = v
	}
	return out, nil
}

func main() {
	mode := flag.String("mode", "auto", "auto|det|robust")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: facilitycover [-mode auto|det|robust] instance.json")
		os.Exit(2)
	}
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var ji jsonInstance
	if err := json.Unmarshal(data, &ji); err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid JSON:", err)
		os.Exit(1)
	}
	inst := facilitycover.Instance{
		Budget:        ji.Budget,
		DemandRevenue: ji.Demands,
	}
	for _, f := range ji.Facilities {
		inst.Facilities = append(inst.Facilities, facilitycover.Facility{
			ID: f.ID, Parent: f.Parent, Cost: f.Cost,
		})
	}
	if inst.Coverage, err = convCoverage(ji.Coverage); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	robust := *mode == "robust" || (*mode == "auto" && len(ji.Scenarios) > 0)
	if robust {
		ri := &facilitycover.RobustInstance{Instance: inst, EmergencyLimit: ji.EmergencyLimit}
		for _, sc := range ji.Scenarios {
			cov, err := convCoverage(sc.Coverage)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			ri.Scenarios = append(ri.Scenarios, facilitycover.Scenario{
				Group: sc.Group, Probability: sc.Probability, Coverage: cov,
			})
		}
		res, err := facilitycover.SolveRobust(ri)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if err := facilitycover.VerifyRobust(ri, res); err != nil {
			fmt.Fprintln(os.Stderr, "VERIFICATION FAILED:", err)
			os.Exit(1)
		}
		fmt.Print(res.PolicyTree())
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		fmt.Println("verification: OK (per-scenario budget checked)")
		return
	}

	res, err := facilitycover.SolveDeterministic(&inst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := facilitycover.VerifyDeterministic(&inst, res); err != nil {
		fmt.Fprintln(os.Stderr, "VERIFICATION FAILED:", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	fmt.Println("verification: OK (subset enumeration, no double counting)")
}
