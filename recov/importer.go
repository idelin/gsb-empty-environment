package recov

import (
	"crypto/ed25519"
	"sort"
)

// LogID 常量区分两条日志。
const (
	DirectoryLogID = "directory"
	ReceiptLogID   = "receipts"
)

// Importer 是宿主侧的增量导入与判定引擎。
// 记录可以任意顺序到达（离线见证可能先收到回执、后收到目录前缀），
// 导入器只做存储与校验，结论在 Evaluate 时按当前已到证据给出。
type Importer struct {
	dirVerifier ed25519.PublicKey
	witVerifier ed25519.PublicKey

	// 目录日志：按检查点规模与根哈希保存已验签检查点，事件按序号去重存储。
	dirCheckpoints map[uint64]map[string]SignedCheckpoint
	dirEvents      map[uint64]map[string]*Event // seq -> leafHash -> event（保留分叉内容）

	// OPK 承诺树内容，按其规范哈希索引。
	opkBlocks map[string]*OPKBlock

	// 回执日志：检查点与回执叶子分别去重存储。
	witCheckpoints map[uint64]map[string]SignedCheckpoint
	receipts       map[uint64]map[string]*Receipt // seq -> leafHash -> receipt

	// 待判定信封按会话标识索引。
	envelopes map[string]*SessionEnvelope
}

// NewImporter 用宿主信任的目录与见证公钥构造导入器。
func NewImporter(directoryVerifier, witnessVerifier ed25519.PublicKey) *Importer {
	return &Importer{
		dirVerifier:    clone(directoryVerifier),
		witVerifier:    clone(witnessVerifier),
		dirCheckpoints: map[uint64]map[string]SignedCheckpoint{},
		dirEvents:      map[uint64]map[string]*Event{},
		witCheckpoints: map[uint64]map[string]SignedCheckpoint{},
		receipts:       map[uint64]map[string]*Receipt{},
		opkBlocks:      map[string]*OPKBlock{},
		envelopes:      map[string]*SessionEnvelope{},
	}
}

// ImportDirectoryCheckpoint 校验并保存一条目录检查点。
func (im *Importer) ImportDirectoryCheckpoint(sc SignedCheckpoint) error {
	if sc.Checkpoint.LogID != DirectoryLogID {
		return wrapErr(ErrMalformed, "expected directory log, got %q", sc.Checkpoint.LogID)
	}
	if err := verifySignedCheckpoint(sc, im.dirVerifier); err != nil {
		return err
	}
	im.putCheckpoint(im.dirCheckpoints, sc)
	return nil
}

// ImportWitnessCheckpoint 校验并保存一条回执日志检查点。
func (im *Importer) ImportWitnessCheckpoint(sc SignedCheckpoint) error {
	if sc.Checkpoint.LogID != ReceiptLogID {
		return wrapErr(ErrMalformed, "expected receipts log, got %q", sc.Checkpoint.LogID)
	}
	if err := verifySignedCheckpoint(sc, im.witVerifier); err != nil {
		return err
	}
	im.putCheckpoint(im.witCheckpoints, sc)
	return nil
}

func (im *Importer) putCheckpoint(store map[uint64]map[string]SignedCheckpoint, sc SignedCheckpoint) {
	key := string(sc.Checkpoint.TreeRoot)
	if store[sc.Checkpoint.TreeSize] == nil {
		store[sc.Checkpoint.TreeSize] = map[string]SignedCheckpoint{}
	}
	store[sc.Checkpoint.TreeSize][key] = sc
}

// ImportEvent 保存一条目录事件（按序号去重；内容冲突由树根校验负责）。
func (im *Importer) ImportEvent(ev *Event) error {
	if im.dirEvents[ev.Seq] == nil {
		im.dirEvents[ev.Seq] = map[string]*Event{}
	}
	im.dirEvents[ev.Seq][string(EventHash(ev))] = cloneEvent(ev)
	return nil
}

// ImportOPKBlock 保存一棵 OPK 承诺树，并自校验其根与叶子承诺一致。
func (im *Importer) ImportOPKBlock(b *OPKBlock) error {
	if uint32(len(b.LeafKeys)) != b.LeafCount {
		return wrapErr(ErrMalformed, "opk block leaf count mismatch")
	}
	leafData := make([][]byte, b.LeafCount)
	for i, keyPt := range b.LeafKeys {
		pub, err := UnmarshalECDHPublic(keyPt)
		if err != nil {
			return err
		}
		leafData[i] = OPKCommitment(pub, uint32(i), b.Instance, b.Account)
	}
	root := MTH(leafData)
	if !equalBytes(root, b.Root) {
		return wrapErr(ErrMalformed, "opk block root does not match leaf commitments")
	}
	im.opkBlocks[string(OPKBlockHash(b))] = cloneBlock(b)
	return nil
}

// ImportReceipt 保存一条回执叶子（按序号去重）。
func (im *Importer) ImportReceipt(r *Receipt) error {
	if im.receipts[r.Seq] == nil {
		im.receipts[r.Seq] = map[string]*Receipt{}
	}
	im.receipts[r.Seq][string(ReceiptHash(r))] = cloneReceipt(r)
	return nil
}

// ImportEnvelope 登记一个待判定的会话信封。
func (im *Importer) ImportEnvelope(env *SessionEnvelope) error {
	sid := DeriveSessionID(env.HandshakeDigest, env.OPKCommitment, env.Instance, env.Account)
	im.envelopes[string(sid)] = cloneEnvelope(env)
	return nil
}

// ---- 前缀补全与分支重建 ----

// hasPrefix 判断 [0,size) 的事件/回执是否全部到齐。
func (im *Importer) hasDirPrefix(size uint64) bool {
	for i := uint64(0); i < size; i++ {
		if len(im.dirEvents[i]) == 0 {
			return false
		}
	}
	return true
}

func (im *Importer) hasReceiptPrefix(size uint64) bool {
	for i := uint64(0); i < size; i++ {
		if len(im.receipts[i]) == 0 {
			return false
		}
	}
	return true
}

// dirBranches 重建在 sendSize 处所有“可成立”的目录分支。
// 可成立 = 存在已验签检查点，且对应事件前缀已到齐，并能从叶子中选出一套
// 与该检查点根完全匹配的事件序列。冲突后缀会让同规模出现多个根，从而产生多分支。
func (im *Importer) dirBranches(sendSize uint64) ([]*branchState, error) {
	candidates := im.dirCheckpoints[sendSize]
	if len(candidates) == 0 {
		return nil, wrapErr(ErrMissingInclusion, "no signed directory checkpoint at size %d", sendSize)
	}
	if !im.hasDirPrefix(sendSize) {
		return nil, wrapErr(ErrPending, "directory prefix [0,%d) not fully delivered", sendSize)
	}
	var branches []*branchState
	for _, sc := range candidates {
		events, ok := im.matchDirPrefix(sendSize, sc.Checkpoint.TreeRoot)
		if !ok {
			continue // 已到齐的叶子拼不出该检查点根：该分支当前不成立
		}
		st, err := foldEvents(events)
		if err != nil {
			return nil, err
		}
		st.root = sc.Checkpoint.TreeRoot
		branches = append(branches, st)
	}
	if len(branches) == 0 {
		return nil, wrapErr(ErrMissingInclusion,
			"delivered prefix matches none of the signed checkpoints at size %d", sendSize)
	}
	return branches, nil
}

// matchDirPrefix 在每个序号可能有多份候选叶子（分叉）的情况下，
// 选出一套与目标根一致的事件序列。小目录采用带回溯的匹配。
func (im *Importer) matchDirPrefix(size uint64, targetRoot []byte) ([]*Event, bool) {
	chosen := make([]*Event, size)
	var search func(pos uint64) bool
	search = func(pos uint64) bool {
		if pos == size {
			leafData := make([][]byte, size)
			for i, ev := range chosen {
				leafData[i] = EventHash(ev)
			}
			return equalBytes(MTH(leafData), targetRoot)
		}
		// 候选按叶子哈希排序，保证回放确定性。
		hashes := make([]string, 0, len(im.dirEvents[pos]))
		for h := range im.dirEvents[pos] {
			hashes = append(hashes, h)
		}
		sort.Strings(hashes)
		for _, h := range hashes {
			chosen[pos] = im.dirEvents[pos][h]
			if search(pos + 1) {
				return true
			}
		}
		return false
	}
	if !search(0) {
		return nil, false
	}
	return chosen, true
}

// receiptBranches 重建回执日志在所有已到齐且验签的检查点上的分支。
func (im *Importer) receiptBranches() ([][]*Receipt, error) {
	sizes := sortedUint64Keys(im.witCheckpoints)
	var out [][]*Receipt
	for _, size := range sizes {
		if !im.hasReceiptPrefix(size) {
			continue
		}
		for _, sc := range im.witCheckpoints[size] {
			leaves, ok := im.matchReceiptPrefix(size, sc.Checkpoint.TreeRoot)
			if ok {
				// 同规模的每个成立分支（冲突后缀）都要纳入，以发现跨分支双花。
				out = append(out, leaves)
			}
		}
	}
	return out, nil
}

func (im *Importer) matchReceiptPrefix(size uint64, targetRoot []byte) ([]*Receipt, bool) {
	chosen := make([]*Receipt, size)
	var search func(pos uint64) bool
	search = func(pos uint64) bool {
		if pos == size {
			leafData := make([][]byte, size)
			for i, r := range chosen {
				leafData[i] = ReceiptHash(r)
			}
			return equalBytes(MTH(leafData), targetRoot)
		}
		hashes := make([]string, 0, len(im.receipts[pos]))
		for h := range im.receipts[pos] {
			hashes = append(hashes, h)
		}
		sort.Strings(hashes)
		for _, h := range hashes {
			chosen[pos] = im.receipts[pos][h]
			if search(pos + 1) {
				return true
			}
		}
		return false
	}
	if !search(0) {
		return nil, false
	}
	return chosen, true
}

func sortedUint64Keys(m map[uint64]map[string]SignedCheckpoint) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
