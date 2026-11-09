package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Subject identifies a granting/receiving party. JSON strings and JSON
// numbers are both accepted as identifiers.
type Subject string

func (s *Subject) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty subject")
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return err
		}
		*s = Subject(str)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(trimmed, &num); err == nil {
		*s = Subject(num.String())
		return nil
	}
	*s = Subject(string(trimmed))
	return nil
}

// Edge is [grantor, receiver, start, end, remaining delegation layers].
// The validity interval is left-closed and right-open: [start, end).
type Edge struct {
	From   Subject
	To     Subject
	Start  int64
	End    int64
	Layers int
}

func (e *Edge) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 5 {
		return fmt.Errorf("edge must have 5 fields, got %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &e.From); err != nil {
		return fmt.Errorf("edge grantor: %w", err)
	}
	if err := json.Unmarshal(raw[1], &e.To); err != nil {
		return fmt.Errorf("edge receiver: %w", err)
	}
	if err := json.Unmarshal(raw[2], &e.Start); err != nil {
		return fmt.Errorf("edge start: %w", err)
	}
	if err := json.Unmarshal(raw[3], &e.End); err != nil {
		return fmt.Errorf("edge end: %w", err)
	}
	if err := json.Unmarshal(raw[4], &e.Layers); err != nil {
		return fmt.Errorf("edge layers: %w", err)
	}
	return nil
}

// Query is [time, target].
type Query struct {
	At  int64
	Who Subject
}

func (q *Query) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 2 {
		return fmt.Errorf("query must have 2 fields, got %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &q.At); err != nil {
		return fmt.Errorf("query time: %w", err)
	}
	if err := json.Unmarshal(raw[1], &q.Who); err != nil {
		return fmt.Errorf("query target: %w", err)
	}
	return nil
}

type inputPayload struct {
	Root    Subject `json:"root"`
	Edges   []Edge  `json:"edges"`
	Queries []Query `json:"queries"`
}

// rootBudget marks the root state, whose budget is unlimited.
const rootBudget = -1

type bfsState struct {
	node   Subject
	budget int
}

func solve(in inputPayload) []int {
	adj := make(map[Subject][]Edge)
	for _, edge := range in.Edges {
		adj[edge.From] = append(adj[edge.From], edge)
	}

	results := make([]int, len(in.Queries))
	for i, query := range in.Queries {
		results[i] = minHops(in.Root, adj, query)
	}
	return results
}

// minHops returns the fewest edges forming a delegation chain from root to
// query.Who where every edge is valid at query.At, or -1 if none exists.
func minHops(root Subject, adj map[Subject][]Edge, query Query) int {
	if query.Who == root {
		return 0
	}

	visited := map[bfsState]bool{{node: root, budget: rootBudget}: true}
	queue := []bfsState{{node: root, budget: rootBudget}}
	hops := 0

	for len(queue) > 0 {
		level := len(queue)
		for _, current := range queue[:level] {
			for _, edge := range adj[current.node] {
				if query.At < edge.Start || query.At >= edge.End {
					continue
				}
				if current.budget != rootBudget && current.budget <= 0 {
					continue
				}

				nextBudget := edge.Layers
				if current.budget != rootBudget {
					nextBudget = min(current.budget-1, edge.Layers)
				}

				if edge.To == query.Who {
					return hops + 1
				}

				next := bfsState{node: edge.To, budget: nextBudget}
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		queue = queue[level:]
		hops++
	}

	return -1
}
