package zoneauth

import (
	"bytes"
	"crypto/ed25519"
	"errors"
)

// VerdictKind tells the cache what to do with a previously cached query.
type VerdictKind string

const (
	// VerdictReusable: the new-root evidence reproduces the same answer; the
	// cached entry stays valid.
	VerdictReusable VerdictKind = "reusable"
	// VerdictChanged: the new-root evidence produces a different answer; the
	// cache must replace the entry (including negative -> positive/referral).
	VerdictChanged VerdictKind = "changed"
	// VerdictPending: no authenticated evidence covers the query under the new
	// root. The entry is neither kept nor negated; a fresh query is required.
	VerdictPending VerdictKind = "pending"
)

// CachedQuery is everything a client retained from the previous round for
// one query: the query name, the authorization answer and its partial proof.
type CachedQuery struct {
	Query  Name
	Answer *Answer
	Proof  *Proof
}

// Verdict is the re-evaluation outcome for one cached query.
type Verdict struct {
	Query Name
	Kind  VerdictKind
	// NewAnswer is set for Reusable and Changed verdicts.
	NewAnswer *Answer
	Reason    string
}

// ErrPendingEvidence marks a path walk that needs a node the patch did not
// authenticate. It is deliberately distinct from NXDomain: an unprovided
// sibling is never treated as an empty tree.
var ErrPendingEvidence = errors.New("zoneauth: new-root evidence does not cover this query")

// nodeView holds the two evidence pools a client can draw from. New-evidence
// nodes are directly authenticated by the new root; old-evidence nodes may be
// adopted only by matching a hash edge that a new-root parent commits.
type nodeView struct {
	newByKey map[string]*TrieNode
	oldByKey map[string]*TrieNode
}

func buildView(newProofs, oldProofs []*Proof, newApex *TrieNode) *nodeView {
	view := &nodeView{
		newByKey: map[string]*TrieNode{},
		oldByKey: map[string]*TrieNode{},
	}
	if newApex != nil {
		view.newByKey[""] = newApex
	}
	for _, proof := range newProofs {
		if proof == nil {
			continue
		}
		for _, node := range proof.Nodes {
			view.newByKey[node.Path.Key()] = node
		}
		if proof.WildcardNode != nil {
			view.newByKey[proof.WildcardNode.Path.Key()] = proof.WildcardNode
		}
	}
	for _, proof := range oldProofs {
		if proof == nil {
			continue
		}
		for _, node := range proof.Nodes {
			view.oldByKey[node.Path.Key()] = node
		}
	}
	return view
}

// adoptNode resolves a child by label beneath parent. New-root evidence wins;
// an old-evidence node is returned only when its hash equals the edge the new
// parent commits, so stale or modified subtrees can never be adopted.
func (v *nodeView) adoptNode(parent *TrieNode, label string) (*TrieNode, bool) {
	childName := parent.Path.With(label)
	key := childName.Key()
	if node, ok := v.newByKey[key]; ok {
		return node, true
	}
	edgeHash, bound := parent.Children[label]
	if !bound {
		return nil, false
	}
	if node, ok := v.oldByKey[key]; ok && bytes.Equal(HashNode(node), edgeHash) {
		return node, true
	}
	return nil, true // edge exists but evidence is missing -> pending
}

// Reevaluate verifies the patch and re-evaluates every cached query against
// the new root using only evidence the client holds. Ancestry is label
// structural throughout; changes at the closest existing ancestor propagate
// exactly to its subtree rather than to string-prefix neighbors.
func Reevaluate(p *Patch, cached *RootEnvelope, trusted map[string]ed25519.PublicKey, entries []CachedQuery) ([]Verdict, *VerifiedRoot, error) {
	verified, err := VerifyPatch(p, cached, trusted)
	if err != nil {
		return nil, nil, err
	}
	return ReevaluateVerified(p, entries), verified, nil
}

// ReevaluateVerified runs only the evidence-driven part of re-evaluation
// against an already authenticated patch. Hosts use it to model clients that
// received a valid patch but retain only a subset of its evidence: an edge
// whose node is missing yields Pending, never a fabricated NXDomain.
func ReevaluateVerified(p *Patch, entries []CachedQuery) []Verdict {
	newProofs := make([]*Proof, 0, len(p.Ops))
	oldProofs := make([]*Proof, 0, len(p.Ops)+len(entries))
	for i := range p.Ops {
		newProofs = append(newProofs, p.Ops[i].NewProof)
		oldProofs = append(oldProofs, p.Ops[i].OldProof)
	}
	newProofs = append(newProofs, p.ClientProofs...)
	for _, entry := range entries {
		if entry.Proof != nil {
			oldProofs = append(oldProofs, entry.Proof)
		}
	}
	view := buildView(newProofs, oldProofs, p.NewApex)

	verdicts := make([]Verdict, 0, len(entries))
	for _, entry := range entries {
		verdicts = append(verdicts, evaluateEntry(entry, p, view))
	}
	return verdicts
}

func evaluateEntry(entry CachedQuery, p *Patch, view *nodeView) Verdict {
	verdict := Verdict{Query: entry.Query.Clone()}

	// Walk the query path apex-first over authenticated nodes only.
	cur := view.newByKey[""]
	if cur == nil || !bytes.Equal(HashNode(cur), p.NewRoot.RootHash) {
		verdict.Kind = VerdictPending
		verdict.Reason = "new apex node missing from evidence"
		return verdict
	}

	depth := 0
	for depth < len(entry.Query) {
		if cur.Delegate {
			break
		}
		label := entry.Query[depth]
		edgeHash, bound := cur.Children[label]
		if !bound {
			break // authenticated missing edge at the closest encloser
		}
		next, edgeExists := view.adoptNode(cur, label)
		if !edgeExists {
			break
		}
		if next == nil {
			// The edge exists and is committed, but its subtree was not
			// supplied: pending rather than assumed empty.
			verdict.Kind = VerdictPending
			verdict.Reason = "child edge authenticated but node evidence not provided"
			return verdict
		}
		if !bytes.Equal(HashNode(next), edgeHash) {
			verdict.Kind = VerdictPending
			verdict.Reason = "available node does not match new-root edge hash"
			return verdict
		}
		cur = next
		depth++
	}

	// Reconstruct a minimum partial proof under the new root from adopted
	// nodes, then derive the answer with the same logic as the interpreter.
	synth := synthesizeProof(entry.Query, cur, view)
	newAnswer, err := synth.DeriveAnswer()
	if err != nil {
		verdict.Kind = VerdictPending
		verdict.Reason = err.Error()
		return verdict
	}

	verdict.NewAnswer = newAnswer
	if answersEquivalent(entry.Answer, newAnswer) {
		verdict.Kind = VerdictReusable
		verdict.Reason = "new-root evidence reproduces the cached answer"
	} else {
		verdict.Kind = VerdictChanged
		verdict.Reason = changeReason(entry.Answer, newAnswer)
	}
	return verdict
}

// synthesizeProof assembles the apex..closest chain visible in the view and
// attaches a wildcard node only when the anchor's committed children map
// authenticates the "*" edge and that node is actually present in evidence.
func synthesizeProof(query Name, closest *TrieNode, view *nodeView) *Proof {
	chain := make([]*TrieNode, 0, len(closest.Path)+1)
	for depth := 0; depth <= len(closest.Path); depth++ {
		node := view.newByKey[query[:depth].Key()]
		if node == nil {
			// Adopted old-root node: resolve through the parent edge.
			if depth > 0 {
				parent := chain[depth-1]
				node, _ = view.adoptNode(parent, query[depth-1])
			}
		}
		if node == nil {
			break
		}
		chain = append(chain, node)
	}
	proof := &Proof{Query: query.Clone(), Nodes: chain}
	if !closest.Delegate && len(query) == len(closest.Path)+1 {
		if wildHash, bound := closest.Children["*"]; bound {
			key := closest.Path.With("*").Key()
			// Only new-root template evidence is valid: an old wildcard may
			// have changed, so adopting it by edge presence alone would
			// silently reuse stale content.
			if wild := view.newByKey[key]; wild != nil && bytes.Equal(HashNode(wild), wildHash) {
				proof.WildcardNode = wild
			} else {
				// The anchor commits a "*" edge whose node evidence is not
				// available. Concluding NXDomain here would silently treat
				// unprovided evidence as an empty tree.
				proof.incomplete = true
			}
		}
	}
	return proof
}

func answersEquivalent(old, now *Answer) bool {
	if old == nil || now == nil || old.Kind != now.Kind {
		return false
	}
	if !old.Closest.Equal(now.Closest) {
		return false
	}
	if old.Kind == AnswerReferral && !old.Cut.Equal(now.Cut) {
		return false
	}
	return recordsEqual(old.Records, now.Records)
}

func changeReason(old, now *Answer) string {
	return "answer changed from " + string(old.Kind) + " to " + string(now.Kind)
}
