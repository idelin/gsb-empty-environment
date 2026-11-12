package netsec

import (
	"fmt"
	"sort"
)

// ViolationCode 描述迁移映射破坏关联前提的具体原因。
type ViolationCode string

const (
	// ViolationUnknownSource 映射引用了旧图中不存在的节点。
	ViolationUnknownSource ViolationCode = "unknown-source"
	// ViolationUnknownTarget 映射引用了新图中不存在的节点。
	ViolationUnknownTarget ViolationCode = "unknown-target"
	// ViolationUnmappedNode 旧图中的节点在映射里完全缺失。
	ViolationUnmappedNode ViolationCode = "unmapped-node"
	// ViolationDependencyBroken 旧图中的依赖边在新图里找不到对应边。
	ViolationDependencyBroken ViolationCode = "dependency-broken"
)

// Violation 是一条映射校验违例。
type Violation struct {
	Code     ViolationCode
	SourceID string
	TargetID string
	Edge     *Edge
	Message  string
}

// NodeMigration 是调用方显式提供的旧版本到新版本的节点迁移映射。
// 同一个旧节点可以映射到多个新节点候选，候选全部保留，关联结果会标记为
// 歧义而不是擅自选定。映射只接收显式条目，绝不依据节点名称或文本相似度
// 自动生成。
type NodeMigration struct {
	FromVersion string
	ToVersion   string
	candidates  map[string][]string
}

// NewNodeMigration 创建一个空的迁移映射。
func NewNodeMigration(fromVersion, toVersion string) *NodeMigration {
	return &NodeMigration{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		candidates:  make(map[string][]string),
	}
}

// Map 登记一条“旧节点 -> 新节点”的显式迁移候选。重复登记相同条目会被
// 去重；旧节点此前已有其它候选时，新候选作为歧义候选追加。
func (m *NodeMigration) Map(oldNodeID, newNodeID string) error {
	if oldNodeID == "" || newNodeID == "" {
		return fmt.Errorf("migration entries must reference non-empty node ids")
	}
	for _, existing := range m.candidates[oldNodeID] {
		if existing == newNodeID {
			return nil
		}
	}
	m.candidates[oldNodeID] = append(m.candidates[oldNodeID], newNodeID)
	return nil
}

// Candidates 返回旧节点的全部迁移候选，顺序为登记顺序。
func (m *NodeMigration) Candidates(oldNodeID string) []string {
	return append([]string(nil), m.candidates[oldNodeID]...)
}

// IsAmbiguous 报告旧节点是否存在多个迁移候选。
func (m *NodeMigration) IsAmbiguous(oldNodeID string) bool {
	return len(m.candidates[oldNodeID]) > 1
}

func (m *NodeMigration) oldNodeIDs() []string {
	ids := make([]string, 0, len(m.candidates))
	for id := range m.candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// EdgeEvidence 记录一条旧依赖边在新图中的对应情况。
type EdgeEvidence struct {
	OldEdge        Edge
	FromCandidates []string
	ToCandidates   []string
	// MatchedEdges 是新图中满足候选组合且关系一致的边。
	MatchedEdges []Edge
}

// Check 校验映射相对于一对来源图是否成立：
//   - 所有映射端点必须分别存在于旧图和新图；
//   - 旧图中的每个节点都必须至少有一个迁移候选；
//   - 旧图中的每条依赖边，必须能在候选节点组合之间找到关系一致的新边，
//     即迁移必须保持依赖关系。
//
// 校验只消费显式映射；节点名称、路径或错误文本不参与判断。
func (m *NodeMigration) Check(oldGraph, newGraph Graph) ([]Violation, []EdgeEvidence, error) {
	if err := oldGraph.Validate(); err != nil {
		return nil, nil, fmt.Errorf("old graph invalid: %w", err)
	}
	if err := newGraph.Validate(); err != nil {
		return nil, nil, fmt.Errorf("new graph invalid: %w", err)
	}

	var violations []Violation
	for _, oldID := range m.oldNodeIDs() {
		if !oldGraph.hasNode(oldID) {
			violations = append(violations, Violation{
				Code:     ViolationUnknownSource,
				SourceID: oldID,
				Message:  fmt.Sprintf("migration source %q is not present in the old graph", oldID),
			})
		}
		for _, newID := range m.candidates[oldID] {
			if !newGraph.hasNode(newID) {
				violations = append(violations, Violation{
					Code:     ViolationUnknownTarget,
					SourceID: oldID,
					TargetID: newID,
					Message:  fmt.Sprintf("migration target %q for %q is not present in the new graph", newID, oldID),
				})
			}
		}
	}

	for _, node := range oldGraph.Nodes {
		if len(m.candidates[node.ID]) == 0 {
			violations = append(violations, Violation{
				Code:     ViolationUnmappedNode,
				SourceID: node.ID,
				Message:  fmt.Sprintf("old node %q has no explicit migration entry", node.ID),
			})
		}
	}

	var evidence []EdgeEvidence
	for _, oldEdge := range oldGraph.Edges {
		fromCandidates := m.Candidates(oldEdge.From)
		toCandidates := m.Candidates(oldEdge.To)
		edgeEvidence := EdgeEvidence{
			OldEdge:        oldEdge,
			FromCandidates: fromCandidates,
			ToCandidates:   toCandidates,
		}
		for _, newEdge := range newGraph.Edges {
			if !contains(fromCandidates, newEdge.From) || !contains(toCandidates, newEdge.To) {
				continue
			}
			if oldEdge.Relation != "" && newEdge.Relation != oldEdge.Relation {
				continue
			}
			edgeEvidence.MatchedEdges = append(edgeEvidence.MatchedEdges, newEdge)
		}
		if len(edgeEvidence.MatchedEdges) == 0 {
			violations = append(violations, Violation{
				Code:     ViolationDependencyBroken,
				SourceID: oldEdge.From,
				TargetID: oldEdge.To,
				Edge:     &oldEdge,
				Message: fmt.Sprintf(
					"dependency %q -%s-> %q has no matching edge among migration candidates",
					oldEdge.From, oldEdge.Relation, oldEdge.To,
				),
			})
		}
		evidence = append(evidence, edgeEvidence)
	}

	return violations, evidence, nil
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
