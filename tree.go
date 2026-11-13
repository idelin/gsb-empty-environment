package netsec

import (
	"fmt"
	"sort"
	"strings"
)

type nodeInfo struct {
	path     Path
	key      string
	parent   *nodeInfo
	children []*nodeInfo
	label    string
}

type treeIndex struct {
	root  *nodeInfo
	nodes map[string]*nodeInfo
}

func (tree *treeIndex) ensure(path Path) (*nodeInfo, error) {
	if tree.root == nil {
		root := &nodeInfo{path: Path{}, key: "m"}
		tree.root = root
		tree.nodes[root.key] = root
	}
	if !path.HasPrefix(tree.root.path) {
		return nil, fmt.Errorf("%w: %s is outside root %s", ErrInvalidTree, path, tree.root.path)
	}

	current := tree.root
	for i := len(tree.root.path); i < len(path); i++ {
		segment := path[i]
		parentPath := current.path
		nextPath := parentPath.Child(segment.Index, segment.Hardened)
		key := nextPath.String()
		next := tree.nodes[key]
		if next == nil {
			next = &nodeInfo{
				path:   nextPath,
				key:    key,
				parent: current,
			}
			tree.nodes[key] = next
			current.children = append(current.children, next)
		}
		current = next
	}
	return current, nil
}

func (tree *treeIndex) finalize() {
	for _, node := range tree.nodes {
		sort.Slice(node.children, func(i, j int) bool {
			return ComparePaths(node.children[i].path, node.children[j].path) < 0
		})
	}
}

func normalizeRequest(req AuditRequest) (AuditRequest, error) {
	materials := make([]ExportedMaterial, 0, len(req.Materials)+len(req.Exports)+len(req.ExportedMaterials))
	materials = append(materials, req.Materials...)
	materials = append(materials, req.Exports...)
	materials = append(materials, req.ExportedMaterials...)

	for i := range materials {
		normalized, err := normalizeMaterial(materials[i])
		if err != nil {
			return AuditRequest{}, fmt.Errorf("material %d: %w", i, err)
		}
		materials[i] = normalized
	}
	req.Materials = materials
	req.Exports = nil
	req.ExportedMaterials = nil
	if req.Tree == nil {
		switch {
		case req.WalletTree != nil:
			req.Tree = req.WalletTree
		case req.Wallet != nil:
			req.Tree = req.Wallet
		}
	}
	if req.Tree == nil {
		switch {
		case req.Root != nil:
			req.Tree = &WalletTree{Root: req.Root, Nodes: req.Nodes}
		case len(req.Nodes) > 0:
			req.Tree = &WalletTree{Nodes: req.Nodes}
		}
	}
	if req.Tree != nil {
		if req.Root == nil {
			req.Root = req.Tree.Root
		}
		if len(req.Nodes) == 0 {
			req.Nodes = req.Tree.Nodes
		}
	}
	return req, nil
}

func normalizeMaterial(material ExportedMaterial) (ExportedMaterial, error) {
	kindValue := material.Kind
	if kindValue == "" {
		if material.Type != "" {
			kindValue = material.Type
		} else {
			kindValue = material.MaterialType
		}
	}
	kind, err := NormalizeKind(kindValue)
	if err != nil {
		return ExportedMaterial{}, err
	}
	material.Kind = kind
	material.Type = ""
	material.MaterialType = ""

	if material.Path != "" {
		parsed, err := ParsePath(material.Path)
		if err != nil {
			return ExportedMaterial{}, err
		}
		if material.DerivationPath != nil && !material.DerivationPath.Equal(parsed) {
			return ExportedMaterial{}, fmt.Errorf("%w: path and derivationPath disagree", ErrInvalidMaterial)
		}
		material.DerivationPath = normalizeSegments(parsed)
	} else if material.DerivationPath != nil {
		material.DerivationPath = normalizeSegments(material.DerivationPath)
	} else {
		return ExportedMaterial{}, fmt.Errorf("%w: missing path", ErrInvalidMaterial)
	}
	material.Path = material.DerivationPath.String()
	return material, nil
}

// buildTreeIndex creates the complete tree index. When no tree is supplied,
// the envelope is reconstructed from the exported paths so that a caller can
// still audit a bare material list.
func buildTreeIndex(req AuditRequest) (*treeIndex, error) {
	explicit := req.Tree != nil || len(req.Nodes) > 0
	tree := &treeIndex{nodes: map[string]*nodeInfo{}}

	if !explicit {
		root, err := tree.ensure(Path{})
		if err != nil {
			return nil, err
		}
		root.label = "wallet root"
		for _, material := range req.Materials {
			if material.DerivationPath.IsRoot() {
				continue
			}
			if _, err := tree.ensure(material.DerivationPath); err != nil {
				return nil, err
			}
		}
		tree.finalize()
		return tree, nil
	}

	var rootCandidate *WalletNode
	if req.Tree != nil {
		rootCandidate = req.Tree.Root
	}
	if req.Root != nil {
		rootCandidate = req.Root
	}

	var flat []*WalletNode
	if req.Tree != nil {
		flat = append(flat, req.Tree.Nodes...)
		for _, path := range req.Tree.Paths {
			flat = append(flat, &WalletNode{Path: path})
		}
	}
	flat = append(flat, req.Nodes...)

	var rootPath Path
	if rootCandidate == nil {
		for _, node := range flat {
			path, err := resolveNodePath(nil, false, node, true)
			if err != nil {
				return nil, err
			}
			if path.IsRoot() {
				rootCandidate = node
				rootPath = path
				break
			}
		}
	} else {
		path, err := resolveNodePath(nil, false, rootCandidate, true)
		if err != nil {
			return nil, err
		}
		rootPath = path
	}
	if rootCandidate == nil {
		rootPath = Path{}
	}

	root, err := tree.ensure(rootPath)
	if err != nil {
		return nil, err
	}

	if rootCandidate != nil {
		if err := addNestedNode(tree, root, rootCandidate); err != nil {
			return nil, err
		}
	}
	for _, node := range flat {
		path, err := resolveNodePath(nil, false, node, false)
		if err != nil {
			return nil, err
		}
		info, err := tree.ensure(path)
		if err != nil {
			return nil, err
		}
		if info.label == "" {
			info.label = node.Label
		}
		if err := addNestedChildren(tree, info, node.Children); err != nil {
			return nil, err
		}
	}

	tree.finalize()
	return tree, nil
}

func addNestedNode(tree *treeIndex, info *nodeInfo, node *WalletNode) error {
	if info.label == "" {
		info.label = node.Label
	}
	return addNestedChildren(tree, info, node.Children)
}

func addNestedChildren(tree *treeIndex, parent *nodeInfo, children []*WalletNode) error {
	for _, childNode := range children {
		path, err := resolveNodePath(parent.path, true, childNode, false)
		if err != nil {
			return err
		}
		if len(path) != len(parent.path)+1 || !path.HasPrefix(parent.path) {
			return fmt.Errorf("%w: child %s is not directly below %s", ErrInvalidTree, path, parent.path)
		}
		child, err := tree.ensure(path)
		if err != nil {
			return err
		}
		if child.label == "" {
			child.label = childNode.Label
		}
		if err := addNestedChildren(tree, child, childNode.Children); err != nil {
			return err
		}
	}
	return nil
}

// resolveNodePath converts the several accepted node encodings to one
// absolute path. Relative strings are resolved against parentPath, while
// strings beginning with m are always absolute.
func resolveNodePath(parentPath Path, hasParent bool, node *WalletNode, isRoot bool) (Path, error) {
	if node == nil {
		return Path{}, fmt.Errorf("%w: nil node", ErrInvalidTree)
	}

	if node.DerivationPath != nil {
		path := normalizeSegments(node.DerivationPath)
		if hasParent && !isRoot && len(path) != len(parentPath)+1 {
			if len(path) == 1 && !path.HasPrefix(parentPath) {
				path = parentPath.Child(path[0].Index, path[0].Hardened)
			}
		}
		if node.Path != "" {
			parsed, err := ParsePath(node.Path)
			if err != nil {
				return nil, err
			}
			if !parsed.Equal(path) {
				return nil, fmt.Errorf("%w: path and derivationPath disagree", ErrInvalidTree)
			}
		}
		return path, nil
	}

	if node.Path != "" {
		value := strings.TrimSpace(node.Path)
		absoluteSpelling := strings.HasPrefix(value, "m") || strings.HasPrefix(value, "M")
		parsed, err := ParsePath(value)
		if err != nil {
			return nil, err
		}
		if isRoot {
			if absoluteSpelling || !hasParent {
				return parsed, nil
			}
		}
		if hasParent {
			if absoluteSpelling {
				return parsed, nil
			}
			return appendPath(parentPath, parsed), nil
		}
		return parsed, nil
	}

	segment := PathSegment{Index: node.Index, Hardened: node.Hardened}
	if segment.Index >= uint32(1)<<31 {
		segment.Index -= uint32(1) << 31
		segment.Hardened = true
	}
	if hasParent {
		return parentPath.Child(segment.Index, segment.Hardened), nil
	}
	if isRoot {
		return Path{}, nil
	}
	return Path{segment}, nil
}

func appendPath(parent, suffix Path) Path {
	path := make(Path, 0, len(parent)+len(suffix))
	path = append(path, parent...)
	path = append(path, suffix...)
	return path
}

func checkMaterialPaths(tree *treeIndex, materials []ExportedMaterial) error {
	for i, material := range materials {
		if _, ok := tree.nodes[material.DerivationPath.String()]; !ok {
			return fmt.Errorf("material %d: %w: %s", i, ErrUnknownPath, material.Path)
		}
	}
	return nil
}

func treeSnapshot(root *nodeInfo) *WalletTree {
	if root == nil {
		return &WalletTree{}
	}
	return &WalletTree{Root: snapshotNode(root)}
}

func snapshotNode(node *nodeInfo) *WalletNode {
	snapshot := &WalletNode{
		Path:           node.path.String(),
		DerivationPath: clonePath(node.path),
		Label:          node.label,
	}
	if segment, ok := node.path.Last(); ok {
		snapshot.Index = segment.Index
		snapshot.Hardened = segment.Hardened
	}
	for _, child := range node.children {
		snapshot.Children = append(snapshot.Children, snapshotNode(child))
	}
	return snapshot
}

func clonePath(path Path) Path {
	if path == nil {
		return nil
	}
	clone := make(Path, len(path))
	copy(clone, path)
	return clone
}
