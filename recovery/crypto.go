package recovery

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// domainPrefix 为不同用途提供域分隔，避免不同结构的哈希相互混淆。
func domainHash(tag string, parts ...[]byte) Hash {
	h := sha256.New()
	label := []byte(tag)
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(label)))
	h.Write(lenBuf[:])
	h.Write(label)
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		h.Write(lenBuf[:])
		h.Write(p)
	}
	return Hash(h.Sum(nil))
}

func u64Bytes(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

// eventHash 计算目录事件的规范化摘要。
func eventHash(e Event) Hash {
	return domainHash("directory-event/v1",
		u64Bytes(e.GlobalIndex),
		u64Bytes(e.Seq),
		[]byte(e.Account),
		[]byte(e.Device),
		[]byte(e.Name),
		[]byte(e.Kind),
		e.IdentityPK,
		e.PrekeyRoot,
		u64Bytes(e.Epoch),
	)
}

// checkpointSigningBytes 生成目录检查点的待签名内容。
func checkpointSigningBytes(c Checkpoint) []byte {
	keys := make([]string, 0, len(c.DeviceTail))
	for k := range c.DeviceTail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := [][]byte{u64Bytes(c.TreeSize), c.RootHash, c.Prev, u64Bytes(uint64(len(keys)))}
	for _, k := range keys {
		parts = append(parts, []byte(k), u64Bytes(c.DeviceTail[k]))
	}
	return domainHash("directory-checkpoint/v1", parts...)
}

// checkpointRef 是检查点在“上一检查点”链中的规范化摘要。
func checkpointRef(c Checkpoint) Hash {
	return domainHash("checkpoint-ref/v1", checkpointSigningBytes(c))
}

// prekeyLeafHash 计算一次性公钥承诺树的叶子摘要。
func prekeyLeafHash(device DeviceID, epoch, index uint64, commitment Hash) Hash {
	return domainHash("prekey-leaf/v1",
		[]byte(device),
		u64Bytes(epoch),
		u64Bytes(index),
		commitment,
	)
}

// receiptSigningBytes 生成回执的待签名内容。
func receiptSigningBytes(r Receipt) []byte {
	return domainHash("consumption-receipt/v1",
		r.SessionDigest,
		[]byte(r.Device),
		r.IdentityPK,
		r.Commitment,
		u64Bytes(r.SendTreeSize),
	)
}

// receiptCheckpointSigningBytes 生成见证检查点的待签名内容。
func receiptCheckpointSigningBytes(sequence uint64, root Hash) []byte {
	return domainHash("receipt-checkpoint/v1", u64Bytes(sequence), root)
}

// signCheckpoint 用目录身份钥对检查点签名。
func signCheckpoint(priv ed25519.PrivateKey, c Checkpoint) SignedCheckpoint {
	sig := ed25519.Sign(priv, checkpointSigningBytes(c))
	return SignedCheckpoint{Checkpoint: c, Signature: append([]byte(nil), sig...)}
}

// verifyCheckpoint 用目录公钥验证检查点签名。
func verifyCheckpoint(pub ed25519.PublicKey, sc SignedCheckpoint) bool {
	if len(pub) != ed25519.PublicKeySize || len(sc.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, checkpointSigningBytes(sc.Checkpoint), sc.Signature)
}

// signReceipt 用设备身份钥对回执签名。
func signReceipt(priv ed25519.PrivateKey, r Receipt) Receipt {
	r.Signature = ed25519.Sign(priv, receiptSigningBytes(r))
	return r
}

// verifyReceipt 用回执内声明的设备身份公钥验证回执签名。
func verifyReceipt(r Receipt) bool {
	if len(r.IdentityPK) != ed25519.PublicKeySize || len(r.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(r.IdentityPK), receiptSigningBytes(r), r.Signature)
}

// signReceiptCheckpoint 用见证钥对回执检查点签名。
func signReceiptCheckpoint(priv ed25519.PrivateKey, sequence uint64, root Hash) ReceiptCheckpoint {
	sig := ed25519.Sign(priv, receiptCheckpointSigningBytes(sequence, root))
	return ReceiptCheckpoint{Sequence: sequence, RootHash: append(Hash(nil), root...), Signature: append([]byte(nil), sig...)}
}

// verifyReceiptCheckpoint 用见证公钥验证回执检查点签名。
func verifyReceiptCheckpoint(pub ed25519.PublicKey, rc ReceiptCheckpoint) bool {
	if len(pub) != ed25519.PublicKeySize || len(rc.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, receiptCheckpointSigningBytes(rc.Sequence, rc.RootHash), rc.Signature)
}

func equalHash(a, b Hash) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func copyHash(h Hash) Hash {
	if h == nil {
		return nil
	}
	out := make(Hash, len(h))
	copy(out, h)
	return out
}
