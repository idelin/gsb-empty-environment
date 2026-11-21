package diffdisclose

import (
	"crypto/ed25519"
	"crypto/rand"
)

// Manifest 是绑定租户、版本、父清单摘要与树根摘要的签名清单。
// Version=0 的创世清单使用全零 Parent。
type Manifest struct {
	Tenant    string
	Version   uint64
	Parent    Digest
	Root      Digest
	PublicKey []byte
	Signature []byte
}

// digest 返回清单的规范摘要，即签名与父子链接的绑定内容。
func (m Manifest) digest() Digest {
	return manifestDigest(m.Tenant, m.Version, m.Parent, m.Root)
}

// ManifestDigest 返回清单的规范摘要，供外部调用方构造父链接或比对版本。
func ManifestDigest(m Manifest) Digest {
	return m.digest()
}

// Signer 抽象清单签名能力，便于集成服务侧 KMS 或测试替身。
type Signer interface {
	Sign(message []byte) ([]byte, error)
	PublicKey() []byte
}

// Verifier 抽象清单验签能力。
type Verifier interface {
	Verify(publicKey, message, signature []byte) bool
}

// Ed25519Signer 是基于 crypto/ed25519 的默认签名器。
type Ed25519Signer struct {
	priv ed25519.PrivateKey
}

// NewEd25519Signer 生成一把临时 Ed25519 签名器（主要用于测试与示例）。
func NewEd25519Signer() (*Ed25519Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Ed25519Signer{priv: priv}, nil
}

// NewEd25519SignerFromKey 包装已有私钥。
func NewEd25519SignerFromKey(priv ed25519.PrivateKey) *Ed25519Signer {
	return &Ed25519Signer{priv: priv}
}

// Sign 对消息签名。
func (s *Ed25519Signer) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, message), nil
}

// PublicKey 返回公钥。
func (s *Ed25519Signer) PublicKey() []byte {
	pub := s.priv.Public().(ed25519.PublicKey)
	return append([]byte(nil), pub...)
}

// Ed25519Verifier 是默认的 Ed25519 验证器。
type Ed25519Verifier struct{}

// Verify 验证签名。
func (Ed25519Verifier) Verify(publicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, signature)
}

// SignManifest 用签名器为树与父子链接生成签名清单。
func SignManifest(signer Signer, tenant string, version uint64, parent Digest, root Digest) (Manifest, error) {
	m := Manifest{
		Tenant:  tenant,
		Version: version,
		Parent:  parent,
		Root:    root,
	}
	digest := m.digest()
	sig, err := signer.Sign(digest[:])
	if err != nil {
		return Manifest{}, err
	}
	m.PublicKey = append([]byte(nil), signer.PublicKey()...)
	m.Signature = append([]byte(nil), sig...)
	return m, nil
}

// VerifyManifest 校验清单签名与基本结构。
func VerifyManifest(m Manifest, verifier Verifier) error {
	if len(m.PublicKey) == 0 || len(m.Signature) == 0 || m.Tenant == "" {
		return errBadManifest
	}
	digest := m.digest()
	if !verifier.Verify(m.PublicKey, digest[:], m.Signature) {
		return errBadSignature
	}
	return nil
}
