package recov

import (
	"crypto/ed25519"
	"encoding/json"
	"sort"
)

// 验收包内每条记录都有独立的规范哈希，清单记录这些哈希的排序集合。
// 最小子集的含义：去掉任意一条记录，独立重放都会缺失至少一项证明。

func signedCheckpointRecordHash(sc *SignedCheckpoint) []byte {
	body := enc(
		putString(sc.Checkpoint.LogID), putU64(sc.Checkpoint.TreeSize),
		putBytes(sc.Checkpoint.TreeRoot), putBytes(sc.SignerKey), putBytes(sc.Signature),
	)
	return hashDomain("recov/record/checkpoint/v1:", body)
}

func envelopeRecordHash(env *SessionEnvelope) []byte {
	proof := enc(putU64(env.OPKInclusion.LeafIndex), putU64(env.OPKInclusion.TreeSize))
	for _, h := range env.OPKInclusion.Hashes {
		proof = append(proof, putBytes(h)...)
	}
	body := enc(
		putString(string(env.Account)), putString(string(env.Instance)), putU32(env.OPKIndex),
		putBytes(env.OPKCommitment), proof,
		signedCheckpointRecordHash(&env.SendCheckpoint),
		putBytes(env.EphemeralPub), putBytes(env.HandshakeDigest),
		putBytes(env.WrappedKey), putBytes(env.Nonce),
	)
	return hashDomain("recov/record/envelope/v1:", body)
}

// BuildAcceptancePackage 在导入器已对某会话验收通过后，导出最小必要记录子集。
//
// 记录构成（每条都承担一项不可替代的证明）：
//   - 会话信封：OPK 包含证明、握手摘要、封装密钥、发送时检查点；
//   - OPK 承诺树：把承诺叶子重算到目录事件所承诺的树根；
//   - 发送时目录检查点 + 完整事件前缀：证明会话发起时的授权状态；
//   - 一个可重放的回执检查点 + 完整回执前缀：证明唯一消费、无双花。
func (im *Importer) BuildAcceptancePackage(env *SessionEnvelope, witness *WitnessKey) (*AcceptancePackage, error) {
	sid := DeriveSessionID(env.HandshakeDigest, env.OPKCommitment, env.Instance, env.Account)
	if _, err := im.Evaluate(env); err != nil {
		return nil, wrapErr(err, "cannot package a session that does not verify")
	}

	block, err := im.findOPKBlock(env)
	if err != nil {
		return nil, err
	}

	sendSize := env.SendCheckpoint.Checkpoint.TreeSize
	dirEvents, ok := im.matchDirPrefix(sendSize, env.SendCheckpoint.Checkpoint.TreeRoot)
	if !ok {
		return nil, wrapErr(ErrMissingInclusion, "cannot reconstruct send checkpoint directory prefix")
	}

	receiptLeaves, receiptCP, err := im.smallestReplayableReceiptBranch(env, sid)
	if err != nil {
		return nil, err
	}

	type entry struct {
		rec  JSONRecord
		hash []byte
	}
	entries := []entry{}
	add := func(kind string, v any, h []byte) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rec: JSONRecord{Kind: kind, Raw: raw}, hash: h})
		return nil
	}

	if err := add(KindSessionEnvelope, env, envelopeRecordHash(env)); err != nil {
		return nil, err
	}
	if err := add(KindOPKBlock, block, OPKBlockHash(block)); err != nil {
		return nil, err
	}
	if err := add(KindSignedCheckpoint, env.SendCheckpoint,
		signedCheckpointRecordHash(&env.SendCheckpoint)); err != nil {
		return nil, err
	}
	for _, ev := range dirEvents {
		if err := add(KindEvent, ev, EventHash(ev)); err != nil {
			return nil, err
		}
	}
	for _, r := range receiptLeaves {
		if err := add(KindReceipt, r, ReceiptHash(r)); err != nil {
			return nil, err
		}
	}
	if err := add(KindSignedCheckpoint, receiptCP, signedCheckpointRecordHash(&receiptCP)); err != nil {
		return nil, err
	}

	hashes := make([][]byte, len(entries))
	records := make([]JSONRecord, len(entries))
	for i, e := range entries {
		hashes[i] = e.hash
		records[i] = e.rec
	}
	sort.Slice(hashes, func(i, j int) bool { return string(hashes[i]) < string(hashes[j]) })

	manifest := Manifest{
		SessionID:         sid,
		DirectoryVerifier: clone(im.dirVerifier),
		WitnessVerifier:   clone(im.witVerifier),
		RecordHashes:      hashes,
	}
	pkg := &AcceptancePackage{
		Manifest:   manifest,
		WitnessSig: witness.sign(ManifestHash(&manifest)),
		Records:    records,
	}
	if err := VerifyAcceptancePackage(pkg); err != nil {
		return nil, err
	}
	if err := assertMinimal(pkg); err != nil {
		return nil, err
	}
	return pkg, nil
}

// smallestReplayableReceiptBranch 选出覆盖本会话回执的最小可重放回执前缀及其检查点。
func (im *Importer) smallestReplayableReceiptBranch(env *SessionEnvelope, sid []byte) ([]*Receipt, SignedCheckpoint, error) {
	sizes := sortedUint64Keys(im.witCheckpoints)
	for _, size := range sizes {
		for _, sc := range im.witCheckpoints[size] {
			leaves, ok := im.matchReceiptPrefix(size, sc.Checkpoint.TreeRoot)
			if !ok {
				continue
			}
			hasSelf := false
			for _, r := range leaves {
				if r.Instance == env.Instance && r.Account == env.Account &&
					r.OPKIndex == env.OPKIndex &&
					equalBytes(r.Commitment, env.OPKCommitment) &&
					equalBytes(r.SessionID, sid) {
					hasSelf = true
				}
			}
			if hasSelf {
				return leaves, sc, nil
			}
		}
	}
	return nil, SignedCheckpoint{}, wrapErr(ErrMissingInclusion,
		"no replayable receipt branch contains this session's receipt")
}

// VerifyAcceptancePackage 不依赖导入器的历史状态，独立重放整包并做完整验收。
func VerifyAcceptancePackage(pkg *AcceptancePackage) error {
	if pkg == nil {
		return wrapErr(ErrMalformed, "nil package")
	}
	if !ed25519.Verify(pkg.Manifest.WitnessVerifier, ManifestHash(&pkg.Manifest), pkg.WitnessSig) {
		return wrapErr(ErrBadSignature, "manifest witness signature invalid")
	}

	got := map[string]bool{}
	decoded := make([]any, 0, len(pkg.Records))
	for _, rec := range pkg.Records {
		v, err := decodeRecord(rec)
		if err != nil {
			return err
		}
		h := recordHashOf(rec)
		if h == nil {
			return wrapErr(ErrMalformed, "unknown record kind %q", rec.Kind)
		}
		if got[string(h)] {
			return wrapErr(ErrMalformed, "duplicate record in package")
		}
		got[string(h)] = true
		decoded = append(decoded, v)
	}
	for _, h := range pkg.Manifest.RecordHashes {
		if !got[string(h)] {
			return wrapErr(ErrMissingInclusion, "record declared by manifest is absent")
		}
	}
	if len(got) != len(pkg.Manifest.RecordHashes) {
		return wrapErr(ErrMissingInclusion, "package contains records not declared by manifest")
	}

	im, envelope, err := packageImporter(pkg, decoded)
	if err != nil {
		return err
	}
	decision, err := im.Evaluate(envelope)
	if err != nil {
		return err
	}
	if !equalBytes(decision.SessionID, pkg.Manifest.SessionID) {
		return wrapErr(ErrMalformed, "manifest session id does not match envelope")
	}
	return nil
}

// packageImporter 用包内记录构造一个已加载全部证据的导入器。
func packageImporter(pkg *AcceptancePackage, decoded []any) (*Importer, *SessionEnvelope, error) {
	im := NewImporter(pkg.Manifest.DirectoryVerifier, pkg.Manifest.WitnessVerifier)
	var envelope *SessionEnvelope
	for _, v := range decoded {
		switch t := v.(type) {
		case *SessionEnvelope:
			envelope = t
		case *OPKBlock:
			if err := im.ImportOPKBlock(t); err != nil {
				return nil, nil, err
			}
		case *SignedCheckpoint:
			switch t.Checkpoint.LogID {
			case DirectoryLogID:
				if err := im.ImportDirectoryCheckpoint(*t); err != nil {
					return nil, nil, err
				}
			case ReceiptLogID:
				if err := im.ImportWitnessCheckpoint(*t); err != nil {
					return nil, nil, err
				}
			}
		case *Receipt:
			if err := im.ImportReceipt(t); err != nil {
				return nil, nil, err
			}
		case *Event:
			if err := im.ImportEvent(t); err != nil {
				return nil, nil, err
			}
		}
	}
	if envelope == nil {
		return nil, nil, wrapErr(ErrMissingInclusion, "package contains no session envelope")
	}
	return im, envelope, nil
}

func decodeRecord(rec JSONRecord) (any, error) {
	var v any
	switch rec.Kind {
	case KindSessionEnvelope:
		v = &SessionEnvelope{}
	case KindOPKBlock:
		v = &OPKBlock{}
	case KindSignedCheckpoint:
		v = &SignedCheckpoint{}
	case KindReceipt:
		v = &Receipt{}
	case KindEvent:
		v = &Event{}
	default:
		return nil, wrapErr(ErrMalformed, "unknown record kind %q", rec.Kind)
	}
	if err := json.Unmarshal(rec.Raw, v); err != nil {
		return nil, wrapErr(ErrMalformed, "decode %s: %v", rec.Kind, err)
	}
	return v, nil
}

func recordHashOf(rec JSONRecord) []byte {
	v, err := decodeRecord(rec)
	if err != nil {
		return nil
	}
	switch t := v.(type) {
	case *SessionEnvelope:
		return envelopeRecordHash(t)
	case *OPKBlock:
		return OPKBlockHash(t)
	case *SignedCheckpoint:
		return signedCheckpointRecordHash(t)
	case *Receipt:
		return ReceiptHash(t)
	case *Event:
		return EventHash(t)
	}
	return nil
}

// assertMinimal 验证去掉任意一条记录后整包都无法再通过独立验收。
func assertMinimal(pkg *AcceptancePackage) error {
	for i := range pkg.Records {
		reduced := clonePackage(pkg)
		removedHash := recordHashOf(pkg.Records[i])
		reduced.Records = append(reduced.Records[:i:i], reduced.Records[i+1:]...)
		reduced.Manifest.RecordHashes = dropHash(reduced.Manifest.RecordHashes, removedHash)
		if err := VerifyAcceptancePackage(reduced); err == nil {
			return wrapErr(ErrMalformed, "package not minimal: a %s record is unnecessary",
				pkg.Records[i].Kind)
		}
	}
	return nil
}

func dropHash(hashes [][]byte, target []byte) [][]byte {
	out := make([][]byte, 0, len(hashes))
	for _, h := range hashes {
		if equalBytes(h, target) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func clonePackage(pkg *AcceptancePackage) *AcceptancePackage {
	raw, _ := json.Marshal(pkg)
	var cp AcceptancePackage
	_ = json.Unmarshal(raw, &cp)
	return &cp
}
