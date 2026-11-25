package recov

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"hash"
	"io"
	"math/big"
)

// DirectoryKey 是目录服务方对检查点签名的 ed25519 密钥。
type DirectoryKey struct {
	Public ed25519.PublicKey
	priv   ed25519.PrivateKey
}

// WitnessKey 是离线见证对回执检查点与验收包清单签名的 ed25519 密钥。
type WitnessKey struct {
	Public ed25519.PublicKey
	priv   ed25519.PrivateKey
}

// GenerateDirectoryKey 生成目录签名钥。
func GenerateDirectoryKey() (*DirectoryKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &DirectoryKey{Public: pub, priv: priv}, nil
}

// GenerateWitnessKey 生成见证签名钥。
func GenerateWitnessKey() (*WitnessKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &WitnessKey{Public: pub, priv: priv}, nil
}

func (k *DirectoryKey) sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

func (k *WitnessKey) sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

// SignCheckpoint 由目录或见证对一条检查点签名。
func (k *DirectoryKey) SignCheckpoint(c *Checkpoint) (SignedCheckpoint, error) {
	sig := ed25519.Sign(k.priv, CheckpointSigningBytes(c))
	return SignedCheckpoint{Checkpoint: *c, SignerKey: clone(k.Public), Signature: sig}, nil
}

// SignReceiptCheckpoint 由见证对回执日志检查点签名。
func (k *WitnessKey) SignReceiptCheckpoint(c *Checkpoint) (SignedCheckpoint, error) {
	sig := ed25519.Sign(k.priv, CheckpointSigningBytes(c))
	return SignedCheckpoint{Checkpoint: *c, SignerKey: clone(k.Public), Signature: sig}, nil
}

// verifySignedCheckpoint 用期望的签名公钥校验检查点签名。
func verifySignedCheckpoint(sc SignedCheckpoint, expected ed25519.PublicKey) error {
	if !equalBytes(sc.SignerKey, expected) {
		return wrapErr(ErrBadSignature, "checkpoint signer key does not match trusted verifier")
	}
	if !ed25519.Verify(expected, CheckpointSigningBytes(&sc.Checkpoint), sc.Signature) {
		return wrapErr(ErrBadSignature, "checkpoint signature invalid (log %s size %d)",
			sc.Checkpoint.LogID, sc.Checkpoint.TreeSize)
	}
	return nil
}

// ---- P-256 ECDH + HKDF + AES-GCM：恢复密钥的封装与解封 ----

const wrapInfo = "recov/recovery-key-wrap/v1"

// GenerateECDHKey 生成一把 P-256 ECDH 密钥（用于身份钥或 OPK 叶子）。
func GenerateECDHKey() (*ecdh.PrivateKey, error) {
	return ecdh.P256().GenerateKey(rand.Reader)
}

// MarshalECDHPublic 以 SEC1 未压缩点导出公钥。
func MarshalECDHPublic(pub *ecdh.PublicKey) []byte {
	return pub.Bytes()
}

// UnmarshalECDHPublic 把 SEC1 点（压缩或未压缩）还原为 ECDH 公钥。
func UnmarshalECDHPublic(pt []byte) (*ecdh.PublicKey, error) {
	var x, y *big.Int
	switch len(pt) {
	case 65:
		x, y = elliptic.Unmarshal(elliptic.P256(), pt)
	case 33:
		x, y = elliptic.UnmarshalCompressed(elliptic.P256(), pt)
	default:
		return nil, wrapErr(ErrMalformed, "unsupported P-256 point length %d", len(pt))
	}
	if x == nil {
		return nil, wrapErr(ErrMalformed, "invalid compressed P-256 point")
	}
	uncompressed := elliptic.Marshal(elliptic.P256(), x, y)
	return ecdh.P256().NewPublicKey(uncompressed)
}

func deriveWrapKey(shared, sessionID []byte) ([]byte, error) {
	salt := sha256.Sum256(sessionID)
	return hkdfExtractExpand(sha256.New, salt[:], shared, []byte(wrapInfo), 32), nil
}

// hkdfExtractExpand 用标准库 hmac 实现 RFC 5869 HKDF（提取-扩展）。
func hkdfExtractExpand(h func() hash.Hash, salt, ikm, info []byte, length int) []byte {
	if len(salt) == 0 {
		salt = make([]byte, h().Size())
	}
	ext := hmac.New(h, salt)
	ext.Write(ikm)
	prk := ext.Sum(nil)

	var okm, previous []byte
	for counter := byte(1); len(okm) < length; counter++ {
		exp := hmac.New(h, prk)
		exp.Write(previous)
		exp.Write(info)
		exp.Write([]byte{counter})
		previous = exp.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:length]
}

// WrapRecoveryKey 用接收方 OPK 私钥与发送方临时密钥的 ECDH 派生密钥封装恢复密钥。
func WrapRecoveryKey(receiverPub *ecdh.PublicKey, ephemeralPriv *ecdh.PrivateKey,
	sessionID, recoveryKey []byte) (wrapped, nonce []byte, err error) {
	shared, err := ephemeralPriv.ECDH(receiverPub)
	if err != nil {
		return nil, nil, wrapErr(ErrUnwrap, "ecdh failed: %v", err)
	}
	key, err := deriveWrapKey(shared, sessionID)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	wrapped = gcm.Seal(nil, nonce, recoveryKey, sessionID)
	return wrapped, nonce, nil
}

// UnwrapRecoveryKey 在验收通过后由宿主调用，解封恢复密钥。
func UnwrapRecoveryKey(receiverPriv *ecdh.PrivateKey, ephemeralPubPt, sessionID, nonce, wrapped []byte) ([]byte, error) {
	pub, err := UnmarshalECDHPublic(ephemeralPubPt)
	if err != nil {
		return nil, err
	}
	shared, err := receiverPriv.ECDH(pub)
	if err != nil {
		return nil, wrapErr(ErrUnwrap, "ecdh failed: %v", err)
	}
	key, err := deriveWrapKey(shared, sessionID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, wrapped, sessionID)
	if err != nil {
		return nil, wrapErr(ErrUnwrap, "aead open failed")
	}
	return plain, nil
}

// OPKCommitment 计算 OPK 叶子的公钥承诺。承诺树叶子数据即此承诺。
func OPKCommitment(pub *ecdh.PublicKey, index uint32, inst InstanceID, acct AccountID) []byte {
	body := enc(
		putString(string(acct)), putString(string(inst)), putU32(index),
		putBytes(pub.Bytes()),
	)
	return hashDomain("recov/opk-leaf/v1:", body)
}
