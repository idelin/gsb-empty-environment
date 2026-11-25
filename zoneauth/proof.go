package zoneauth

import (
	"bytes"
	"errors"
)

// AnswerKind enumerates the authorization conclusions a cache can hold.
type AnswerKind string

const (
	// AnswerPositive: the queried name exists and carries the returned records.
	AnswerPositive AnswerKind = "positive"
	// AnswerReferral: resolution crossed a delegation cut at Cut.
	AnswerReferral AnswerKind = "referral"
	// AnswerNoData: the name exists but has no records of the requested type.
	AnswerNoData AnswerKind = "nodata"
	// AnswerNXDomain: no name exists for the query (including wildcard denial).
	AnswerNXDomain AnswerKind = "nxdomain"
)

// Answer is the authorization result of one cache query.
type Answer struct {
	Kind    AnswerKind
	Records []Record
	// Cut is the delegation name for referrals, otherwise nil.
	Cut Name
	// Closest is the deepest existing ancestor of the query.
	Closest Name
	// ViaWildcard is set when the conclusion was synthesized through the
	// wildcard template beneath Closest.
	ViaWildcard bool
}

// Proof is a partial, client-held proof for a single query. Nodes run from
// the apex down to the closest existing ancestor. That final node's
// authenticated children map proves that the next query label is absent, and
// WildcardNode carries the template node when the template exists.
type Proof struct {
	Query Name `json:"query"`
	// Nodes contains the committed trie nodes on the apex..closest chain, in
	// that order. It never terminates inside a delegation: the delegation cut
	// node is the last node for a referral.
	Nodes []*TrieNode `json:"nodes"`
	// WildcardNode is the committed "*" child of the closest encloser,
	// present only when the template actually exists.
	WildcardNode *TrieNode `json:"wildcard_node,omitempty"`
	// incomplete marks client-synthesized proofs whose anchor commits an
	// edge whose node evidence was not supplied. It is never serialized.
	incomplete bool
}

// Clone returns an independent deep copy of the proof.
func (p *Proof) Clone() *Proof {
	cp := &Proof{Query: p.Query.Clone(), WildcardNode: p.WildcardNode.Clone()}
	for _, node := range p.Nodes {
		cp.Nodes = append(cp.Nodes, node.Clone())
	}
	return cp
}

func (p *Proof) rootNode() *TrieNode {
	if len(p.Nodes) == 0 {
		return nil
	}
	return p.Nodes[0]
}

func (p *Proof) closestNode() *TrieNode {
	if len(p.Nodes) == 0 {
		return nil
	}
	return p.Nodes[len(p.Nodes)-1]
}

// VerifyStructural recomputes every node hash and checks the parent-child
// bindings, the apex root hash and (when present) the wildcard template
// binding. It does not check the signature; callers pair it with
// VerifyEnvelope. Missing siblings stay missing in the children map: the
// verifier never substitutes an empty hash for a label the proof omits.
func (p *Proof) VerifyStructural(rootHash []byte) error {
	if len(p.Nodes) == 0 {
		return errors.New("zoneauth: proof contains no nodes")
	}
	apex := p.Nodes[0]
	if len(apex.Path) != 0 {
		return errors.New("zoneauth: proof does not start at the apex")
	}
	if !bytes.Equal(HashNode(apex), rootHash) {
		return errors.New("zoneauth: apex node hash does not match root")
	}
	for idx, node := range p.Nodes {
		// A wildcard proof queries "<anchor>/*", whose final label does not
		// appear as a regular path node: the chain may legitimately stop at
		// the anchor, while every other chain node must lie on the query.
		if len(node.Path) > 0 && !p.Query.HasPrefix(node.Path) {
			return errors.New("zoneauth: proof node lies outside the query path")
		}
		if idx > 0 {
			parent := p.Nodes[idx-1]
			if len(node.Path) != len(parent.Path)+1 {
				return errors.New("zoneauth: non-adjacent proof chain")
			}
			bound, ok := parent.Children[node.Path[len(node.Path)-1]]
			if !ok || !bytes.Equal(bound, HashNode(node)) {
				return errors.New("zoneauth: child hash not authenticated by parent")
			}
		}
	}
	closest := p.closestNode()
	if p.WildcardNode != nil {
		bound, ok := closest.Children["*"]
		if !ok || !bytes.Equal(bound, HashNode(p.WildcardNode)) {
			return errors.New("zoneauth: wildcard node not authenticated by closest encloser")
		}
		if !p.WildcardNode.Path.Equal(closest.Path.With("*")) {
			return errors.New("zoneauth: wildcard node path mismatch")
		}
	}
	return nil
}

// DeriveAnswer evaluates a structurally verified partial proof against its
// query and returns the authenticated answer. The proof must end at the
// closest existing ancestor of the query; all label ancestry is structural.
func (p *Proof) DeriveAnswer() (*Answer, error) {
	if err := p.validateShape(); err != nil {
		return nil, err
	}
	closest := p.closestNode()
	answer := &Answer{Closest: closest.Path.Clone()}

	if closest.Delegate {
		answer.Kind = AnswerReferral
		answer.Cut = closest.Path.Clone()
		answer.Records = append([]Record(nil), closest.Records...)
		return answer, nil
	}

	if len(closest.Path) == len(p.Query) {
		answer.Kind = AnswerNoData
		answer.Records = append([]Record(nil), closest.Records...)
		return answer, nil
	}

	nextLabel := p.Query[len(closest.Path)]
	if _, exists := closest.Children[nextLabel]; exists {
		return nil, errors.New("zoneauth: proof stops before an existing child (incomplete evidence)")
	}

	// A wildcard only covers exactly one label beneath its parent.
	if len(p.Query) == len(closest.Path)+1 && p.WildcardNode != nil {
		answer.Kind = AnswerPositive
		answer.ViaWildcard = true
		answer.Records = append([]Record(nil), p.WildcardNode.Records...)
		return answer, nil
	}

	answer.Kind = AnswerNXDomain
	return answer, nil
}

func (p *Proof) validateShape() error {
	if len(p.Nodes) == 0 {
		return errors.New("zoneauth: proof contains no nodes")
	}
	closest := p.closestNode()
	if len(closest.Path) > len(p.Query) {
		return errors.New("zoneauth: proof deeper than its query")
	}
	if !p.Query.HasPrefix(closest.Path) {
		return errors.New("zoneauth: closest encloser is not a query ancestor")
	}
	if len(closest.Path) < len(p.Query) {
		nextLabel := p.Query[len(closest.Path)]
		if _, exists := closest.Children[nextLabel]; exists {
			return errors.New("zoneauth: missing child node in proof chain")
		}
	}
	if p.incomplete {
		return errors.New("zoneauth: authenticated edge lacks node evidence")
	}
	return nil
}
