package netsec

import (
	"path"
	"sort"
)

// ValidateReferenceGraph rejects cycles formed by local references across
// the kustomizations in one repository.
//
// nodes maps each kustomization to its cleaned, root-relative file path
// (for example "overlays/dev/kustomization.yaml"). Local resources, bases
// and patch paths are treated as edges from a document to the document or
// directory they reference. Remote references are ignored here because they
// cannot participate in an in-repository cycle.
//
// Every node is first run through Validate so a cyclic graph cannot be used
// to smuggle an otherwise invalid reference past the gateway.
func (admitter *Admitter) ValidateReferenceGraph(nodes map[string]*Kustomization) error {
	collector := violationCollector{}

	// Map cleaned node keys back to the caller's original map keys, and
	// index which kustomization lives in each directory so directory-shaped
	// references (the common case) resolve to a node.
	keys := make([]string, 0, len(nodes))
	originalKey := make(map[string]string, len(nodes))
	dirToNode := make(map[string]string, len(nodes))
	for original := range nodes {
		node := cleanRepoPath(original)
		if !inRoot(node) {
			collector.add("referenceGraph", -1, original, ReasonPathEscapesRoot,
				"kustomization node path escapes the repository root")
			continue
		}
		keys = append(keys, node)
		originalKey[node] = original
		dirToNode[path.Dir(node)] = node
	}
	sort.Strings(keys)

	edges := make(map[string][]string)
	for _, node := range keys {
		kustomization := nodes[originalKey[node]]
		baseDir := path.Dir(node)
		if baseDir == "." {
			baseDir = ""
		}
		if err := admitter.validateAt(kustomization, baseDir); err != nil {
			collector.violations = append(collector.violations,
				err.(*AdmissionError).Violations...)
		}
		if kustomization == nil {
			edges[node] = nil
			continue
		}
		edges[node] = admitter.graphEdges(&collector, baseDir, kustomization, dirToNode, keys)
	}

	detectCycles(keys, edges, &collector)

	if len(collector.violations) == 0 {
		return nil
	}
	return &AdmissionError{Violations: collector.violations}
}

// graphEdges resolves every local reference of one document to its
// root-relative target. Invalid references are recorded as violations and
// skipped, so they cannot produce a misleading cycle report.
func (admitter *Admitter) graphEdges(
	collector *violationCollector,
	baseDir string,
	kustomization *Kustomization,
	dirToNode map[string]string,
	nodeKeys []string,
) []string {
	var targets []string
	add := func(field string, index int, value string) {
		kind, ok := classifyReference(value)
		if !ok || kind == refRemote {
			return
		}
		resolved, reason, detail := resolveLocalPath(admitter.catalog, baseDir, value)
		if reason != "" {
			collector.add(field, index, value, reason, detail)
			return
		}
		if targetNode, ok := resolveGraphNode(resolved, dirToNode, nodeKeys); ok {
			targets = append(targets, targetNode)
		}
	}

	for index, resource := range kustomization.Resources {
		add("resources", index, resource)
	}
	for index, base := range kustomization.Bases {
		add("bases", index, base)
	}
	for index, patch := range kustomization.Patches {
		if patch.Path != "" {
			add("patches", index, patch.Path)
		}
	}
	return dedupe(targets)
}

// resolveGraphNode maps a resolved resource path to a kustomization node.
// Exact file matches win; otherwise the reference is treated as a directory
// containing one of the conventional kustomization file names.
func resolveGraphNode(resolved string, dirToNode map[string]string, nodeKeys []string) (string, bool) {
	for _, node := range nodeKeys {
		if node == resolved {
			return node, true
		}
	}
	if node, ok := dirToNode[resolved]; ok {
		return node, true
	}
	return "", false
}

// detectCycles runs an iterative depth-first search over edges using a
// three-color marking. Deterministic key order keeps the reported cycle
// stable across runs.
func detectCycles(keys []string, edges map[string][]string, collector *violationCollector) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(keys))

	var visit func(node string, stack []string)
	visit = func(node string, stack []string) {
		color[node] = gray
		stack = append(stack, node)

		neighbors := append([]string(nil), edges[node]...)
		sort.Strings(neighbors)
		for _, neighbor := range neighbors {
			switch color[neighbor] {
			case gray:
				cycle := append([]string(nil), stack...)
				cycle = append(cycle, neighbor)
				collector.add("referenceGraph", -1, joinCycle(cycle),
					ReasonReferenceCycle, "circular kustomization reference")
			case white:
				if _, known := edges[neighbor]; known {
					visit(neighbor, stack)
				}
			}
		}
		color[node] = black
	}

	for _, node := range keys {
		if color[node] == white {
			visit(node, nil)
		}
	}
}

func dedupe(values []string) []string {
	if len(values) < 2 {
		return values
	}
	sort.Strings(values)
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func joinCycle(nodes []string) string {
	result := ""
	for index, node := range nodes {
		if index > 0 {
			result += " -> "
		}
		result += node
	}
	return result
}
