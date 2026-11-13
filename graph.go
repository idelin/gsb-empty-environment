package netsec

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// ScriptDeclaration is the complete set of actions one build script declares.
type ScriptDeclaration struct {
	Name    string   `json:"name"`
	Actions []Action `json:"actions,omitempty"`
}

// PackageNode is one node in the package graph.
type PackageNode struct {
	Digest    Digest              `json:"digest"`
	DependsOn []Digest            `json:"dependsOn,omitempty"`
	Scripts   []ScriptDeclaration `json:"scripts,omitempty"`
}

// PackageGraph is the untrusted input supplied to a planner.
type PackageGraph struct {
	Nodes []PackageNode `json:"nodes"`
}

// Convenience aliases for callers that prefer shorter names.
type Package = PackageNode
type Script = ScriptDeclaration
type Graph = PackageGraph

// NewPackageGraph is a small builder helper.
func NewPackageGraph(nodes ...PackageNode) PackageGraph {
	return PackageGraph{Nodes: nodes}
}

func validateScriptName(name string) error {
	if name == "" {
		return fmt.Errorf("empty script name")
	}
	if len(name) > 256 {
		return fmt.Errorf("script name is too long")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("script name contains whitespace or control characters")
		}
	}
	return nil
}

func normalizeGraph(graph PackageGraph) (map[Digest]PackageNode, error) {
	nodes := make(map[Digest]PackageNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if err := node.Digest.Validate(); err != nil {
			return nil, &GraphError{Code: "invalid-digest", Package: node.Digest, Message: err.Error()}
		}
		if _, exists := nodes[node.Digest]; exists {
			return nil, &GraphError{Code: "duplicate-package", Package: node.Digest, Message: "package digest appears more than once"}
		}
		scriptNames := make(map[string]struct{}, len(node.Scripts))
		for _, script := range node.Scripts {
			if err := validateScriptName(script.Name); err != nil {
				return nil, &GraphError{Code: "invalid-script", Package: node.Digest, Message: err.Error()}
			}
			if _, exists := scriptNames[script.Name]; exists {
				return nil, &GraphError{Code: "duplicate-script", Package: node.Digest, Message: fmt.Sprintf("script %q appears more than once", script.Name)}
			}
			scriptNames[script.Name] = struct{}{}
		}
		node.DependsOn = uniqueSortedDigests(node.DependsOn)
		nodes[node.Digest] = node
	}
	for _, node := range nodes {
		for _, dep := range node.DependsOn {
			if dep == node.Digest {
				return nil, &GraphError{Code: "dependency-cycle", Package: node.Digest, Message: "package depends on itself"}
			}
			if _, exists := nodes[dep]; !exists {
				return nil, &GraphError{Code: "unknown-dependency", Package: node.Digest, Message: fmt.Sprintf("dependency %s is not in the graph", dep)}
			}
		}
	}
	return nodes, nil
}

func uniqueSortedDigests(in []Digest) []Digest {
	set := make(map[Digest]struct{}, len(in))
	for _, d := range in {
		set[d] = struct{}{}
	}
	out := make([]Digest, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// topologicalOrder returns a deterministic dependency-first order. Among
// currently available nodes, the lexicographically smallest digest runs first.
func topologicalOrder(nodes map[Digest]PackageNode) ([]Digest, error) {
	indegree := make(map[Digest]int, len(nodes))
	dependents := make(map[Digest][]Digest, len(nodes))
	for digest, node := range nodes {
		indegree[digest] = len(node.DependsOn)
		for _, dep := range node.DependsOn {
			dependents[dep] = append(dependents[dep], digest)
		}
	}
	for dep := range dependents {
		sort.Slice(dependents[dep], func(i, j int) bool { return dependents[dep][i] < dependents[dep][j] })
	}
	var ready []Digest
	for digest, n := range indegree {
		if n == 0 {
			ready = append(ready, digest)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	order := make([]Digest, 0, len(nodes))
	for len(ready) > 0 {
		digest := ready[0]
		ready = ready[1:]
		order = append(order, digest)
		for _, dependent := range dependents[digest] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	}
	if len(order) != len(nodes) {
		var remaining []string
		for digest, degree := range indegree {
			if degree > 0 {
				remaining = append(remaining, string(digest))
			}
		}
		sort.Strings(remaining)
		return nil, &GraphError{Code: "dependency-cycle", Message: "cycle among: " + strings.Join(remaining, ", ")}
	}
	return order, nil
}
