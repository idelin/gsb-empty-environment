package recovery

import "sort"

// ExportBundle 从验收器已导入的记录中挑出支持某 accepted 决策所需的
// 最小必要记录子集：每一条都对应一项不可省略的证明，去掉任何一条都会
// 让 VerifyBundle 失去该项证明而失败。
func (a *Acceptor) ExportBundle(req RecoveryRequest, decision Decision) (Bundle, error) {
	if decision.Status != StatusAccepted || decision.Identity == nil {
		return Bundle{}, ErrEmptyPayload
	}
	send := req.SendCheckpoint
	heads, ok := a.viableHeads(send)
	if !ok {
		return Bundle{}, ErrBadProof
	}

	seen := make(map[string]bool)
	var records []Record
	add := func(key string, r Record) {
		if seen[key] {
			return
		}
		seen[key] = true
		records = append(records, r)
	}

	// 1. 每个可成立分支回溯到发送检查点的签名链，以及 size=n 上的冲突兄弟。
	// size>n 的分叉后缀不影响发送时身份，按最小性原则不导出。
	for _, head := range heads {
		cur := a.ancestorAtSize(head, send.TreeSize)
		if cur == nil {
			return Bundle{}, ErrBadProof
		}
		for {
			add("cp:"+checkpointKey(cur.Checkpoint), Record{Kind: RecCheckpoint, Checkpoint: cur})
			if cur.TreeSize == 0 {
				break
			}
			prev := a.checkpoints[string(cur.Prev)]
			if prev == nil {
				return Bundle{}, ErrBadProof
			}
			cur = prev
		}
	}

	// 2. 目标设备实例在每个分支上的连续事件链及包含证明。
	for _, head := range heads {
		atSend := a.ancestorAtSize(head, req.SendCheckpoint.TreeSize)
		if atSend == nil {
			return Bundle{}, ErrBadProof
		}
		for _, er := range a.deviceEventsForRoot(req.Account, req.Device, atSend.RootHash) {
			key := "ev:" + eventRootKey(eventHash(er.Event), atSend.RootHash)
			add(key, Record{Kind: RecEvent, Event: er})
		}
	}

	// 3. 当前生效代预密钥树中，目标承诺的包含证明。
	identity := decision.Identity
	if pp, ok := a.findPrekeyForRoot(req.Device, identity.PrekeyRoot, req.Commitment); ok {
		add("pk:"+prekeyKey(pp), Record{Kind: RecPrekeyProof, PrekeyProof: pp})
	} else {
		return Bundle{}, ErrBadProof
	}

	// 4. 见证侧：最新签名检查点、会话摘要的稀疏证明、唯一回执本身。
	latest := a.latestReceiptCheckpoint()
	if latest == nil {
		return Bundle{}, ErrBadProof
	}
	add("rcp:"+u64ToString(latest.Sequence), Record{Kind: RecReceiptCheckpoint, ReceiptCheckpoint: latest})

	sp, ok := a.sparseProofs[string(req.SessionDigest)]
	if !ok {
		return Bundle{}, ErrBadProof
	}
	add("sp:"+string(req.SessionDigest), Record{Kind: RecSparseProof, SparseProof: sp})

	var chosen *Receipt
	for _, r := range a.receipts {
		if equalHash(r.SessionDigest, req.SessionDigest) {
			chosen = r
			break
		}
	}
	if chosen == nil {
		return Bundle{}, ErrBadProof
	}
	add("rc:"+receiptKey(chosen), Record{Kind: RecReceipt, Receipt: chosen})

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return recordOrderingKey(records[i]) < recordOrderingKey(records[j])
	})

	return Bundle{
		Trust:   a.anchor,
		Request: req,
		Records: records,
	}, nil
}

func u64ToString(v uint64) string {
	return string(u64Bytes(v))
}

func recordOrderingKey(r Record) string {
	switch r.Kind {
	case RecCheckpoint:
		return checkpointKey(r.Checkpoint.Checkpoint)
	case RecEvent:
		return eventRootKey(eventHash(r.Event.Event), r.Event.Proof.RootHash)
	case RecPrekeyProof:
		return prekeyKey(r.PrekeyProof)
	case RecReceiptCheckpoint:
		return u64ToString(r.ReceiptCheckpoint.Sequence)
	case RecSparseProof:
		return string(r.SparseProof.Key)
	case RecReceipt:
		return receiptKey(r.Receipt)
	default:
		return ""
	}
}

// VerifyBundle 不依赖任何额外上下文，仅凭 Bundle 内的信任锚与记录独立验证。
// 验收通过时返回的 Decision 可直接用于宿主回调。
func VerifyBundle(b Bundle) (Decision, error) {
	acceptor := NewAcceptor(b.Trust, nil)
	for _, r := range b.Records {
		if err := acceptor.Ingest(r); err != nil {
			return Decision{}, err
		}
	}
	decision := acceptor.Evaluate(b.Request)
	if decision.Status != StatusAccepted {
		return decision, nil
	}

	// 最小性自检：去掉任何一条记录都必须失去至少一项证明。
	for i := range b.Records {
		stripped := NewAcceptor(b.Trust, nil)
		for j, r := range b.Records {
			if i == j {
				continue
			}
			if err := stripped.Ingest(r); err != nil {
				return Decision{}, err
			}
		}
		again := stripped.Evaluate(b.Request)
		if again.Status == StatusAccepted {
			return Decision{}, ErrNonMinimal
		}
	}
	return decision, nil
}
