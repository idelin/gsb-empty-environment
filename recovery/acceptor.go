package recovery

import (
	"bytes"
	"crypto/ed25519"
	"sort"
)

// Acceptor 是消息宿主导入的历史会话恢复验收器。
//
// 记录可乱序到达（离线见证可能先拿到回执，数月后才拿到目录前缀）。
// Ingest 只做单条自洽校验并缓存；跨记录判定都发生在 Evaluate，
// 因此不会因为到达顺序而拒绝任何本来成立的恢复。
type Acceptor struct {
	anchor   TrustAnchor
	callback RecoveryCallback

	checkpoints map[string]*SignedCheckpoint
	// 同一事件在不同分叉下可能有不同包含路径，按“事件摘要|树根”多版本保存。
	events       map[string]*EventRecord
	prekeys      map[string]*PrekeyProof
	receiptCPs   map[uint64]*ReceiptCheckpoint
	sparseProofs map[string]*SparseProof
	receipts     map[string]*Receipt
}

// NewAcceptor 用带外信任锚创建验收器。callback 可为 nil。
func NewAcceptor(anchor TrustAnchor, callback RecoveryCallback) *Acceptor {
	return &Acceptor{
		anchor:       anchor,
		callback:     callback,
		checkpoints:  make(map[string]*SignedCheckpoint),
		events:       make(map[string]*EventRecord),
		prekeys:      make(map[string]*PrekeyProof),
		receiptCPs:   make(map[uint64]*ReceiptCheckpoint),
		sparseProofs: make(map[string]*SparseProof),
		receipts:     make(map[string]*Receipt),
	}
}

func checkpointKey(c Checkpoint) string {
	return string(checkpointRef(c))
}

func eventRootKey(eh Hash, root Hash) string {
	return string(eh) + "|" + string(root)
}

func prekeyKey(pp *PrekeyProof) string {
	return string(domainHash("prekey-ref/v1",
		[]byte(pp.Device), u64Bytes(pp.Epoch), u64Bytes(pp.Index), pp.Leaf))
}

func receiptKey(r *Receipt) string {
	return string(receiptHash(*r))
}

// Ingest 导入一条验收记录。重复导入幂等；单条结构或签名不合法时报错，
// 调用方可跳过该记录而不影响其它记录。
func (a *Acceptor) Ingest(r Record) error {
	switch r.Kind {
	case RecCheckpoint:
		if r.Checkpoint == nil {
			return ErrEmptyPayload
		}
		if !verifyCheckpoint(ed25519.PublicKey(a.anchor.DirectoryKey), *r.Checkpoint) {
			return ErrBadSignature
		}
		a.checkpoints[checkpointKey(r.Checkpoint.Checkpoint)] = r.Checkpoint
	case RecEvent:
		if r.Event == nil {
			return ErrEmptyPayload
		}
		if r.Event.Proof.Index >= r.Event.Proof.Size {
			return ErrBadProof
		}
		a.events[eventRootKey(eventHash(r.Event.Event), r.Event.Proof.RootHash)] = r.Event
	case RecPrekeyProof:
		if r.PrekeyProof == nil {
			return ErrEmptyPayload
		}
		if r.PrekeyProof.Index >= r.PrekeyProof.Size {
			return ErrBadProof
		}
		a.prekeys[prekeyKey(r.PrekeyProof)] = r.PrekeyProof
	case RecReceiptCheckpoint:
		if r.ReceiptCheckpoint == nil {
			return ErrEmptyPayload
		}
		if !verifyReceiptCheckpoint(ed25519.PublicKey(a.anchor.WitnessKey), *r.ReceiptCheckpoint) {
			return ErrBadSignature
		}
		a.receiptCPs[r.ReceiptCheckpoint.Sequence] = r.ReceiptCheckpoint
	case RecSparseProof:
		if r.SparseProof == nil {
			return ErrEmptyPayload
		}
		if len(r.SparseProof.Key) != 32 {
			return ErrBadProof
		}
		a.sparseProofs[string(r.SparseProof.Key)] = r.SparseProof
	case RecReceipt:
		if r.Receipt == nil {
			return ErrEmptyPayload
		}
		if !verifyReceipt(*r.Receipt) {
			return ErrBadSignature
		}
		a.receipts[receiptKey(r.Receipt)] = r.Receipt
	default:
		return ErrUnknownRecord
	}
	return nil
}

// IngestBundle 一次性导入一个记录子集。
func (a *Acceptor) IngestBundle(b Bundle) error {
	for _, r := range b.Records {
		if err := a.Ingest(r); err != nil {
			return err
		}
	}
	return nil
}

// viableHeads 返回所有可成立的链末端（分叉头）。每个末端的签名链都可
// 回溯到 size=0 创世点；同一 size 出现多个不同检查点即冲突后缀。
func (a *Acceptor) viableHeads(send SignedCheckpoint) ([]*SignedCheckpoint, bool) {
	n := send.TreeSize
	if have := a.lookupCheckpoint(send.Checkpoint); have == nil || !a.chainViable(have) {
		return nil, false
	}

	// 链末端：没有任何检查点把它当作 Prev。
	referenced := make(map[string]bool)
	for _, cp := range a.checkpoints {
		if len(cp.Prev) > 0 {
			referenced[string(cp.Prev)] = true
		}
	}

	var heads []*SignedCheckpoint
	for key, cp := range a.checkpoints {
		if !referenced[key] && a.chainViable(cp) && cp.TreeSize >= n {
			if a.ancestorAtSize(cp, n) != nil {
				heads = append(heads, cp)
			}
		}
	}
	sort.Slice(heads, func(i, j int) bool {
		return checkpointKey(heads[i].Checkpoint) < checkpointKey(heads[j].Checkpoint)
	})
	if len(heads) == 0 {
		return nil, false
	}
	return heads, true
}

// ancestorAtSize 沿签名链回溯到指定 TreeSize 的祖先检查点。
func (a *Acceptor) ancestorAtSize(tip *SignedCheckpoint, size uint64) *SignedCheckpoint {
	cur := tip
	for cur.TreeSize > size {
		prev, ok := a.checkpoints[string(cur.Prev)]
		if !ok {
			return nil
		}
		cur = prev
	}
	if cur.TreeSize != size {
		return nil
	}
	return cur
}

func (a *Acceptor) lookupCheckpoint(c Checkpoint) *SignedCheckpoint {
	if sc, ok := a.checkpoints[checkpointKey(c)]; ok {
		return sc
	}
	return nil
}

// chainViable 检查签名检查点链：size 从 0 连续递增，每一级 Prev 指向上一级。
func (a *Acceptor) chainViable(head *SignedCheckpoint) bool {
	cur := head
	for cur.TreeSize > 0 {
		if len(cur.Prev) == 0 {
			return false
		}
		prev, ok := a.checkpoints[string(cur.Prev)]
		if !ok {
			return false
		}
		if prev.TreeSize != cur.TreeSize-1 {
			return false
		}
		if !equalHash(checkpointRef(prev.Checkpoint), cur.Prev) {
			return false
		}
		cur = prev
	}
	return len(cur.Prev) == 0
}

// Evaluate 对恢复请求执行完整验收。结论为 accepted 且注册了回调时，
// 恢复密钥材料经回调交付宿主。
func (a *Acceptor) Evaluate(req RecoveryRequest) Decision {
	if len(req.SessionDigest) == 0 || len(req.Commitment) == 0 || req.Account == "" || req.Device == "" {
		return Decision{Status: StatusRejected, Reason: ReasonEmptyRequest, Request: &req}
	}
	send := req.SendCheckpoint
	if !verifyCheckpoint(ed25519.PublicKey(a.anchor.DirectoryKey), send) {
		return Decision{Status: StatusRejected, Reason: ReasonBadSignature, Request: &req}
	}
	heads, ok := a.viableHeads(send)
	if !ok {
		return Decision{Status: StatusPending, Reason: ReasonMissingCheckpoint, Request: &req}
	}

	evals := make([]branchResult, 0, len(heads))
	for _, head := range heads {
		evals = append(evals, a.evaluateBranch(req, head))
	}

	var rejected Reason
	var missing []Reason
	var acceptedIdentity *SessionIdentity
	for _, be := range evals {
		switch {
		case be.rejected != ReasonNone:
			rejected = be.rejected
		case be.missing != ReasonNone:
			missing = append(missing, be.missing)
		default:
			id := be.id
			acceptedIdentity = &id
		}
	}
	if rejected != ReasonNone {
		return Decision{Status: StatusRejected, Reason: rejected, Request: &req, ViableBranches: len(heads)}
	}

	// 所有可成立分支都证明齐了，会话身份必须逐字段相同。
	if len(missing) == 0 {
		first := evals[0].digest
		for _, be := range evals[1:] {
			if !bytes.Equal(first, be.digest) {
				return Decision{
					Status: StatusRejected, Reason: ReasonBranchConflict,
					Request: &req, Identity: acceptedIdentity, ViableBranches: len(heads),
				}
			}
		}
	} else {
		// 仍有分支缺少包含证明：保持 pending，绝不因部分分支可证就放行。
		reason := missing[0]
		return Decision{
			Status: StatusPending, Reason: reason, Request: &req,
			Identity: acceptedIdentity, ViableBranches: len(heads),
		}
	}

	decision := a.evaluateReceipts(req, len(heads), acceptedIdentity)
	if decision.Status == StatusAccepted && a.callback != nil {
		a.callback(decision)
	}
	return decision
}

type branchResult struct {
	id       SessionIdentity
	digest   Hash
	missing  Reason
	rejected Reason
}

func (a *Acceptor) evaluateBranch(req RecoveryRequest, head *SignedCheckpoint) branchResult {
	n := req.SendCheckpoint.TreeSize
	atSend := a.ancestorAtSize(head, n)
	if atSend == nil {
		return branchResult{missing: ReasonMissingCheckpoint}
	}
	head = atSend
	// 1. 按 DeviceTail 重建该设备实例在该分支 size=n 时的完整事件链。
	// DeviceTail 由目录签名背书，无需导入任何无关设备的事件。
	var chain []*EventRecord
	tail, registered := head.DeviceTail[DeviceTailKey(req.Account, req.Device)]
	if !registered {
		return branchResult{rejected: ReasonNotAuthorized}
	}
	located := a.deviceEventsForRoot(req.Account, req.Device, head.RootHash)
	if len(located) == 0 {
		return branchResult{missing: ReasonMissingEventProof}
	}
	if located[len(located)-1].Event.GlobalIndex != tail {
		// 签名尾部指出的最后一条本设备事件尚未导入，链不完整。
		return branchResult{missing: ReasonMissingEventProof}
	}
	for i, er := range located {
		if er.Proof.Size != head.TreeSize {
			return branchResult{missing: ReasonMissingEventProof}
		}
		if err := verifyInclusion(logLeafHash(er.Event), er.Proof, head.RootHash); err != nil {
			return branchResult{rejected: ReasonBadProof}
		}
		if er.Event.Seq != uint64(i) {
			return branchResult{rejected: ReasonBadProof}
		}
		chain = append(chain, er)
	}

	for _, er := range chain {
		if er.Event.Kind == EventRemove {
			return branchResult{rejected: ReasonDeviceRemoved}
		}
	}
	last := chain[len(chain)-1].Event

	// 2. 一次性公钥承诺必须包含在当前生效代的预密钥承诺树中。
	pp, ok := a.findPrekeyForRoot(req.Device, last.PrekeyRoot, req.Commitment)
	if !ok {
		return branchResult{missing: ReasonMissingPrekeyProof}
	}
	leaf := prekeyLeafHash(req.Device, last.Epoch, pp.Index, req.Commitment)
	if !equalHash(pp.Leaf, leaf) {
		return branchResult{rejected: ReasonBadProof}
	}
	if err := verifyPrekeyInclusion(*pp, last.PrekeyRoot); err != nil {
		return branchResult{rejected: ReasonBadProof}
	}

	id := SessionIdentity{
		Account:       req.Account,
		Device:        req.Device,
		IdentityPK:    copyHash(Hash(last.IdentityPK)),
		PrekeyRoot:    copyHash(last.PrekeyRoot),
		Commitment:    copyHash(req.Commitment),
		SessionDigest: copyHash(req.SessionDigest),
	}
	return branchResult{
		id: id,
		digest: domainHash("session-identity/v1",
			[]byte(id.Account), []byte(id.Device), id.IdentityPK, id.PrekeyRoot,
			id.Commitment, id.SessionDigest),
	}
}

// deviceEventsForRoot 返回锚定某分支根的目标设备事件，按全局位置排序。
func (a *Acceptor) deviceEventsForRoot(account AccountID, device DeviceID, root Hash) []*EventRecord {
	var located []*EventRecord
	for _, er := range a.events {
		if er.Event.Account == account && er.Event.Device == device &&
			equalHash(er.Proof.RootHash, root) {
			located = append(located, er)
		}
	}
	sort.Slice(located, func(i, j int) bool {
		return located[i].Event.GlobalIndex < located[j].Event.GlobalIndex
	})
	return located
}

// findPrekeyForRoot 按当前授权的预密钥树根定位承诺证明，避免依赖调用方知道代次。
func (a *Acceptor) findPrekeyForRoot(device DeviceID, root, commitment Hash) (*PrekeyProof, bool) {
	for _, pp := range a.prekeys {
		if pp.Device != device {
			continue
		}
		leaf := prekeyLeafHash(device, pp.Epoch, pp.Index, commitment)
		if !equalHash(pp.Leaf, leaf) {
			continue
		}
		if err := verifyPrekeyInclusion(*pp, root); err == nil {
			return pp, true
		}
	}
	return nil, false
}

func (a *Acceptor) latestReceiptCheckpoint() *ReceiptCheckpoint {
	var best *ReceiptCheckpoint
	for _, cp := range a.receiptCPs {
		if best == nil || cp.Sequence > best.Sequence {
			best = cp
		}
	}
	return best
}

// evaluateReceipts 强制“同一公钥承诺只被这一会话摘要消费一次”。
// 缺少包含/非包含证明（pending）与确有双重消费（rejected）明确分开。
func (a *Acceptor) evaluateReceipts(req RecoveryRequest, branches int, identity *SessionIdentity) Decision {
	latest := a.latestReceiptCheckpoint()
	if latest == nil {
		return a.pending(req, ReasonMissingReceiptProof, branches, identity)
	}
	sp, ok := a.sparseProofs[string(req.SessionDigest)]
	if !ok {
		return a.pending(req, ReasonMissingReceiptProof, branches, identity)
	}
	root, err := verifySparseProof(*sp)
	if err != nil || !equalHash(root, latest.RootHash) {
		return Decision{
			Status: StatusRejected, Reason: ReasonBadProof, Request: &req,
			Identity: identity, ViableBranches: branches,
		}
	}

	var matching []Receipt
	for _, r := range a.receipts {
		if equalHash(r.SessionDigest, req.SessionDigest) {
			matching = append(matching, *r)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		return receiptKey(&matching[i]) < receiptKey(&matching[j])
	})

	if !sp.Exists {
		if len(matching) != 0 {
			return Decision{
				Status: StatusRejected, Reason: ReasonBadProof, Request: &req,
				Identity: identity, ViableBranches: branches,
			}
		}
		// 见证尚未看到该会话的任何回执，目录前缀再全也不能放行。
		return a.pending(req, ReasonNoReceipt, branches, identity)
	}
	if len(matching) == 0 {
		return a.pending(req, ReasonMissingReceipt, branches, identity)
	}

	// 稀疏树叶子值必须与本地持有的整组回执一致，防止隐藏额外消费。
	if !equalHash(sp.Value, receiptSetHash(matching)) {
		return Decision{
			Status: StatusRejected, Reason: ReasonDoubleSpend, Request: &req,
			Identity: identity, ViableBranches: branches,
		}
	}

	for _, r := range matching {
		if !verifyReceipt(r) {
			return Decision{
				Status: StatusRejected, Reason: ReasonReceiptSignature, Request: &req,
				Identity: identity, ViableBranches: branches,
			}
		}
		if r.Device != req.Device || !equalHash(r.Commitment, req.Commitment) {
			return Decision{
				Status: StatusRejected, Reason: ReasonNotAuthorized, Request: &req,
				Identity: identity, ViableBranches: branches,
			}
		}
	}

	if len(matching) > 1 {
		return Decision{
			Status: StatusRejected, Reason: ReasonDoubleSpend, Request: &req,
			Identity: identity, ViableBranches: branches,
		}
	}

	return Decision{
		Status: StatusAccepted, Reason: ReasonNone, Request: &req,
		Identity: identity, ViableBranches: branches,
	}
}

func (a *Acceptor) pending(req RecoveryRequest, reason Reason, branches int, identity *SessionIdentity) Decision {
	return Decision{
		Status: StatusPending, Reason: reason, Request: &req,
		Identity: identity, ViableBranches: branches,
	}
}
