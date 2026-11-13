package netsec

import (
	"encoding/json"
	"fmt"
	"strings"
)

func requestFromArgs(args ...any) (AuditRequest, error) {
	var request AuditRequest
	for _, arg := range args {
		part, err := requestFromValue(arg)
		if err != nil {
			return AuditRequest{}, err
		}
		mergeRequest(&request, part)
	}
	return request, nil
}

func requestFromValue(value any) (AuditRequest, error) {
	switch typed := value.(type) {
	case nil:
		return AuditRequest{}, nil
	case AuditRequest:
		return typed, nil
	case *AuditRequest:
		if typed == nil {
			return AuditRequest{}, nil
		}
		return *typed, nil
	case ExportedMaterial:
		return AuditRequest{Materials: []ExportedMaterial{typed}}, nil
	case *ExportedMaterial:
		if typed == nil {
			return AuditRequest{}, nil
		}
		return AuditRequest{Materials: []ExportedMaterial{*typed}}, nil
	case []ExportedMaterial:
		return AuditRequest{Materials: append([]ExportedMaterial(nil), typed...)}, nil
	case []*ExportedMaterial:
		materials := make([]ExportedMaterial, 0, len(typed))
		for _, material := range typed {
			if material != nil {
				materials = append(materials, *material)
			}
		}
		return AuditRequest{Materials: materials}, nil
	case WalletTree:
		return AuditRequest{Tree: &typed}, nil
	case *WalletTree:
		return AuditRequest{Tree: typed}, nil
	case WalletNode:
		return AuditRequest{Root: &typed}, nil
	case *WalletNode:
		return AuditRequest{Root: typed}, nil
	case []WalletNode:
		nodes := make([]*WalletNode, 0, len(typed))
		for i := range typed {
			nodes = append(nodes, &typed[i])
		}
		return AuditRequest{Nodes: nodes}, nil
	case []*WalletNode:
		return AuditRequest{Nodes: append([]*WalletNode(nil), typed...)}, nil
	case []string:
		nodes := make([]*WalletNode, 0, len(typed))
		for _, path := range typed {
			nodes = append(nodes, &WalletNode{Path: path})
		}
		return AuditRequest{Nodes: nodes}, nil
	case map[string]*WalletNode:
		nodes := make([]*WalletNode, 0, len(typed))
		for path, node := range typed {
			if node == nil {
				node = &WalletNode{}
			}
			clone := *node
			if clone.Path == "" && clone.DerivationPath == nil {
				clone.Path = path
			}
			nodes = append(nodes, &clone)
		}
		return AuditRequest{Nodes: nodes}, nil
	case map[string]WalletNode:
		nodes := make([]*WalletNode, 0, len(typed))
		for path, node := range typed {
			clone := node
			if clone.Path == "" && clone.DerivationPath == nil {
				clone.Path = path
			}
			nodes = append(nodes, &clone)
		}
		return AuditRequest{Nodes: nodes}, nil
	case AuditReport:
		return AuditRequest{Baseline: &typed}, nil
	case *AuditReport:
		return AuditRequest{Baseline: typed}, nil
	case []byte:
		return requestFromJSON(typed)
	case string:
		if strings.HasPrefix(strings.TrimSpace(typed), "{") || strings.HasPrefix(strings.TrimSpace(typed), "[") {
			return requestFromJSON([]byte(typed))
		}
		return AuditRequest{}, fmt.Errorf("%w: expected JSON object or material list", ErrInvalidMaterial)
	case json.RawMessage:
		return requestFromJSON(typed)
	}

	// Unknown structs and maps are accepted through their JSON representation.
	// This keeps adapters decoupled from this package's concrete field types.
	encoded, err := json.Marshal(value)
	if err != nil {
		return AuditRequest{}, fmt.Errorf("%w: %T: %v", ErrInvalidMaterial, value, err)
	}
	return requestFromJSON(encoded)
}

func requestFromJSON(data []byte) (AuditRequest, error) {
	trimmed := strings.TrimSpace(string(data))
	var request AuditRequest
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err == nil {
			hasExposed, hasExposedPaths, hasSafe := false, false, false
			hasTreeShape, hasRequestShape := false, false
			hasMaterialShape := false
			for key := range fields {
				switch strings.ToLower(key) {
				case "exposed":
					hasExposed = true
				case "exposedpaths":
					hasExposedPaths = true
				case "safe":
					hasSafe = true
				case "root", "nodes", "paths", "children", "index", "hardened", "path":
					hasTreeShape = true
				case "materials", "exports", "exportedmaterials", "tree", "wallettree":
					hasRequestShape = true
				case "kind", "type", "materialtype":
					hasMaterialShape = true
				}
			}
			if hasMaterialShape && hasTreeShape && !hasRequestShape && !hasExposed {
				var material ExportedMaterial
				if err := json.Unmarshal(data, &material); err == nil {
					return AuditRequest{Materials: []ExportedMaterial{material}}, nil
				}
			}
			if hasExposed && (hasExposedPaths || hasSafe) {
				var report AuditReport
				if err := json.Unmarshal(data, &report); err != nil {
					return AuditRequest{}, fmt.Errorf("%w: %v", ErrInvalidMaterial, err)
				}
				return AuditRequest{Baseline: &report}, nil
			}
			if hasTreeShape && !hasRequestShape && !hasMaterialShape {
				var tree WalletTree
				if err := json.Unmarshal(data, &tree); err != nil {
					return AuditRequest{}, fmt.Errorf("%w: %v", ErrInvalidMaterial, err)
				}
				return AuditRequest{Tree: &tree}, nil
			}
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return AuditRequest{}, fmt.Errorf("%w: %v", ErrInvalidMaterial, err)
		}
		return request, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var materials []ExportedMaterial
		if err := json.Unmarshal(data, &materials); err == nil {
			valid := true
			for i := range materials {
				if _, err := normalizeMaterial(materials[i]); err != nil {
					valid = false
					break
				}
			}
			if valid {
				return AuditRequest{Materials: materials}, nil
			}
		}
		var nodes []*WalletNode
		if err := json.Unmarshal(data, &nodes); err == nil {
			return AuditRequest{Nodes: nodes}, nil
		}
		var paths []string
		if err := json.Unmarshal(data, &paths); err == nil {
			return requestFromValue(paths)
		}
	}
	return AuditRequest{}, fmt.Errorf("%w: unsupported JSON shape", ErrInvalidMaterial)
}

func mergeRequest(dst *AuditRequest, src AuditRequest) {
	dst.Materials = append(dst.Materials, src.Materials...)
	dst.Exports = append(dst.Exports, src.Exports...)
	dst.ExportedMaterials = append(dst.ExportedMaterials, src.ExportedMaterials...)
	if src.WalletTree != nil && dst.WalletTree == nil {
		dst.WalletTree = src.WalletTree
	}
	if src.Wallet != nil && dst.Wallet == nil {
		dst.Wallet = src.Wallet
	}
	if src.Tree != nil {
		if dst.Tree == nil {
			dst.Tree = src.Tree
		} else {
			dst.Nodes = append(dst.Nodes, src.Tree.Nodes...)
			if dst.Root == nil {
				dst.Root = src.Tree.Root
			}
		}
	}
	if src.Root != nil {
		if dst.Root == nil {
			dst.Root = src.Root
		}
	}
	dst.Nodes = append(dst.Nodes, src.Nodes...)
	if src.Baseline != nil && dst.Baseline == nil {
		dst.Baseline = src.Baseline
	}
	if src.Previous != nil && dst.Previous == nil {
		dst.Previous = src.Previous
	}
	if src.PreviousReport != nil && dst.PreviousReport == nil {
		dst.PreviousReport = src.PreviousReport
	}
	if src.Prior != nil && dst.Prior == nil {
		dst.Prior = src.Prior
	}
	if src.LastReport != nil && dst.LastReport == nil {
		dst.LastReport = src.LastReport
	}
	dst.PreviouslyExposed = append(dst.PreviouslyExposed, src.PreviouslyExposed...)
	dst.PreviousExposed = append(dst.PreviousExposed, src.PreviousExposed...)
	dst.PreviousExposedPaths = append(dst.PreviousExposedPaths, src.PreviousExposedPaths...)
}

// BuildReport validates and audits an importable request. It is an explicit
// alias for Audit for service code that treats construction and analysis as
// separate steps.
func BuildReport(args ...any) (AuditReport, error) {
	return Audit(args...)
}

func materialSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}
	return set
}

func newlyExposed(oldPaths, nextPaths []string) []string {
	old := materialSet(oldPaths)
	new := make([]string, 0)
	for _, path := range nextPaths {
		if _, ok := old[path]; !ok {
			new = append(new, path)
		}
	}
	return new
}
