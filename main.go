package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// exampleJSON is the built-in demo scenario, kept identical to
// example.json (a test guards the sync).
const exampleJSON = `{
  "versions": [
    {"id": "v1", "features": ["core"]},
    {"id": "v2", "features": ["core", "audit-log"]},
    {"id": "v3", "features": ["core", "audit-log", "metrics"]},
    {"id": "v4", "features": ["core", "audit-log", "metrics", "tracing"]},
    {"id": "v5", "features": ["core", "audit-log", "metrics", "tracing", "exp-cache"]}
  ],
  "edges": [
    {"from": "v2", "to": "v1", "cost": 1, "undoes": ["audit-log"]},
    {"from": "v3", "to": "v2", "cost": 1, "undoes": ["metrics"]},
    {"from": "v4", "to": "v3", "cost": 1, "undoes": ["tracing"]},
    {"from": "v5", "to": "v4", "cost": 2, "undoes": ["exp-cache"]},
    {"from": "v5", "to": "v3", "cost": 4, "undoes": ["tracing", "exp-cache"]},
    {"from": "v4", "to": "v2", "cost": 2, "undoes": ["metrics", "tracing"]}
  ],
  "nodes": [
    {"id": "A", "version": "v5"},
    {"id": "B", "version": "v4", "forbidden": ["v2"]},
    {"id": "C", "version": "v3"}
  ],
  "budget": 6,
  "compatibility": [
    {
      "nodeA": "A",
      "nodeB": "B",
      "allowedPairs": [
        ["v5", "v4"], ["v5", "v3"], ["v5", "v2"],
        ["v4", "v4"], ["v4", "v2"],
        ["v3", "v4"], ["v3", "v3"], ["v3", "v2"]
      ]
    }
  ]
}
`

func loadScenario(path string) (*Scenario, error) {
	data := []byte(exampleJSON)
	if path != "" {
		b, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data = b
	}
	var sc Scenario
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse scenario: %v", err)
	}
	return &sc, nil
}

func runPart1(g *Graph) (*Plan, error) {
	plan, err := SelectCommonTarget(g)
	if err != nil {
		return nil, err
	}
	fmt.Println("=== Part 1: common rollback target ===")
	fmt.Printf("Target version: %s\n", plan.Target)
	fmt.Printf("Retained features (%d): %s\n", len(plan.RetainedFeatures), strings.Join(plan.RetainedFeatures, ", "))
	fmt.Printf("Total rollback steps: %d (total budget cost: %d)\n", plan.TotalSteps, plan.TotalCost)
	fmt.Println("Node paths:")
	for _, np := range plan.NodePlans {
		if np.Steps == 0 {
			fmt.Printf("  %s: %s (already at target)\n", np.NodeID, np.Path[0])
		} else {
			fmt.Printf("  %s: %s (%d step(s), cost %d)\n", np.NodeID, strings.Join(np.Path, " -> "), np.Steps, np.Cost)
		}
	}
	if err := VerifyPlan(g, plan); err != nil {
		return nil, fmt.Errorf("independent verification failed: %v", err)
	}
	fmt.Println("Independent verification: OK (reachability, path validity, feature set, optimality)")
	fmt.Println()
	return plan, nil
}

func runPart2(g *Graph, target string) error {
	traj, err := SearchTrajectory(g, target)
	if err != nil {
		return err
	}
	fmt.Printf("=== Part 2: constrained transition trajectory (target %s) ===\n", target)
	fmt.Printf("Shared budget: %d\n", g.scenario.Budget)
	fmt.Printf("Initial state: %s\n", strings.Join(traj.Initial, " "))
	cum := 0
	for k, mv := range traj.Moves {
		cum += mv.Cost
		fmt.Printf("Step %d: %s %s -> %s (cost %d, cumulative %d/%d)  state=[%s]\n",
			k+1, mv.NodeID, mv.From, mv.To, mv.Cost, cum, g.scenario.Budget,
			strings.Join(traj.States[k+1], " "))
	}
	fmt.Printf("Goal state: %s (%d transition(s), total cost %d)\n",
		strings.Join(traj.Goal, " "), len(traj.Moves), traj.TotalCost)
	if err := VerifyTrajectory(g, traj, target); err != nil {
		return fmt.Errorf("trajectory verification failed: %v", err)
	}
	fmt.Println("Trajectory verification: OK (edges, forbidden versions, compatibility, budget)")
	return nil
}

func main() {
	input := flag.String("input", "", "scenario JSON file (default: built-in example)")
	part := flag.String("part", "all", "which part to run: 1, 2, or all")
	flag.Parse()
	sc, err := loadScenario(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	g, err := BuildGraph(sc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid scenario:", err)
		os.Exit(1)
	}
	target := ""
	if *part == "1" || *part == "all" {
		plan, err := runPart1(g)
		if err != nil {
			fmt.Fprintln(os.Stderr, "part 1:", err)
			os.Exit(1)
		}
		target = plan.Target
	}
	if *part == "2" || *part == "all" {
		if target == "" {
			plan, err := SelectCommonTarget(g)
			if err != nil {
				fmt.Fprintln(os.Stderr, "part 2: cannot determine target:", err)
				os.Exit(1)
			}
			target = plan.Target
		}
		if err := runPart2(g, target); err != nil {
			fmt.Fprintln(os.Stderr, "part 2:", err)
			os.Exit(1)
		}
	}
}
