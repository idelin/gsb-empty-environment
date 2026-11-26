package policycheck

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Attestation 把一个 DAG 节点的内容、密码学依赖和命名谓词依赖绑定。
type Attestation struct {
	ID        string   `json:"id"`
	DAGIndex  int      `json:"dag_index"`
	Digest    Digest   `json:"digest"`
	Content   Digest   `json:"content"`
	DependsOn []Digest `json:"depends_on,omitempty"`
	NamedDeps []string `json:"named_deps,omitempty"`
}

// RuleAttestation 把参考规则也纳入溯源图。
type RuleAttestation struct {
	ID        string   `json:"id"`
	Index     int      `json:"index"`
	Digest    Digest   `json:"digest"`
	Predicate Digest   `json:"predicate"`
	Decision  Digest   `json:"decision"`
	NamedDeps []string `json:"named_deps,omitempty"`
}

// NamedAttestation 把命名谓词的调用方实现/版本摘要纳入溯源图。
type NamedAttestation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Digest  Digest `json:"digest"`
}

// Manifest 是证书中被签名的规范化材料。
type Manifest struct {
	Schema          string             `json:"schema"`
	DAGDigest       Digest             `json:"dag_digest"`
	Root            Digest             `json:"root"`
	Nodes           []Attestation      `json:"nodes"`
	Rules           []RuleAttestation  `json:"rules,omitempty"`
	NamedPredicates []NamedAttestation `json:"named_predicates,omitempty"`
}

// Certificate 是 HMAC 保护的完整 DAG 溯源证书。
type Certificate struct {
	Manifest  Manifest `json:"manifest"`
	Signature []byte   `json:"signature,omitempty"`
}

// TraceGraph 是可按命名谓词反查受影响分支的依赖溯源图。
type TraceGraph struct {
	Certificate *Certificate
	byDigest    map[Digest]Attestation
	byIndex     map[int]Attestation
	dependents  map[Digest][]int
}

// DependencyGraph 是溯源依赖图的术语别名。
type DependencyGraph = TraceGraph

// ProvenanceGraph 是密码学溯源图的术语别名。
type ProvenanceGraph = TraceGraph

// NewDependencyGraph 构造依赖溯源图。
func NewDependencyGraph(dag *DAG) (*DependencyGraph, error) { return BuildTraceGraph(dag) }

// NewProvenanceGraph 构造密码学依赖溯源图。
func NewProvenanceGraph(dag *DAG) (*ProvenanceGraph, error) { return BuildTraceGraph(dag) }

func canonicalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func signManifest(key []byte, manifest Manifest) []byte {
	if len(key) == 0 {
		return nil
	}
	return hmacSHA256(key, canonicalJSON(manifest))
}

// BuildCertificate 为前向 DAG 构造内容寻址证明并用 key 签名。
func BuildCertificate(dag *DAG, key []byte) (*Certificate, error) {
	return BuildCertificateWithRegistry(dag, nil, key)
}

// BuildCertificateWithRegistry 在 DAG 证明之外绑定被引用命名谓词的摘要。
func BuildCertificateWithRegistry(dag *DAG, registry Registry, key []byte) (*Certificate, error) {
	if err := dag.ValidateForward(); err != nil {
		return nil, err
	}
	n := len(dag.Nodes)
	atts := make([]Attestation, n)
	byDigest := make(map[Digest]Attestation, n)

	for i := n - 1; i >= 0; i-- {
		node := dag.Nodes[i]
		id := node.ID
		if id == "" {
			id = fmt.Sprintf("dag-node:%d", i)
		}
		content := HashCanonical("dag-node", node)
		var deps []Digest
		var named []string

		if node.Decision != nil {
			deps = append(deps, HashCanonical("decision", node.Decision))
		} else {
			predDigest := node.Test.ContentDigest()
			deps = append(deps, predDigest)
			named = append(named, CollectNamedNames(node.Test)...)
			for _, target := range []int{node.True.To, node.False.To} {
				if target < 0 || target >= n || atts[target].Digest == "" {
					return nil, fmt.Errorf("policycheck: internal certificate ordering error at node %d target %d", i, target)
				}
				deps = append(deps, atts[target].Digest)
			}
		}

		sort.Slice(deps, func(a, b int) bool { return deps[a] < deps[b] })
		named = uniqueSorted(named)
		digest := HashCanonical("attestation", struct {
			ID        string   `json:"id"`
			Index     int      `json:"index"`
			Content   Digest   `json:"content"`
			DependsOn []Digest `json:"depends_on"`
			NamedDeps []string `json:"named_deps"`
		}{id, i, content, deps, named})

		atts[i] = Attestation{
			ID:        id,
			DAGIndex:  i,
			Digest:    digest,
			Content:   content,
			DependsOn: deps,
			NamedDeps: named,
		}
		byDigest[digest] = atts[i]
	}

	manifest := Manifest{
		Schema:          "policycheck/v1",
		DAGDigest:       dag.Digest(),
		Root:            atts[dag.Root].Digest,
		Nodes:           atts,
		NamedPredicates: namedAttestationsForDAG(dag, registry),
	}
	cert := &Certificate{Manifest: manifest, Signature: signManifest(key, manifest)}
	return cert, nil
}

func namedAttestationsForDAG(dag *DAG, registry Registry) []NamedAttestation {
	var preds []*Pred
	if dag != nil {
		for _, node := range dag.Nodes {
			if node != nil && node.Test != nil {
				preds = append(preds, node.Test)
			}
		}
	}
	return namedAttestations(preds, registry)
}

func namedAttestations(predRoots []*Pred, registry Registry) []NamedAttestation {
	names := CollectNamedNames(predRoots...)
	out := make([]NamedAttestation, 0, len(names))
	for _, name := range names {
		att := NamedAttestation{Name: name}
		if np, ok := registry.get(name); ok {
			att.Version = np.Version
			att.Digest = np.ContentDigest()
		} else {
			att.Digest = HashCanonical("unbound-named-predicate", name)
		}
		out = append(out, att)
	}
	return out
}

func mergeNamedAttestations(a, b []NamedAttestation) []NamedAttestation {
	byName := make(map[string]NamedAttestation)
	for _, att := range a {
		byName[att.Name] = att
	}
	for _, att := range b {
		byName[att.Name] = att
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]NamedAttestation, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

// BuildProvenance 同时绑定参考规则链与 DAG。
func BuildProvenance(policy *Policy, dag *DAG, key []byte) (*Certificate, error) {
	return BuildProvenanceWithRegistry(policy, dag, nil, key)
}

// BuildProvenanceWithRegistry 同时绑定规则、DAG 和命名谓词实现摘要。
func BuildProvenanceWithRegistry(policy *Policy, dag *DAG, registry Registry, key []byte) (*Certificate, error) {
	cert, err := BuildCertificateWithRegistry(dag, registry, key)
	if err != nil {
		return nil, err
	}
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		rules := make([]RuleAttestation, 0, len(policy.Rules))
		for i := range policy.Rules {
			r := &policy.Rules[i]
			predDigest := r.When.ContentDigest()
			decisionDigest := HashCanonical("rule-decision", struct {
				Action  Action   `json:"action"`
				Payload Payload  `json:"payload"`
				Audit   []string `json:"audit"`
				Notify  []string `json:"notify"`
			}{r.Action, r.Payload, r.Audit, r.Notify})
			named := CollectNamedNames(r.When)
			ra := RuleAttestation{
				ID:        policy.ruleID(i),
				Index:     i,
				Predicate: predDigest,
				Decision:  decisionDigest,
				NamedDeps: named,
			}
			ra.Digest = HashCanonical("rule-attestation", ra)
			rules = append(rules, ra)
		}
		cert.Manifest.Rules = rules
		var roots []*Pred
		for i := range policy.Rules {
			roots = append(roots, policy.Rules[i].When)
		}
		cert.Manifest.NamedPredicates = mergeNamedAttestations(
			cert.Manifest.NamedPredicates,
			namedAttestations(roots, registry),
		)
		cert.Signature = signManifest(key, cert.Manifest)
	}
	return cert, nil
}

// VerifyCertificate 重算 DAG 证明并校验 HMAC。
func VerifyCertificate(dag *DAG, cert *Certificate, key []byte) error {
	return verifyCertificateWithRegistry(dag, nil, cert, key, false)
}

// VerifyCertificateWithRegistry 额外核对命名谓词实现摘要。
func VerifyCertificateWithRegistry(dag *DAG, registry Registry, cert *Certificate, key []byte) error {
	return verifyCertificateWithRegistry(dag, registry, cert, key, true)
}

func verifyCertificateWithRegistry(dag *DAG, registry Registry, cert *Certificate, key []byte, checkNamed bool) error {
	if cert == nil {
		return fmt.Errorf("policycheck: nil certificate")
	}
	if len(key) > 0 {
		expectedMAC := signManifest(key, cert.Manifest)
		if !equalMAC(expectedMAC, cert.Signature) {
			return fmt.Errorf("policycheck: certificate HMAC verification failed")
		}
	}
	expected, err := BuildCertificateWithRegistry(dag, registry, key)
	if err != nil {
		return err
	}
	if cert.Manifest.DAGDigest != expected.Manifest.DAGDigest {
		return fmt.Errorf("policycheck: DAG digest mismatch: certificate=%s actual=%s",
			cert.Manifest.DAGDigest, expected.Manifest.DAGDigest)
	}
	if cert.Manifest.Root != expected.Manifest.Root {
		return fmt.Errorf("policycheck: root digest mismatch")
	}
	if len(cert.Manifest.Nodes) != len(expected.Manifest.Nodes) {
		return fmt.Errorf("policycheck: node count mismatch")
	}
	for i := range expected.Manifest.Nodes {
		if !reflect.DeepEqual(cert.Manifest.Nodes[i], expected.Manifest.Nodes[i]) {
			return fmt.Errorf("policycheck: node attestation %d was forged or is stale", i)
		}
	}
	if checkNamed && !reflect.DeepEqual(cert.Manifest.NamedPredicates, expected.Manifest.NamedPredicates) {
		return fmt.Errorf("policycheck: named predicate provenance mismatch")
	}
	if len(cert.Manifest.Rules) != 0 {
		// Rule provenance is bound by HMAC; structural equality against a
		// policy is available through VerifyProvenanceRules.
		if len(key) == 0 {
			return fmt.Errorf("policycheck: rule provenance requires HMAC key to be tamper-evident")
		}
	}
	return nil
}

// VerifyProvenanceRules 用当前策略核对证书中的规则溯源项。
func VerifyProvenanceRules(policy *Policy, cert *Certificate, key []byte) error {
	if cert == nil {
		return fmt.Errorf("policycheck: nil certificate")
	}
	expectedRules := map[int]RuleAttestation{}
	if policy != nil {
		tmp, err := BuildProvenance(policy, mustMinimalDAG(), key)
		if err != nil {
			return err
		}
		for _, r := range tmp.Manifest.Rules {
			expectedRules[r.Index] = r
		}
	}
	if len(cert.Manifest.Rules) != len(expectedRules) {
		return fmt.Errorf("policycheck: rule attestation count mismatch")
	}
	for _, got := range cert.Manifest.Rules {
		want, ok := expectedRules[got.Index]
		if !ok || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("policycheck: rule attestation %d mismatch", got.Index)
		}
	}
	if len(key) > 0 {
		expectedMAC := signManifest(key, cert.Manifest)
		if !equalMAC(expectedMAC, cert.Signature) {
			return fmt.Errorf("policycheck: rule provenance HMAC verification failed")
		}
	}
	return nil
}

func mustMinimalDAG() *DAG {
	return &DAG{Root: 0, Nodes: []*DAGNode{{Decision: &Decision{Action: ActionAllow}}}}
}

// NewTraceGraph 从证书构造反查图。
func NewTraceGraph(cert *Certificate) (*TraceGraph, error) {
	if cert == nil {
		return nil, fmt.Errorf("policycheck: nil certificate")
	}
	g := &TraceGraph{
		Certificate: cert,
		byDigest:    make(map[Digest]Attestation),
		byIndex:     make(map[int]Attestation),
		dependents:  make(map[Digest][]int),
	}
	for _, node := range cert.Manifest.Nodes {
		g.byDigest[node.Digest] = node
		g.byIndex[node.DAGIndex] = node
		for _, dep := range node.DependsOn {
			g.dependents[dep] = append(g.dependents[dep], node.DAGIndex)
		}
	}
	return g, nil
}

// BuildTraceGraph 是 BuildCertificate + NewTraceGraph 的便捷方法。
func BuildTraceGraph(dag *DAG) (*TraceGraph, error) {
	cert, err := BuildCertificate(dag, nil)
	if err != nil {
		return nil, err
	}
	return NewTraceGraph(cert)
}

// AffectedByNamed 返回直接或传递依赖指定命名谓词的 DAG 节点下标。
func (g *TraceGraph) AffectedByNamed(name string) []int {
	affected := make(map[int]struct{})
	queue := make([]int, 0)
	for _, node := range g.Certificate.Manifest.Nodes {
		if containsString(node.NamedDeps, name) {
			if _, ok := affected[node.DAGIndex]; !ok {
				affected[node.DAGIndex] = struct{}{}
				queue = append(queue, node.DAGIndex)
			}
		}
	}

	for head := 0; head < len(queue); head++ {
		idx := queue[head]
		node, ok := g.byIndex[idx]
		if !ok {
			continue
		}
		// Any attestation that depends on this node's attestation digest is
		// an ancestor and must also be rehashed/recomputed.
		for _, parentIdx := range g.dependents[node.Digest] {
			if _, ok := affected[parentIdx]; !ok {
				affected[parentIdx] = struct{}{}
				queue = append(queue, parentIdx)
			}
		}
	}

	out := make([]int, 0, len(affected))
	for idx := range affected {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// DirectNamedNodes 返回测试谓词中直接引用 name 的 DAG 节点。
func (g *TraceGraph) DirectNamedNodes(name string) []int {
	var out []int
	for _, node := range g.Certificate.Manifest.Nodes {
		if containsString(node.NamedDeps, name) {
			out = append(out, node.DAGIndex)
		}
	}
	sort.Ints(out)
	return out
}

// ControlReachableIndices 返回直接引用 name 的节点及其全部前向可达
// 后代。它们是命名谓词语义变化后，输入可能改道进入的控制流分支。
func (g *TraceGraph) ControlReachableIndices(name string) []int {
	direct := g.DirectNamedNodes(name)
	seen := make(map[int]struct{})
	queue := append([]int(nil), direct...)
	for _, idx := range direct {
		seen[idx] = struct{}{}
	}
	for head := 0; head < len(queue); head++ {
		node, ok := g.byIndex[queue[head]]
		if !ok {
			continue
		}
		for _, dep := range node.DependsOn {
			child, ok := g.byDigest[dep]
			if !ok {
				continue
			}
			if _, exists := seen[child.DAGIndex]; exists {
				continue
			}
			seen[child.DAGIndex] = struct{}{}
			queue = append(queue, child.DAGIndex)
		}
	}
	out := make([]int, 0, len(seen))
	for idx := range seen {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// AffectedBranchIndices 是 ControlReachableIndices 的术语别名。
func (g *TraceGraph) AffectedBranchIndices(name string) []int {
	return g.ControlReachableIndices(name)
}

// DependsOnNamed 报告节点是否传递依赖某个命名谓词。
func (g *TraceGraph) DependsOnNamed(index int, name string) bool {
	return containsInt(g.AffectedByNamed(name), index)
}

func uniqueSorted(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsInt(items []int, want int) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
