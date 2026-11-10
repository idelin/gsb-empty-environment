package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
)

// infBudget marks the unlimited budget held by root before any hop.
// Edge layer counts are assumed to fit below math.MaxInt64.
const infBudget = math.MaxInt64

// Edge is [grantor, grantee, start, end, remainingDelegationLayers].
// Its validity interval [start, end) is half-open.
type Edge struct {
	From   string
	To     string
	Start  int64
	End    int64
	Layers int64
}

func (e *Edge) UnmarshalJSON(data []byte) error {
	var raw [5]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("edge must be [from,to,start,end,layers] with integer ids: %w", err)
	}
	if raw[4] < 0 {
		return fmt.Errorf("edge delegation layers must be non-negative, got %d", raw[4])
	}
	if raw[2] > raw[3] {
		return fmt.Errorf("edge interval start (%d) must not be after end (%d)", raw[2], raw[3])
	}
	e.From = int64ID(raw[0])
	e.To = int64ID(raw[1])
	e.Start = raw[2]
	e.End = raw[3]
	e.Layers = raw[4]
	return nil
}

// Query is [timestamp, target].
type Query struct {
	At     int64
	Target string
}

func (q *Query) UnmarshalJSON(data []byte) error {
	var raw [2]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("query must be [time,target] with integer ids: %w", err)
	}
	q.At = raw[0]
	q.Target = int64ID(raw[1])
	return nil
}

type Input struct {
	Root    ID      `json:"root"`
	Edges   []Edge  `json:"edges"`
	Queries []Query `json:"queries"`
}

// ID accepts an integer JSON subject id, matching the integer ids in edges.
type ID string

func (id *ID) UnmarshalJSON(data []byte) error {
	var raw int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("id must be an integer: %w", err)
	}
	*id = ID(int64ID(raw))
	return nil
}

func int64ID(v int64) string {
	return strconv.FormatInt(v, 10)
}

// nextBudget applies the hop rule. A hop is only legal when budget > 0;
// after the hop the budget is min(previousBudget-1, edgeLayers).
func nextBudget(budget, layers int64) int64 {
	if budget == infBudget {
		return layers
	}
	if budget-1 < layers {
		return budget - 1
	}
	return layers
}

// minHops returns the fewest hops from root to target at time at, or -1.
// BFS states are (node, budget): budgets reached through different paths
// must never be merged, since only one path's budget can be carried onward.
func minHops(root, target string, adjacency map[string][]Edge, at int64) int {
	if root == target {
		return 0
	}

	type state struct {
		node   string
		budget int64
	}

	visited := map[state]int{{node: root, budget: infBudget}: 0}
	frontier := []state{{node: root, budget: infBudget}}
	distance := []int{0}

	for len(frontier) > 0 {
		current := frontier[0]
		frontier = frontier[1:]
		hops := distance[0]
		distance = distance[1:]

		if current.budget <= 0 {
			continue
		}
		for _, edge := range adjacency[current.node] {
			if at < edge.Start || at >= edge.End {
				continue
			}
			next := state{node: edge.To, budget: nextBudget(current.budget, edge.Layers)}
			if _, seen := visited[next]; seen {
				continue
			}
			nextHops := hops + 1
			if next.node == target {
				return nextHops
			}
			visited[next] = nextHops
			frontier = append(frontier, next)
			distance = append(distance, nextHops)
		}
	}
	return -1
}

func run(stdin io.Reader, stdout io.Writer) error {
	var input Input
	decoder := json.NewDecoder(stdin)
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}

	adjacency := make(map[string][]Edge, len(input.Edges))
	for _, edge := range input.Edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge)
	}

	answers := make([]int, len(input.Queries))
	for i, query := range input.Queries {
		answers[i] = minHops(string(input.Root), query.Target, adjacency, query.At)
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	if err := encoder.Encode(answers); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	_, err := stdout.Write(buffer.Bytes())
	return err
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
