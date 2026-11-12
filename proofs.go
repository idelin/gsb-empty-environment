package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// ProofKind 区分删除证明与不可解密证明。
type ProofKind uint8

const (
	// KindDeletion 证明目标块已从备份介质删除（或重写为不含该数据）。
	KindDeletion ProofKind = 1
	// KindUndecryptable 证明目标块的密钥材料已销毁，残余密文不可解密，
	// 在隐私语义上等价于数据不可恢复。
	KindUndecryptable ProofKind = 2
)

// String 返回证明类型的稳定名称，用于签名与报告。
func (k ProofKind) String() string {
	switch k {
	case KindDeletion:
		return "deletion"
	case KindUndecryptable:
		return "undecryptable"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// GlobalScope 标识全局作用域证明：对该块在所有分支上的可达引用都生效。
// 只有宿主确认该块（及其全部共享副本）整体删除/销毁时才可出具。
const GlobalScope = ""

// Proof 是备份宿主提交的单条删除或不可解密证据。
//
// Scope 为空（GlobalScope）时为全局证明，覆盖目标块在每条分支上的引用；
// Scope 为具体分支名时为分支作用域证明，只覆盖该分支上的引用。
// 共享块被多个分支引用时，单个分支作用域证明不能充当全局删除。
type Proof struct {
	BlockID   BlockID
	Kind      ProofKind
	Scope     string
	SignerID  string
	Signature []byte
	// Reference 可选，指向底层工单/审计流水，仅用于报告可追溯性。
	Reference string
}

// signingDomain 对本包签名消息做域分离，避免与其他协议消息混用。
const signingDomain = "netsec/q0455 backup-deletion-proof v1"

// SigningInput 返回参与签名的规范字节。认证器实现与宿主签名工具必须
// 使用完全一致的编码，Reference 不纳入签名（属外部追溯元数据）。
func (p Proof) SigningInput() []byte {
	domainHash := sha256.Sum256([]byte(signingDomain))
	blockHash := sha256.Sum256([]byte(p.BlockID))
	scopeHash := sha256.Sum256([]byte(p.Scope))
	signerHash := sha256.Sum256([]byte(p.SignerID))

	buf := make([]byte, 0, sha256.Size*4+16)
	buf = append(buf, domainHash[:]...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(p.Kind))
	buf = append(buf, blockHash[:]...)
	buf = append(buf, scopeHash[:]...)
	buf = append(buf, signerHash[:]...)
	return buf
}

// Authenticator 抽象备份宿主的证明认证策略，便于注入测试或 HSM/KMS 实现。
type Authenticator interface {
	// Verify 仅在签名确实由 proof.SignerID 对应密钥签发时返回 nil；
	// 签名无效返回 ErrProofRejected，认证器自身故障返回 ErrAuthenticator。
	Verify(proof Proof) error
}

// Ed25519Authenticator 基于登记的 Ed25519 公钥核验证明签名。
// 未登记的 SignerID 一律拒证；该类型可并发用于多 goroutine 只读核验。
type Ed25519Authenticator struct {
	keys map[string]ed25519.PublicKey
}

// NewEd25519Authenticator 用 signerID -> 公钥映射构造认证器。
func NewEd25519Authenticator(keys map[string]ed25519.PublicKey) *Ed25519Authenticator {
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		dup := make(ed25519.PublicKey, len(key))
		copy(dup, key)
		copied[id] = dup
	}
	return &Ed25519Authenticator{keys: copied}
}

// Verify 实现 Authenticator。
func (a *Ed25519Authenticator) Verify(proof Proof) error {
	key, ok := a.keys[proof.SignerID]
	if !ok {
		return fmt.Errorf("%w: unknown signer %q", ErrProofRejected, proof.SignerID)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: signer %q has malformed public key", ErrAuthenticator, proof.SignerID)
	}
	if !ed25519.Verify(key, proof.SigningInput(), proof.Signature) {
		return fmt.Errorf("%w: invalid signature from %q for block %q", ErrProofRejected, proof.SignerID, proof.BlockID)
	}
	return nil
}

// SignProof 用 Ed25519 私钥对证明做规范签名，写回 Signature 字段并返回。
// 供备份宿主的测试或离线签名工具使用；生产签名可在 HSM/KMS 内按
// Proof.SigningInput 的同一规范编码完成。
func SignProof(proof Proof, signerID string, key ed25519.PrivateKey) Proof {
	proof.SignerID = signerID
	proof.Signature = ed25519.Sign(key, proof.SigningInput())
	return proof
}

// EncodeSignature 以 base64 标准编码导出签名，方便宿主落盘或传输。
func EncodeSignature(sig []byte) string {
	return base64.StdEncoding.EncodeToString(sig)
}
