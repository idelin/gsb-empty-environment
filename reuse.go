package policycheck

import (
	"fmt"
	"sort"
)

// NamedChange 描述一个命名谓词的语义摘要变化。
type NamedChange struct {
	Name      string `json:"name"`
	OldDigest Digest `json:"old_digest,omitempty"`
	NewDigest Digest `json:"new_digest,omitempty"`
}

// ReuseEntry 是可复用或已重算分支的密码学条目。
type ReuseEntry struct {
	NodeID   string `json:"node_id"`
	DAGIndex int    `json:"dag_index"`
	Digest   Digest `json:"digest"`
}

// PartialReuseCertificate 证明一次单命名谓词变更中的部分复用集合。
type PartialReuseCertificate struct {
	Schema        string       `json:"schema"`
	Change        NamedChange  `json:"change"`
	BaseRoot      Digest       `json:"base_root"`
	CandidateRoot Digest       `json:"candidate_root"`
	Reused        []ReuseEntry `json:"reused,omitempty"`
	Recomputed    []ReuseEntry `json:"recomputed,omitempty"`
	Signature     []byte       `json:"signature,omitempty"`
}

type reuseSigningMaterial struct {
	Schema        string       `json:"schema"`
	Change        NamedChange  `json:"change"`
	BaseRoot      Digest       `json:"base_root"`
	CandidateRoot Digest       `json:"candidate_root"`
	Reused        []ReuseEntry `json:"reused,omitempty"`
	Recomputed    []ReuseEntry `json:"recomputed,omitempty"`
}

// IssuePartialReuseCertificate 用宿主 HMAC 密钥签发部分复用证书。
func IssuePartialReuseCertificate(
	key []byte,
	change NamedChange,
	baseRoot, candidateRoot Digest,
	reused, recomputed []ReuseEntry,
) *PartialReuseCertificate {
	cert := &PartialReuseCertificate{
		Schema:        "policycheck-partial-reuse/v1",
		Change:        change,
		BaseRoot:      baseRoot,
		CandidateRoot: candidateRoot,
		Reused:        append([]ReuseEntry(nil), reused...),
		Recomputed:    append([]ReuseEntry(nil), recomputed...),
	}
	material := reuseSigningMaterial{
		Schema: cert.Schema, Change: cert.Change, BaseRoot: cert.BaseRoot,
		CandidateRoot: cert.CandidateRoot, Reused: cert.Reused, Recomputed: cert.Recomputed,
	}
	cert.Signature = hmacSHA256(key, canonicalJSON(material))
	return cert
}

// VerifyPartialReuseCertificate 校验签名、当前 DAG 摘要以及复用集合的依赖纯洁性。
func VerifyPartialReuseCertificate(dag *DAG, cert *PartialReuseCertificate, key []byte) error {
	if cert == nil {
		return fmt.Errorf("policycheck: nil partial reuse certificate")
	}
	if len(key) == 0 {
		return fmt.Errorf("policycheck: refusing unsigned partial reuse certificate")
	}
	material := reuseSigningMaterial{
		Schema: cert.Schema, Change: cert.Change, BaseRoot: cert.BaseRoot,
		CandidateRoot: cert.CandidateRoot, Reused: cert.Reused, Recomputed: cert.Recomputed,
	}
	if !equalMAC(hmacSHA256(key, canonicalJSON(material)), cert.Signature) {
		return fmt.Errorf("policycheck: forged or tampered partial reuse certificate")
	}

	full, err := BuildCertificate(dag, key)
	if err != nil {
		return err
	}
	if full.Manifest.Root != cert.CandidateRoot {
		return fmt.Errorf("policycheck: candidate root does not match current DAG")
	}
	graph, err := NewTraceGraph(full)
	if err != nil {
		return err
	}
	affected := graph.AffectedByNamed(cert.Change.Name)

	seen := make(map[int]struct{})
	for _, entry := range cert.Reused {
		if entry.DAGIndex < 0 || entry.DAGIndex >= len(dag.Nodes) {
			return fmt.Errorf("policycheck: reused node %d out of range", entry.DAGIndex)
		}
		att := full.Manifest.Nodes[entry.DAGIndex]
		if att.Digest != entry.Digest {
			return fmt.Errorf("policycheck: reused node %d digest is stale or forged", entry.DAGIndex)
		}
		if entry.NodeID != "" && att.ID != entry.NodeID {
			return fmt.Errorf("policycheck: reused node %d ID mismatch", entry.DAGIndex)
		}
		if containsInt(affected, entry.DAGIndex) {
			return fmt.Errorf("policycheck: node %d depends on changed named predicate %q and cannot be partially reused",
				entry.DAGIndex, cert.Change.Name)
		}
		if _, dup := seen[entry.DAGIndex]; dup {
			return fmt.Errorf("policycheck: duplicate reuse claim for node %d", entry.DAGIndex)
		}
		seen[entry.DAGIndex] = struct{}{}
	}

	for _, entry := range cert.Recomputed {
		if entry.DAGIndex < 0 || entry.DAGIndex >= len(dag.Nodes) {
			return fmt.Errorf("policycheck: recomputed node %d out of range", entry.DAGIndex)
		}
		if full.Manifest.Nodes[entry.DAGIndex].Digest != entry.Digest {
			return fmt.Errorf("policycheck: recomputed node %d digest does not match DAG", entry.DAGIndex)
		}
	}
	return nil
}

// BranchRebuildFunc 只接收受影响节点，并返回重算后的节点条目。
type BranchRebuildFunc func(affectedIndices []int) ([]ReuseEntry, error)

// IncrementalResult 报告一次单命名谓词增量重算。
type IncrementalResult struct {
	Change            NamedChange
	AffectedIndices   []int
	DirectIndices     []int
	ReachableIndices  []int
	ReusedEntries     []ReuseEntry
	RecomputedEntries []ReuseEntry
	RebuildCalls      int
	Certificate       *PartialReuseCertificate
}

// RecomputeAffectedBranches 根据溯源图只把受影响下标交给 rebuild。
//
// rebuild 可以原地修改 DAG；未受影响节点的当前摘要会作为复用条目
// 写入 HMAC 证书。
func RecomputeAffectedBranches(
	dag *DAG,
	change NamedChange,
	key []byte,
	rebuild BranchRebuildFunc,
) (*IncrementalResult, error) {
	base, err := BuildCertificate(dag, key)
	if err != nil {
		return nil, err
	}
	graph, err := NewTraceGraph(base)
	if err != nil {
		return nil, err
	}
	affected := graph.AffectedByNamed(change.Name)

	result := &IncrementalResult{Change: change, AffectedIndices: append([]int(nil), affected...)}
	result.DirectIndices = graph.DirectNamedNodes(change.Name)
	result.ReachableIndices = graph.ControlReachableIndices(change.Name)
	var recomputed []ReuseEntry
	if rebuild != nil {
		result.RebuildCalls = 1
		recomputed, err = rebuild(append([]int(nil), affected...))
		if err != nil {
			return nil, err
		}
	}
	result.RecomputedEntries = append([]ReuseEntry(nil), recomputed...)

	candidate, err := BuildCertificate(dag, key)
	if err != nil {
		return nil, err
	}
	if len(candidate.Manifest.Nodes) != len(base.Manifest.Nodes) {
		return nil, fmt.Errorf("policycheck: incremental rebuild must preserve DAG node slots")
	}
	affectedBeforeSet := make(map[int]struct{})
	for _, idx := range affected {
		affectedBeforeSet[idx] = struct{}{}
	}
	affectedAfter, err := NewTraceGraph(candidate)
	if err != nil {
		return nil, err
	}
	affectedSet := make(map[int]struct{})
	for _, idx := range affectedAfter.AffectedByNamed(change.Name) {
		affectedSet[idx] = struct{}{}
	}

	for i, old := range base.Manifest.Nodes {
		if _, isAffected := affectedBeforeSet[i]; isAffected {
			continue
		}
		if i >= len(candidate.Manifest.Nodes) || candidate.Manifest.Nodes[i].Digest != old.Digest {
			return nil, fmt.Errorf("policycheck: incremental rebuild modified unaffected node %d", i)
		}
	}

	for i, att := range candidate.Manifest.Nodes {
		if _, affected := affectedSet[i]; affected {
			continue
		}
		result.ReusedEntries = append(result.ReusedEntries, ReuseEntry{
			NodeID: att.ID, DAGIndex: i, Digest: att.Digest,
		})
	}
	sort.Slice(result.ReusedEntries, func(i, j int) bool {
		return result.ReusedEntries[i].DAGIndex < result.ReusedEntries[j].DAGIndex
	})

	result.Certificate = IssuePartialReuseCertificate(
		key, change, base.Manifest.Root, candidate.Manifest.Root,
		result.ReusedEntries, result.RecomputedEntries,
	)
	return result, nil
}
