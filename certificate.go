package anchor

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
)

// Certificate 是带时间戳证明的存证证书（审计凭据）：
// 证明某数据在 Anchor.Timestamp 之前已包含于被锚定的默克尔树中。
type Certificate struct {
	Data      []byte      // 被存证的原始数据
	LeafIndex int         // 数据在树中的位置
	Proof     []ProofNode // 默克尔包含证明
	Anchor    Anchor      // 带时间戳与签名的锚定记录
}

// IssueCertificate 为树中第 index 条数据签发存证证书。
func IssueCertificate(tree *MerkleTree, data []byte, index int, anc *Anchor) (*Certificate, error) {
	if anc == nil {
		return nil, errors.New("nil anchor")
	}
	proof, ok := tree.Proof(index)
	if !ok {
		return nil, fmt.Errorf("leaf index %d out of range", index)
	}
	return &Certificate{
		Data:      data,
		LeafIndex: index,
		Proof:     proof,
		Anchor:    *anc,
	}, nil
}

// VerifyCertificate 验证存证证书：
//  1. 由数据重算叶哈希，沿证明路径重算默克尔根，须等于锚定根；
//  2. 锚定签名须有效（锚定根与时间戳不可被事后篡改）；
//  3. 锚定时间戳须不晚于 notLaterThan，即数据在该时刻之前已确定存在。
func VerifyCertificate(pub *ecdsa.PublicKey, cert *Certificate, notLaterThan int64) error {
	if cert == nil {
		return errors.New("nil certificate")
	}
	leaf := HashLeaf(cert.Data)
	if !VerifyProof(leaf, cert.Proof, cert.Anchor.Root) {
		return errors.New("merkle proof does not match anchored root")
	}
	if !VerifySignature(pub, &cert.Anchor) {
		return errors.New("invalid anchor signature")
	}
	if cert.Anchor.Timestamp > notLaterThan {
		return fmt.Errorf("anchor timestamp %d is later than required %d",
			cert.Anchor.Timestamp, notLaterThan)
	}
	return nil
}
