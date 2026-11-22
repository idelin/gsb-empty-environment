package recovery

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

// prekeyTree 表示某设备某一代的一次性公钥承诺树。
type prekeyTree struct {
	device      DeviceID
	epoch       uint64
	leaves      []Hash
	commitments []Hash
}

func buildPrekeyTree(device DeviceID, epoch uint64, commitments []Hash) prekeyTree {
	leaves := make([]Hash, len(commitments))
	for i, c := range commitments {
		leaves[i] = prekeyLeafHash(device, epoch, uint64(i), c)
	}
	return prekeyTree{device: device, epoch: epoch, leaves: leaves, commitments: commitments}
}

func (pt prekeyTree) root() Hash {
	return treeRoot("mth-leaf/prekey/v1", pt.leaves)
}

func (pt prekeyTree) proof(index uint64) PrekeyProof {
	return PrekeyProof{
		Device: pt.device,
		Epoch:  pt.epoch,
		Index:  index,
		Size:   uint64(len(pt.leaves)),
		Leaf:   pt.leaves[index],
		Hashes: prekeyInclusionPath(pt.leaves, int(index)),
	}
}

// branch 是一条带签名检查点链的目录分支。
type branch struct {
	dirPriv ed25519.PrivateKey
	events  []Event
	leaves  []Hash
	cps     []SignedCheckpoint
	tails   map[string]uint64
}

func newBranch(dirPriv ed25519.PrivateKey) *branch {
	b := &branch{dirPriv: dirPriv}
	b.tails = make(map[string]uint64)
	genesis := signCheckpoint(dirPriv, Checkpoint{
		TreeSize:   0,
		RootHash:   treeRoot("mth-leaf/directory/v1", nil),
		DeviceTail: map[string]uint64{},
	})
	b.cps = append(b.cps, genesis)
	return b
}

func (b *branch) appendEvent(e Event) {
	e.GlobalIndex = uint64(len(b.events))
	b.events = append(b.events, e)
	b.leaves = append(b.leaves, logLeafHash(e))
	b.tails[DeviceTailKey(e.Account, e.Device)] = e.GlobalIndex
	root := treeRoot("mth-leaf/directory/v1", b.leaves)
	prev := checkpointRef(b.cps[len(b.cps)-1].Checkpoint)
	cp := signCheckpoint(b.dirPriv, Checkpoint{
		TreeSize:   uint64(len(b.events)),
		RootHash:   root,
		Prev:       prev,
		DeviceTail: cloneTails(b.tails),
	})
	b.cps = append(b.cps, cp)
}

func cloneTails(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (b *branch) head() SignedCheckpoint {
	return b.cps[len(b.cps)-1]
}

// rootAt 返回前 size 个叶子的历史 MTH（size=0 为创世空根）。
func (b *branch) rootAt(size int) Hash {
	return treeRoot("mth-leaf/directory/v1", b.leaves[:size])
}

// eventRecord 生成锚定本分支当前头的事件包含记录。
func (b *branch) eventRecordAt(globalIndex, size int) EventRecord {
	proof := InclusionProof{
		Index:    uint64(globalIndex),
		Size:     uint64(size),
		RootHash: b.rootAt(size),
		Hashes:   logPath(b.leaves, globalIndex, 0, size),
	}
	return EventRecord{Event: b.events[globalIndex], Proof: proof}
}

func (b *branch) eventRecord(globalIndex int) EventRecord {
	return b.eventRecordAt(globalIndex, len(b.leaves))
}

// forkFrom 从某个 size（含该 size 的检查点）复制并分叉，之后追加不同事件。
func (b *branch) forkFrom(size int) *branch {
	fork := &branch{dirPriv: b.dirPriv}
	fork.events = append([]Event(nil), b.events[:size]...)
	fork.leaves = append([]Hash(nil), b.leaves[:size]...)
	fork.cps = append([]SignedCheckpoint(nil), b.cps[:size+1]...)
	fork.tails = cloneTails(b.cps[size].DeviceTail)
	return fork
}

func (b *branch) ingestChain(a *Acceptor) {
	for _, cp := range b.cps {
		if err := a.Ingest(Record{Kind: RecCheckpoint, Checkpoint: &cp}); err != nil {
			panic(err)
		}
	}
}

// witness 模拟离线见证：维护回执稀疏树并对检查点签名。
type witness struct {
	priv     ed25519.PrivateKey
	entries  map[string]Hash
	receipts map[string][]Receipt
	seq      uint64
}

func newWitness(priv ed25519.PrivateKey) *witness {
	return &witness{
		priv:     priv,
		entries:  make(map[string]Hash),
		receipts: make(map[string][]Receipt),
	}
}

func (w *witness) addReceipt(r Receipt) {
	key := string(r.SessionDigest)
	w.receipts[key] = append(w.receipts[key], r)
	w.entries[key] = receiptSetHash(w.receipts[key])
}

func (w *witness) checkpoint(t *testing.T) ReceiptCheckpoint {
	t.Helper()
	w.seq++
	return signReceiptCheckpoint(w.priv, w.seq, smtRoot(w.entries))
}

func (w *witness) proof(session Hash) SparseProof {
	return smtProof(w.entries, session)
}
