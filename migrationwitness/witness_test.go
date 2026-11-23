package migrationwitness

import (
	"crypto/ed25519"
	"testing"
)

type testEnv struct {
	store *TrustStore
	priv  ed25519.PrivateKey
	keyID KeyID
	log   *EvidenceLog
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	store := NewTrustStore()
	key := KeyID("host-1")
	store.Add(key, pub)
	return &testEnv{store: store, priv: priv, keyID: key, log: NewEvidenceLog(store)}
}

func ref(d string, n int64) Ref { return Ref{Digest: Digest(d), Length: n} }

func (e *testEnv) blob(node, digest string, payload []byte) {
	_, err := e.log.AddBlob(Sign(BlobStatement{
		Node: NodeID(node), Ref: ref(digest, int64(len(payload))), Payload: payload,
	}, e.keyID, e.priv))
	if err != nil {
		panic(err)
	}
}

func (e *testEnv) concat(node, digest string, length int64, edges []Ref) {
	_, err := e.log.AddConcat(Sign(ConcatStatement{
		Node: NodeID(node), Ref: ref(digest, length), Edges: edges,
	}, e.keyID, e.priv))
	if err != nil {
		panic(err)
	}
}

func (e *testEnv) alias(from, to Ref) {
	if _, err := e.log.AddAlias(Sign(AliasStatement{From: from, To: to}, e.keyID, e.priv)); err != nil {
		panic(err)
	}
}

func (e *testEnv) receipt(id ReceiptID, node NodeID, edge int, cand NodeID) {
	if _, err := e.log.AddReceipt(Sign(DisambiguationReceipt{
		ID: id, Manifest: node, Edge: edge, Candidate: CandidateID(cand),
	}, e.keyID, e.priv)); err != nil {
		panic(err)
	}
}

func (e *testEnv) withdraw(id ReceiptID) {
	if _, err := e.log.Withdraw(Sign(WithdrawalStatement{Receipt: id}, e.keyID, e.priv)); err != nil {
		panic(err)
	}
}

func (e *testEnv) close() {
	if _, err := e.log.CloseTable(Sign(ClosureReceipt{}, e.keyID, e.priv)); err != nil {
		panic(err)
	}
}

// bruteForce is an independent, unbudgeted enumerator that explores every
// legal assignment. It cross-checks the budgeted DFS evaluator on small graphs.
type bruteForce struct {
	snap    *Snapshot
	choices map[EdgeKey]CandidateID
	onPath  map[NodeID]bool
	memo    map[NodeID]NewID
	root    NodeID
	rootIDs map[string]NewID
}

func bruteEnumerate(snap *Snapshot, root NodeID) (map[string]NewID, bool) {
	b := &bruteForce{
		snap:    snap,
		choices: map[EdgeKey]CandidateID{},
		onPath:  map[NodeID]bool{},
		memo:    map[NodeID]NewID{},
		root:    root,
		rootIDs: map[string]NewID{},
	}
	_, any := b.resolve(root)
	return b.rootIDs, any
}

func (b *bruteForce) resolve(n NodeID) (NewID, bool) {
	if id, ok := b.memo[n]; ok {
		return id, true
	}
	if b.onPath[n] {
		return NewID{}, false
	}
	nd, ok := b.snap.nodes[n]
	if !ok {
		return NewID{}, false
	}
	b.onPath[n] = true
	defer delete(b.onPath, n)

	if nd.kind == KindBlob {
		b.memo[n] = nd.id
		return nd.id, true
	}
	if sumEdges(nd.edges) != nd.ref.Length {
		return NewID{}, false
	}

	var first NewID
	found := false
	b.resolveEdges(n, nd.edges, 0, nil, func(children []NewID) {
		id := ComputeConcatID(nd.ref.Length, children)
		if n == b.root {
			b.rootIDs[id.String()] = id
		} else if !found {
			b.memo[n] = id
		}
		if !found {
			found = true
			first = id
		}
	})
	if !found {
		return NewID{}, false
	}
	return first, true
}

func (b *bruteForce) resolveEdges(owner NodeID, edges []Ref, idx int, acc []NewID, emit func([]NewID)) {
	if idx == len(edges) {
		emit(acc)
		return
	}
	r := edges[idx]
	key := EdgeKey{Manifest: owner, Edge: idx}
	var cands []CandidateID
	if rid, has := b.snap.receiptOf[key]; has {
		cands = []CandidateID{b.snap.receipts[rid].Candidate}
	} else {
		cands = b.snap.expand(r)
	}
	for _, cid := range cands {
		cand, ok := b.snap.candidate(cid)
		if !ok || cand.Ref.Length != r.Length {
			continue
		}
		if prior, conflict := b.choices[key]; conflict && prior != cid {
			continue
		}
		b.choices[key] = cid
		id, ok := b.resolve(cand.nodeID)
		if !ok {
			delete(b.choices, key)
			continue
		}
		b.resolveEdges(owner, edges, idx+1, append(acc, id), emit)
		delete(b.choices, key)
	}
}

func assertStatus(t *testing.T, got Status, want Status) {
	t.Helper()
	if got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func fmtIDs(ids []NewID) string {
	out := ""
	for _, id := range ids {
		out += " " + id.String()
	}
	return out
}
