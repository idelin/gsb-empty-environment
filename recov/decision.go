package recov

import (
	"errors"
)

// Decision 是对单个会话信封的判定结果。
type Decision struct {
	SessionID []byte
	Accepted  bool
	// BranchCount 是发送时检查点上可成立的目录分支数。
	BranchCount int
	// ReceiptCount 是与该会话绑定的、已纳入验签回执日志的回执数。
	ReceiptCount int
}

// Evaluate 对一个已导入的会话信封给出当前证据下的结论。
//
// 返回 nil 表示验收通过，宿主可继续解封；哨兵错误语义：
//   - ErrPending：证据未到齐（典型：回执先到、目录前缀后到），暂不能下结论；
//   - ErrMissingInclusion：缺少包含证明或日志前缀，无法证明授权/消费；
//   - ErrNotAuthorized：会话发起时实例或 OPK 已不获授权；
//   - ErrForkAmbiguity：冲突后缀下会话身份在各分支不一致；
//   - ErrDoubleConsumption：确有证据证明同一承诺被两个会话摘要消费。
func (im *Importer) Evaluate(env *SessionEnvelope) (*Decision, error) {
	sid := DeriveSessionID(env.HandshakeDigest, env.OPKCommitment, env.Instance, env.Account)

	// 1) 发送时检查点必须由信任的目录钥签名。
	sc := env.SendCheckpoint
	if err := verifySignedCheckpoint(sc, im.dirVerifier); err != nil {
		return nil, err
	}
	if sc.Checkpoint.LogID != DirectoryLogID {
		return nil, wrapErr(ErrMalformed, "send checkpoint is not a directory checkpoint")
	}
	sendSize := sc.Checkpoint.TreeSize
	// 信封自带的发送时检查点在验签通过后本身就是一个可信候选，
	// 即使宿主尚未通过常规渠道导入同规模检查点，也应纳入分支重建。
	if im.dirCheckpoints[sendSize] == nil {
		im.dirCheckpoints[sendSize] = map[string]SignedCheckpoint{}
	}
	im.dirCheckpoints[sendSize][string(sc.Checkpoint.TreeRoot)] = sc

	// 2) 先判断目录前缀是否到齐：未到齐属于“暂不能判定”（Pending），
	//    必须与证据到齐但缺少包含证明的硬性拒绝区分开。
	branches, err := im.dirBranches(sendSize)
	if err != nil {
		return nil, err
	}

	// 3) OPK 承诺必须在该实例授权的 OPK 树内有包含证明。
	block, err := im.findOPKBlock(env)
	if err != nil {
		return nil, err // ErrMissingInclusion：缺少 OPK 树或包含证明不成立
	}

	// 4) 先汇总所有分支的会话身份：冲突后缀下身份不一致 -> 直接判分叉歧义，
	//    优先级高于单分支授权错误。
	var baseline sessionIdentity
	idents := make([]sessionIdentity, 0, len(branches))
	for i, st := range branches {
		ident, err := st.identityFor(env)
		if err != nil {
			return nil, err
		}
		if !equalBytes(ident.opkRoot, block.Root) {
			return nil, wrapErr(ErrNotAuthorized,
				"branch %d: authorized OPK root differs from the block proving the commitment", i)
		}
		idents = append(idents, ident)
		if i == 0 {
			baseline = ident
		}
	}
	for i := 1; i < len(idents); i++ {
		if !baseline.equals(idents[i]) {
			return nil, wrapErr(ErrForkAmbiguity,
				"session identity differs across plausible branches at size %d", sendSize)
		}
	}
	// 身份在所有分支一致后，才检查授权（撤销/注销/换钥）。
	for _, ident := range idents {
		if err := ident.authorized(); err != nil {
			return nil, err
		}
	}

	// 5) 回执日志：该承诺必须被本会话消费，且没有被其它会话消费。
	receiptBranches, err := im.receiptBranches()
	if err != nil {
		return nil, err
	}
	if len(receiptBranches) == 0 {
		// 一个验签回执检查点都还无法重放：可能回执日志尚未送达。
		return nil, wrapErr(ErrPending, "no replayable receipt checkpoint yet")
	}

	selfReceipts, consumers, err := im.analyzeConsumption(receiptBranches, env, sid)
	if err != nil {
		return nil, err
	}
	if selfReceipts == 0 {
		// 有可重放的回执日志，但找不到本会话对该承诺的回执。
		return nil, wrapErr(ErrMissingInclusion,
			"no receipt proving commitment consumption by this session")
	}
	if len(consumers) > 1 {
		other := ""
		for c := range consumers {
			if c != string(sid) {
				other = shortHex(c)
				break
			}
		}
		return nil, wrapErr(ErrDoubleConsumption,
			"commitment also consumed by session %s", other)
	}

	return &Decision{
		SessionID:    sid,
		Accepted:     true,
		BranchCount:  len(branches),
		ReceiptCount: selfReceipts,
	}, nil
}

// findOPKBlock 定位能证明信封承诺的 OPK 树，并验证叶子包含证明。
// 任何失败都归类为“缺少包含证明”，与双花严格区分。
func (im *Importer) findOPKBlock(env *SessionEnvelope) (*OPKBlock, error) {
	if env.OPKInclusion.TreeSize == 0 || env.OPKIndex >= uint32(env.OPKInclusion.TreeSize) {
		return nil, wrapErr(ErrMissingInclusion, "invalid OPK inclusion proof index")
	}
	for _, b := range im.opkBlocks {
		if b.Instance != env.Instance || b.Account != env.Account {
			continue
		}
		if uint64(b.LeafCount) != env.OPKInclusion.TreeSize {
			continue
		}
		// 只接受能与目录状态中该实例授权根对应的承诺树，
		// 避免在历史多棵 OPK 树并存时误用旧树证明新承诺。
		if !im.blockRootAuthorized(env.Instance, b.Root) {
			continue
		}
		pub, err := UnmarshalECDHPublic(b.LeafKeys[env.OPKIndex])
		if err != nil {
			return nil, wrapErr(ErrMissingInclusion, "opk leaf key invalid: %v", err)
		}
		commitment := OPKCommitment(pub, env.OPKIndex, b.Instance, b.Account)
		if !equalBytes(commitment, env.OPKCommitment) {
			continue
		}
		if !VerifyInclusion(env.OPKInclusion, commitment, b.Root) {
			return nil, wrapErr(ErrMissingInclusion,
				"OPK inclusion proof does not verify against the committed tree")
		}
		return b, nil
	}
	return nil, wrapErr(ErrMissingInclusion,
		"no imported OPK block proves commitment for instance %s index %d",
		env.Instance, env.OPKIndex)
}

// blockRootAuthorized 判断给定 OPK 树根是否在某条已观察目录状态中获授权。
func (im *Importer) blockRootAuthorized(inst InstanceID, root []byte) bool {
	for _, byRoot := range im.dirCheckpoints {
		for _, sc := range byRoot {
			size := sc.Checkpoint.TreeSize
			if !im.hasDirPrefix(size) {
				continue
			}
			events, ok := im.matchDirPrefix(size, sc.Checkpoint.TreeRoot)
			if !ok {
				continue
			}
			st, err := foldEvents(events)
			if err != nil {
				continue
			}
			if cur, ok := st.instances[inst]; ok && equalBytes(cur.opkRoot, root) {
				return true
			}
		}
	}
	return false
}

// analyzeConsumption 在所有可成立的回执分支上汇总某承诺的消费情况。
// 返回本会话回执数，以及消费该承诺的全部不同会话标识集合。
func (im *Importer) analyzeConsumption(branches [][]*Receipt, env *SessionEnvelope, sid []byte) (int, map[string]int, error) {
	consumers := map[string]int{}
	seenReceipt := map[string]bool{}
	selfCount := 0
	for _, leaves := range branches {
		for _, r := range leaves {
			if r.Instance != env.Instance || r.Account != env.Account ||
				r.OPKIndex != env.OPKIndex || !equalBytes(r.Commitment, env.OPKCommitment) {
				continue
			}
			rh := string(ReceiptHash(r))
			if seenReceipt[rh] {
				continue // 同一条回执在多个分支出现只计一次
			}
			seenReceipt[rh] = true
			consumers[string(r.SessionID)]++
			if equalBytes(r.SessionID, sid) {
				selfCount++
			}
		}
	}
	return selfCount, consumers, nil
}

// EvaluateAll 对所有已导入信封进行判定，返回会话标识到结果的映射。
func (im *Importer) EvaluateAll() map[string]*SessionResult {
	out := map[string]*SessionResult{}
	for sid, env := range im.envelopes {
		dec, err := im.Evaluate(env)
		out[sid] = &SessionResult{Envelope: env, Decision: dec, Err: err}
	}
	return out
}

// SessionResult 聚合单会话的信封、判定与错误。
type SessionResult struct {
	Envelope *SessionEnvelope
	Decision *Decision
	Err      error
}

// IsPending 便于宿主区分“暂不能判定”与“硬性拒绝”。
func IsPending(err error) bool { return errors.Is(err, ErrPending) }

func shortHex(s string) string {
	if len(s) > 8 {
		return s[:8] + "…"
	}
	return s
}
