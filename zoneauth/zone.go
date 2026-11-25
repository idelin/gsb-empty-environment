package zoneauth

import (
	"crypto/ed25519"
	"errors"
)

// Zone is the manager-side full zone tree. It is a committed materialized
// tree: each node stores both its content and the hashes of its children, so
// any subtree can be exported as a self-contained partial proof.
type Zone struct {
	name      string
	nodes     map[string]*TrieNode
	sequence  uint64
	key       ed25519.PrivateKey
	prevKeyID string
}

// NewZone creates an empty apex zone signed by key.
func NewZone(name string, key ed25519.PrivateKey) *Zone {
	z := &Zone{
		name:  name,
		nodes: map[string]*TrieNode{},
		key:   key,
	}
	z.nodes[""] = &TrieNode{Path: Name{}, Children: map[string][]byte{}}
	return z
}

// Sequence returns the current committed revision number.
func (z *Zone) Sequence() uint64 { return z.sequence }

// Name returns the zone name.
func (z *Zone) Name() string { return z.name }

// PublicKey returns the current signing public key.
func (z *Zone) PublicKey() ed25519.PublicKey {
	return z.key.Public().(ed25519.PublicKey)
}

func (z *Zone) node(name Name) *TrieNode { return z.nodes[name.Key()] }

func (z *Zone) ensurePath(prefix Name) (*TrieNode, error) {
	cur := z.nodes[""]
	for depth := 1; depth <= len(prefix); depth++ {
		if cur.Delegate {
			return nil, errors.New("zoneauth: cannot traverse delegation cut")
		}
		label := prefix[depth-1]
		childName := prefix[:depth]
		child := z.node(childName)
		if child == nil {
			child = &TrieNode{
				Path:     childName.Clone(),
				Children: map[string][]byte{},
			}
			z.nodes[childName.Key()] = child
			cur.Children[label] = nil
		}
		cur = child
	}
	return cur, nil
}

// PutRecords creates intermediate nodes as needed and merges records into the
// target node. Records are stored as a set keyed by (type, value).
func (z *Zone) PutRecords(name Name, recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	target, err := z.ensurePath(name)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		found := false
		for _, existing := range target.Records {
			if existing == rec {
				found = true
				break
			}
		}
		if !found {
			target.Records = append(target.Records, rec)
		}
	}
	z.recompute()
	return nil
}

// AddDelegation creates (or reuses) name and marks it as a cut with the
// supplied NS records. Any descendant material becomes unreachable and is
// removed so the tree remains internally consistent.
func (z *Zone) AddDelegation(name Name, ns []Record) error {
	if len(name) == 0 {
		return errors.New("zoneauth: cannot delegate the apex")
	}
	target, err := z.ensurePath(name)
	if err != nil {
		return err
	}
	for child := range target.Children {
		delete(target.Children, child)
	}
	for key, n := range z.nodes {
		if n != target && n.Path.HasPrefix(name) {
			delete(z.nodes, key)
		}
	}
	target.Delegate = true
	target.Records = append([]Record(nil), ns...)
	target.Children = map[string][]byte{}
	z.recompute()
	return nil
}

// RemoveDelegation lifts the cut at name. The node survives as a regular
// empty node unless content is added by the caller.
func (z *Zone) RemoveDelegation(name Name) error {
	target := z.node(name)
	if target == nil {
		return errors.New("zoneauth: delegation does not exist")
	}
	if !target.Delegate {
		return errors.New("zoneauth: name is not a delegation")
	}
	target.Delegate = false
	target.Records = nil
	z.recompute()
	return nil
}

// SetWildcard installs, replaces or removes (nil recs) the wildcard template
// directly beneath parent.
func (z *Zone) SetWildcard(parent Name, recs []Record) error {
	anchor, err := z.ensurePath(parent)
	if err != nil {
		return err
	}
	if anchor.Delegate {
		return errors.New("zoneauth: cannot place a wildcard beneath a delegation")
	}
	wildName := parent.With("*")
	if len(recs) == 0 {
		if z.node(wildName) != nil {
			delete(z.nodes, wildName.Key())
			delete(anchor.Children, "*")
		}
		z.recompute()
		return nil
	}
	wild := z.node(wildName)
	if wild == nil {
		wild = &TrieNode{Path: wildName, Children: map[string][]byte{}}
		z.nodes[wildName.Key()] = wild
	}
	wild.Delegate = false
	wild.Records = append([]Record(nil), recs...)
	anchor.Children["*"] = nil
	z.recompute()
	return nil
}

// DeleteSubtree removes name and everything beneath it.
func (z *Zone) DeleteSubtree(name Name) error {
	if len(name) == 0 {
		return errors.New("zoneauth: cannot delete the apex")
	}
	if z.node(name) == nil {
		return errors.New("zoneauth: node does not exist")
	}
	parent := z.node(name.Parent())
	delete(parent.Children, name[len(name)-1])
	for key, n := range z.nodes {
		if n.Path.HasPrefix(name) {
			delete(z.nodes, key)
		}
	}
	z.recompute()
	return nil
}

// recompute walks the tree in depth order and refreshes every parent child
// hash, returning the fresh root hash.
func (z *Zone) recompute() []byte {
	root := z.nodes[""]
	var walk func(*TrieNode)
	walk = func(n *TrieNode) {
		for label := range n.Children {
			child := z.node(n.Path.With(label))
			if child == nil {
				continue
			}
			walk(child)
		}
		// Refresh edge hashes only after every subtree is finalized, so a
		// deep mutation propagates all the way to the root in one pass.
		for label := range n.Children {
			child := z.node(n.Path.With(label))
			if child == nil {
				continue
			}
			n.Children[label] = HashNode(child)
		}
	}
	walk(root)
	return HashNode(root)
}

// Commit recomputes all hashes, bumps the sequence and returns the signed
// root envelope. The key currently held by the zone performs the signing.
func (z *Zone) Commit() *RootEnvelope {
	z.sequence++
	env := &RootEnvelope{
		Zone:          z.name,
		Sequence:      z.sequence,
		PreviousKeyID: z.prevKeyID,
		RootHash:      z.recompute(),
	}
	SignRootEnvelope(env, z.key)
	return env
}

// RootHash returns the current root hash without changing the sequence.
func (z *Zone) RootHash() []byte { return z.recompute() }

// Sync forces the in-memory tree and all edge hashes into a consistent
// committed state without advancing the sequence. Hosts call it after a
// batch of direct mutations before constructing a patch builder.
func (z *Zone) Sync() { z.recompute() }

// Clone returns an independent deep copy of the zone, including its key
// material and sequence. Builders use it to freeze the pre-patch state for
// old-root proofs while continuing to mutate the live tree.
func (z *Zone) Clone() *Zone {
	z.recompute()
	cp := &Zone{
		name:      z.name,
		nodes:     make(map[string]*TrieNode, len(z.nodes)),
		sequence:  z.sequence,
		key:       append(ed25519.PrivateKey(nil), z.key...),
		prevKeyID: z.prevKeyID,
	}
	for key, node := range z.nodes {
		cp.nodes[key] = node.Clone()
	}
	return cp
}

// BuildHandoffAt signs a key rollover with the OLD key. It binds the exact
// pre-mutation oldRootHash and post-mutation newRootHash, then installs
// newKey so the new root envelope can be signed by it at the same sequence.
func (z *Zone) BuildHandoffAt(newKey ed25519.PrivateKey, oldRootHash, newRootHash []byte, sequence uint64) *HandoffEnvelope {
	oldPub := z.PublicKey()
	handoff := &HandoffEnvelope{
		Zone:        z.name,
		Sequence:    sequence,
		NewKey:      append(ed25519.PublicKey(nil), newKey.Public().(ed25519.PublicKey)...),
		OldRootHash: append([]byte(nil), oldRootHash...),
		NewRootHash: append([]byte(nil), newRootHash...),
	}
	SignHandoff(handoff, z.key)
	z.prevKeyID = KeyIDFor(oldPub)
	z.key = newKey
	return handoff
}

// CommitAt signs the current tree at an explicit sequence number without
// bumping it. Patch builders use it after BuildHandoff, where the handoff
// envelope already pins the new sequence.
func (z *Zone) CommitAt(sequence uint64) *RootEnvelope {
	z.sequence = sequence
	env := &RootEnvelope{
		Zone:          z.name,
		Sequence:      sequence,
		PreviousKeyID: z.prevKeyID,
		RootHash:      z.recompute(),
	}
	SignRootEnvelope(env, z.key)
	return env
}

// BuildProof exports the minimum committed node chain needed to answer query.
// It walks apex -> closest existing ancestor, stops at delegation cuts, and
// attaches the wildcard template only when it genuinely exists. No absent
// sibling is ever synthesized.
func (z *Zone) BuildProof(query Name) *Proof {
	proof := &Proof{Query: query.Clone()}
	cur := z.nodes[""]
	proof.Nodes = append(proof.Nodes, cur.Clone())
	for depth := 1; depth <= len(query); depth++ {
		if cur.Delegate {
			return proof
		}
		label := query[depth-1]
		child := z.node(query[:depth])
		if child == nil {
			if _, bound := cur.Children[label]; bound {
				panic("zoneauth: dangling child hash in committed tree")
			}
			break
		}
		cur = child
		proof.Nodes = append(proof.Nodes, cur.Clone())
		if cur.Delegate {
			return proof
		}
	}
	if !cur.Delegate && len(cur.Path) < len(query) {
		if wild := z.node(cur.Path.With("*")); wild != nil {
			proof.WildcardNode = wild.Clone()
		}
	}
	return proof
}

// SnapshotNode exports an independent copy of a committed node. Patch
// construction uses it to capture old-root and new-root evidence explicitly.
func (z *Zone) SnapshotNode(name Name) *TrieNode {
	n := z.node(name)
	if n == nil {
		return nil
	}
	return n.Clone()
}
