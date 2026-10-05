package main

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Nat is a non-negative arbitrary precision integer. It accepts either a JSON
// number or a JSON string so that values beyond 64 bits round-trip exactly.
type Nat struct {
	v *big.Int
}

func (n *Nat) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(data) > 0 && data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return fmt.Errorf("invalid non-negative integer: %s", string(data))
		}
		s = str
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return fmt.Errorf("invalid non-negative integer: %q", s)
	}
	n.v = v
	return nil
}

func (n Nat) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.String())
}

func (n Nat) String() string {
	if n.v == nil {
		return "0"
	}
	return n.v.String()
}

// Int returns a copy of the value (nil-safe, zero by default).
func (n Nat) Int() *big.Int {
	if n.v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(n.v)
}

// FileInput describes one backup file (baseline or incremental).
type FileInput struct {
	ID           string   `json:"id"`
	Bytes        Nat      `json:"bytes"`
	Corrupted    bool     `json:"corrupted"`
	Dependencies []string `json:"dependencies"`
}

// EdgeInput is one shard coverage record: a directed edge fromVersion ->
// toVersion served by a file. Versions strictly increase along every edge.
type EdgeInput struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	File string `json:"file"`
}

// ShardInput describes one shard and its requested target version.
type ShardInput struct {
	Name   string      `json:"name"`
	Target int64       `json:"target"`
	Edges  []EdgeInput `json:"edges"`
}

// Input is the planner request. Budget (optional) limits node expansions;
// a negative or absent value means unlimited.
type Input struct {
	Files  []FileInput  `json:"files"`
	Shards []ShardInput `json:"shards"`
	Budget *int64       `json:"budget"`
}

// DiagError is a hard input error. Kinds:
//   duplicate_file_id, missing_id, dependency_cycle, invalid_edge, invalid_target
type DiagError struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Diagnostics reports non-fatal data quality findings.
type Diagnostics struct {
	CorruptedFiles        []string `json:"corruptedFiles"`
	CascadedUnusableFiles []string `json:"cascadedUnusableFiles"`
}

// SolutionJSON is the selected plan: sorted file IDs and total bytes.
type SolutionJSON struct {
	Files      []string `json:"files"`
	TotalBytes string   `json:"totalBytes"`
}

// Solve statuses.
const (
	StatusOptimal         = "optimal"
	StatusBudgetExhausted = "budget_exhausted"
	StatusInfeasible      = "infeasible"
	StatusError           = "error"
)

// SolveOutput is the planner response. LowerBound is always a valid lower
// bound on the optimum cost; when the budget is exhausted the status is
// budget_exhausted and the returned solution is NOT claimed to be optimal.
type SolveOutput struct {
	Status        string        `json:"status"`
	Errors        []DiagError   `json:"errors,omitempty"`
	Diagnostics   *Diagnostics  `json:"diagnostics,omitempty"`
	Solution      *SolutionJSON `json:"solution,omitempty"`
	LowerBound    string        `json:"lowerBound,omitempty"`
	NodesExpanded int64         `json:"nodesExpanded"`
	Budget        int64         `json:"budget"`
}
