package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
)

// Input is the JSON request. Either give an explicit interval
// (min_new/max_new) or a target ratio with a tolerance; the explicit
// interval wins when both are present.
type Input struct {
	Groups    []Group  `json:"groups"`
	MinNew    *int64   `json:"min_new"`
	MaxNew    *int64   `json:"max_new"`
	Ratio     *float64 `json:"ratio"`     // target share of users on the new version
	Tolerance *float64 `json:"tolerance"` // allowed absolute deviation from ratio
}

// GroupPlan is the per-group decision in the output.
type GroupPlan struct {
	ID      string `json:"id"`
	Size    int64  `json:"size"`
	OnNew   bool   `json:"on_new"`
	Changed bool   `json:"changed"`
}

// Output is the JSON response.
type Output struct {
	Feasible    bool        `json:"feasible"`
	TotalUsers  int64       `json:"total_users"`
	Interval    [2]int64    `json:"interval"`
	NewUsers    int64       `json:"new_users,omitempty"`
	Ratio       float64     `json:"ratio,omitempty"`
	MovedUsers  int64       `json:"moved_users,omitempty"`
	MovedGroups int         `json:"moved_groups,omitempty"`
	Plan        []GroupPlan `json:"plan,omitempty"`
	Proof       string      `json:"proof,omitempty"`
}

func interval(in Input, total int64) (int64, int64, error) {
	if in.MinNew != nil && in.MaxNew != nil {
		return *in.MinNew, *in.MaxNew, nil
	}
	if in.Ratio == nil {
		return 0, 0, fmt.Errorf("provide either min_new/max_new or ratio")
	}
	tol := 0.0
	if in.Tolerance != nil {
		tol = *in.Tolerance
	}
	if *in.Ratio < 0 || *in.Ratio > 1 || tol < 0 || tol >= 1 {
		return 0, 0, fmt.Errorf("need 0 <= ratio <= 1 and 0 <= tolerance < 1")
	}
	// Round outward so the integer interval never overstates the ratio band.
	lo := int64(math.Ceil((*in.Ratio - tol) * float64(total)))
	hi := int64(math.Floor((*in.Ratio + tol) * float64(total)))
	return lo, hi, nil
}

func run(data []byte) (Output, error) {
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return Output{}, fmt.Errorf("invalid JSON: %v", err)
	}
	var total int64
	for _, g := range in.Groups {
		total += g.Size
	}
	lo, hi, err := interval(in, total)
	if err != nil {
		return Output{}, err
	}
	out := Output{TotalUsers: total, Interval: [2]int64{lo, hi}}

	asg, proof, err := Solve(in.Groups, lo, hi)
	if err != nil {
		return Output{}, err
	}
	if proof != nil {
		out.Proof = proof.Describe()
		return out, nil
	}
	out.Feasible = true
	out.NewUsers = asg.NewUsers
	out.Ratio = float64(asg.NewUsers) / float64(total)
	out.MovedUsers = asg.MovedUsers
	out.MovedGroups = asg.MovedGroups
	for i, g := range in.Groups {
		out.Plan = append(out.Plan, GroupPlan{
			ID:      g.ID,
			Size:    g.Size,
			OnNew:   asg.OnNew[i],
			Changed: asg.OnNew[i] != g.OnNew,
		})
	}
	return out, nil
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "usage: %s [input.json] (reads stdin when no file is given)\n", os.Args[0])
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(2)
	}
	out, err := run(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode output:", err)
		os.Exit(2)
	}
	if !out.Feasible {
		os.Exit(1)
	}
}
