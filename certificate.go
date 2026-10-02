package merkleanchor

import "errors"

// Certificate 是存证证书（审计凭据）：
// 由默克尔包含证明 + 带时间戳签名的锚定记录组成。
// 它证明：某条数据在 Anchor.Timestamp 之前已入库固定，
// 因为该数据的哈希参与了被签名锚定的默克尔根的计算。
type Certificate struct {
	LeafHash [32]byte    // 数据叶子哈希
	Proof    []ProofStep // 从叶子到根的默克尔路径
	Anchor   Anchor      // 带时间戳与防重放签名的锚定记录
}

// 证书签发/校验错误。
var (
	ErrDataNotAnchored = errors.New("certificate: data not covered by this anchor")
	ErrBadProof        = errors.New("certificate: merkle inclusion proof invalid")
)

// IssueCertificate 为 data 签发存证证书：
// 在 leaves（锚定时刻的完整叶子哈希集）中定位 data，
// 生成包含证明并绑定到已签发的 anchor 上。
func IssueCertificate(data []byte, leaves [][32]byte, anchor *Anchor) (*Certificate, error) {
	leaf := HashLeaf(data)
	index := -1
	for i, l := range leaves {
		if l == leaf {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, ErrDataNotAnchored
	}
	if MerkleRoot(leaves) != anchor.Root {
		return nil, ErrDataNotAnchored
	}
	return &Certificate{
		LeafHash: leaf,
		Proof:    InclusionProof(leaves, index),
		Anchor:   *anchor,
	}, nil
}

// VerifyCertificate 校验存证证书：
// 1) 数据哈希确为证书中的叶子；
// 2) 叶子经默克尔路径归约到被锚定的根；
// 3) 锚定记录签名有效、时间戳合法、非重放。
// 返回锚定时间戳，即“数据必然已存在”的时间上界。
func VerifyCertificate(cert *Certificate, data []byte, v *Verifier, now int64) (int64, error) {
	if HashLeaf(data) != cert.LeafHash {
		return 0, ErrBadProof
	}
	if !VerifyInclusion(cert.LeafHash, cert.Proof, cert.Anchor.Root) {
		return 0, ErrBadProof
	}
	if err := v.VerifyAnchor(&cert.Anchor, now); err != nil {
		return 0, err
	}
	return cert.Anchor.Timestamp, nil
}
