package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// OpenSSH 证书类型，见 PROTOCOL.certkeys。
const (
	CertTypeUser uint32 = 1
	CertTypeHost uint32 = 2
)

// 本库实现的证书算法（CA 与主体密钥均为 Ed25519）。
const (
	UserCertAlgorithm = "ssh-ed25519-cert-v01@openssh.com"
	HostCertAlgorithm = "ssh-ed25519-cert-v01@openssh.com"
	keyAlgorithm      = "ssh-ed25519"
	sigAlgorithm      = "ssh-ed25519"
)

// Certificate 是一张 OpenSSH 格式的证书。
type Certificate struct {
	Nonce           []byte
	SubjectKey      ed25519.PublicKey // 证书主体（用户或主机）的公钥
	Serial          uint64
	Type            uint32 // CertTypeUser 或 CertTypeHost
	KeyID           string
	ValidPrincipals []string
	ValidAfter      uint64 // Unix 秒
	ValidBefore     uint64 // Unix 秒
	SignatureKey    []byte // CA 公钥的线格式 blob
	Signature       []byte // 签名 blob（string 算法名 + string 签名）

	signed []byte // 被签名的字节（证书头到 SignatureKey 为止）
}

// GenerateKey 生成一对 Ed25519 密钥，可用作 CA 密钥或主体密钥。
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// marshalPublicKey 返回 Ed25519 公钥的线格式 blob。
func marshalPublicKey(pub ed25519.PublicKey) []byte {
	var w wireWriter
	w.string([]byte(keyAlgorithm))
	w.string(pub)
	return w.buf
}

// Fingerprint 返回公钥的 OpenSSH 风格 SHA256 指纹，用于 CA 登记索引。
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(marshalPublicKey(pub))
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// SignUserCert 用 signer 指定的 CA 私钥签发一张用户证书。
//
// signer 只要求是一把 Ed25519 私钥；本函数不区分它是用户CA还是主机CA
// 的密钥——是否接受该证书由验证方 Authority 的登记信息决定。
func SignUserCert(signer ed25519.PrivateKey, subject ed25519.PublicKey, keyID string, principals []string, serial, validAfter, validBefore uint64) (*Certificate, error) {
	return signCert(signer, subject, keyID, principals, serial, CertTypeUser, validAfter, validBefore)
}

func signCert(signer ed25519.PrivateKey, subject ed25519.PublicKey, keyID string, principals []string, serial uint64, certType uint32, validAfter, validBefore uint64) (*Certificate, error) {
	if l := len(signer); l != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("netsec: invalid CA private key size %d", l)
	}
	if l := len(subject); l != ed25519.PublicKeySize {
		return nil, fmt.Errorf("netsec: invalid subject public key size %d", l)
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	caPub, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("netsec: CA key is not Ed25519")
	}
	sigKey := marshalPublicKey(caPub)

	var w wireWriter
	w.string([]byte(UserCertAlgorithm))
	w.string(nonce)
	w.string(subject)
	w.uint64(serial)
	w.uint32(certType)
	w.string([]byte(keyID))
	w.stringList(principals)
	w.uint64(validAfter)
	w.uint64(validBefore)
	w.stringList(nil) // critical options
	w.stringList(nil) // extensions
	w.string(nil)     // reserved
	w.string(sigKey)

	sig := ed25519.Sign(signer, w.buf)
	var sigBlob wireWriter
	sigBlob.string([]byte(sigAlgorithm))
	sigBlob.string(sig)

	return &Certificate{
		Nonce:           nonce,
		SubjectKey:      subject,
		Serial:          serial,
		Type:            certType,
		KeyID:           keyID,
		ValidPrincipals: principals,
		ValidAfter:      validAfter,
		ValidBefore:     validBefore,
		SignatureKey:    sigKey,
		Signature:       sigBlob.buf,
		signed:          w.buf,
	}, nil
}

// Bytes 返回证书的完整线格式 blob（含签名）。
func (c *Certificate) Bytes() []byte {
	out := make([]byte, 0, len(c.signed)+4+len(c.Signature))
	out = append(out, c.signed...)
	var w wireWriter
	w.string(c.Signature)
	return append(out, w.buf...)
}

// ParseCertificate 解析证书线格式 blob。
func ParseCertificate(blob []byte) (*Certificate, error) {
	r := &wireReader{data: blob}
	algo := r.string()
	if r.err != nil {
		return nil, r.err
	}
	if string(algo) != UserCertAlgorithm {
		return nil, fmt.Errorf("netsec: unsupported certificate algorithm %q", algo)
	}

	c := &Certificate{}
	c.Nonce = r.string()
	subject := r.string()
	c.Serial = r.uint64()
	c.Type = r.uint32()
	keyID := r.string()
	if r.err != nil {
		return nil, r.err
	}
	c.KeyID = string(keyID)

	principals, err := r.stringList()
	if err != nil {
		return nil, err
	}
	c.ValidPrincipals = principals

	c.ValidAfter = r.uint64()
	c.ValidBefore = r.uint64()
	if _, err := r.stringList(); err != nil { // critical options
		return nil, err
	}
	if _, err := r.stringList(); err != nil { // extensions
		return nil, err
	}
	r.string() // reserved
	c.SignatureKey = r.string()
	if r.err != nil {
		return nil, r.err
	}

	// 到 SignatureKey 为止的内容即为被签名的字节。
	c.signed = blob[:len(blob)-len(r.data)]
	c.Signature = r.string()
	if r.err != nil {
		return nil, r.err
	}
	if len(r.data) != 0 {
		return nil, errWireFormat
	}
	if len(subject) != ed25519.PublicKeySize {
		return nil, errors.New("netsec: invalid subject public key in certificate")
	}
	c.SubjectKey = ed25519.PublicKey(subject)
	return c, nil
}
