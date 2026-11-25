package recov

// 本文件集中提供记录深拷贝，避免调用方在导入后修改切片而污染导入器状态。

func cloneEvent(e *Event) *Event {
	cp := *e
	cp.IdentityKey = clone(e.IdentityKey)
	cp.OPKRoot = clone(e.OPKRoot)
	return &cp
}

func cloneBlock(b *OPKBlock) *OPKBlock {
	cp := &OPKBlock{
		Account:   b.Account,
		Instance:  b.Instance,
		Root:      clone(b.Root),
		LeafCount: b.LeafCount,
		LeafKeys:  make([][]byte, len(b.LeafKeys)),
	}
	for i, k := range b.LeafKeys {
		cp.LeafKeys[i] = clone(k)
	}
	return cp
}

func cloneReceipt(r *Receipt) *Receipt {
	cp := *r
	cp.Commitment = clone(r.Commitment)
	cp.SessionID = clone(r.SessionID)
	return &cp
}

func cloneEnvelope(e *SessionEnvelope) *SessionEnvelope {
	cp := *e
	cp.OPKCommitment = clone(e.OPKCommitment)
	cp.OPKInclusion = MerkleProof{
		LeafIndex: e.OPKInclusion.LeafIndex,
		TreeSize:  e.OPKInclusion.TreeSize,
		Hashes:    cloneHashList(e.OPKInclusion.Hashes),
	}
	cp.EphemeralPub = clone(e.EphemeralPub)
	cp.HandshakeDigest = clone(e.HandshakeDigest)
	cp.WrappedKey = clone(e.WrappedKey)
	cp.Nonce = clone(e.Nonce)
	cp.SendCheckpoint = cloneSignedCheckpoint(e.SendCheckpoint)
	return &cp
}

func cloneSignedCheckpoint(sc SignedCheckpoint) SignedCheckpoint {
	return SignedCheckpoint{
		Checkpoint: Checkpoint{
			LogID:    sc.Checkpoint.LogID,
			TreeSize: sc.Checkpoint.TreeSize,
			TreeRoot: clone(sc.Checkpoint.TreeRoot),
		},
		SignerKey: clone(sc.SignerKey),
		Signature: clone(sc.Signature),
	}
}

func cloneHashList(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, h := range in {
		out[i] = clone(h)
	}
	return out
}
