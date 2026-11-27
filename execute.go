package iac

import (
	"fmt"
	"sync"
)

// NodeStatus is the outcome of one graph node.
type NodeStatus string

const (
	StatusSucceeded NodeStatus = "succeeded"
	StatusFailed    NodeStatus = "failed"
	StatusSkipped   NodeStatus = "skipped"
)

// Executor performs one node operation. Returning an error fails the node.
type Executor func(node *Node) error

// NodeExecution records the outcome of a node.
type NodeExecution struct {
	NodeID  string     `json:"node_id"`
	Address string     `json:"address"`
	Action  ActionKind `json:"action"`
	Status  NodeStatus `json:"status"`
	Err     error      `json:"-"`
}

// ExecutionResult is a deterministic description of an execution run.
type ExecutionResult struct {
	Waves     [][]string        `json:"waves,omitempty"`
	Nodes     []NodeExecution   `json:"nodes,omitempty"`
	Succeeded []string          `json:"succeeded,omitempty"`
	Failed    []string          `json:"failed,omitempty"`
	Skipped   []string          `json:"skipped,omitempty"`
	BlockedBy map[string]string `json:"blocked_by,omitempty"`
	Errors    map[string]error  `json:"-"`
	Err       error             `json:"-"`
}

// Status returns the status for a node.
func (r *ExecutionResult) Status(nodeID string) (NodeStatus, bool) {
	if r == nil {
		return "", false
	}
	for _, n := range r.Nodes {
		if n.NodeID == nodeID {
			return n.Status, true
		}
	}
	return "", false
}

// SucceededNode reports whether nodeID completed successfully.
func (r *ExecutionResult) SucceededNode(nodeID string) bool {
	s, ok := r.Status(nodeID)
	return ok && s == StatusSucceeded
}

// Execute runs the graph wave by wave. All ready nodes in a wave run
// concurrently. A failed node only blocks its transitive dependents;
// independent branches in the same and later waves continue.
func (g *Graph) Execute(exec Executor) *ExecutionResult {
	result := &ExecutionResult{
		BlockedBy: make(map[string]string),
		Errors:    make(map[string]error),
	}
	waves, err := g.TopologicalWaves()
	if err != nil {
		result.Err = err
		result.Skipped = g.NodeIDs()
		return result
	}
	result.Waves = cloneWaves(waves)

	status := make(map[string]NodeStatus, len(g.Nodes))
	failErr := make(map[string]error)

	for _, wave := range waves {
		var ready []*Node
		for _, id := range wave {
			n := g.Node(id)
			if n == nil {
				continue
			}
			blocker := ""
			allComplete := true
			for _, pred := range g.Predecessors(id) {
				st := status[pred]
				if st == StatusFailed || st == StatusSkipped {
					if blocker == "" || pred < blocker {
						blocker = pred
					}
					allComplete = false
				} else if st != StatusSucceeded {
					allComplete = false
				}
			}
			if blocker != "" {
				status[id] = StatusSkipped
				result.BlockedBy[id] = blocker
				continue
			}
			if !allComplete {
				status[id] = StatusSkipped
				result.BlockedBy[id] = "unresolved dependency"
				continue
			}
			ready = append(ready, n)
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, n := range ready {
			wg.Add(1)
			go func(n *Node) {
				defer wg.Done()
				err := runNode(exec, n)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					status[n.ID] = StatusFailed
					failErr[n.ID] = err
					result.Errors[n.ID] = err
				} else {
					status[n.ID] = StatusSucceeded
				}
			}(n)
		}
		wg.Wait()
	}

	for _, id := range g.NodeIDs() {
		n := g.Node(id)
		st, ok := status[id]
		if !ok {
			st = StatusSkipped
			status[id] = st
			result.BlockedBy[id] = "unresolved dependency"
		}
		ne := NodeExecution{NodeID: id, Status: st}
		if n != nil {
			ne.Address = n.Address
			ne.Action = n.Action
		}
		if err := failErr[id]; err != nil {
			ne.Err = err
		}
		result.Nodes = append(result.Nodes, ne)
		switch st {
		case StatusSucceeded:
			result.Succeeded = append(result.Succeeded, id)
		case StatusFailed:
			result.Failed = append(result.Failed, id)
		case StatusSkipped:
			result.Skipped = append(result.Skipped, id)
		}
	}
	if len(result.BlockedBy) == 0 {
		result.BlockedBy = nil
	}
	return result
}

// ExecuteWithFailures is a deterministic convenience executor. Keys may be
// node IDs ("create:db") or resource addresses ("db"); a node ID takes
// precedence. A nil error counts as success.
func (g *Graph) ExecuteWithFailures(failures map[string]error) *ExecutionResult {
	return g.Execute(func(n *Node) error {
		if err, ok := failures[n.ID]; ok {
			return err
		}
		if err, ok := failures[n.Address]; ok {
			return err
		}
		return nil
	})
}

// Simulate is an alias for ExecuteWithFailures.
func (g *Graph) Simulate(failures map[string]error) *ExecutionResult {
	return g.ExecuteWithFailures(failures)
}

func runNode(exec Executor, n *Node) (err error) {
	if exec == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("executor panic at %s: %v", n.ID, r)
		}
	}()
	return exec(n)
}

func cloneWaves(in [][]string) [][]string {
	out := make([][]string, len(in))
	for i := range in {
		out[i] = append([]string(nil), in[i]...)
	}
	return out
}
