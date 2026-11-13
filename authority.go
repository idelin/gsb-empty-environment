package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrUnknownCA 表示证书由未登记的 CA 密钥签发。
	ErrUnknownCA = errors.New("netsec: certificate signed by an unregistered CA key")
	// ErrHostCAKeyNotUserCA 表示证书由主机CA密钥签发，而该密钥未登记为
	// 用户CA。主机CA密钥与用户CA密钥不能互换使用。
	ErrHostCAKeyNotUserCA = errors.New("netsec: host CA key cannot verify user certificates")
	// ErrBadSignature 表示证书签名验证失败。
	ErrBadSignature = errors.New("netsec: invalid certificate signature")
	// ErrNotUserCert 表示证书类型不是用户证书。
	ErrNotUserCert = errors.New("netsec: certificate is not a user certificate")
	// ErrExpired 表示证书不在有效期内。
	ErrExpired = errors.New("netsec: certificate is not valid at the current time")
	// ErrPrincipal 表示请求的主体不在证书允许列表中。
	ErrPrincipal = errors.New("netsec: principal not listed in certificate")
)

// Authority 是 SSH 服务侧的 CA 登记处。主机CA密钥与用户CA密钥分开登记，
// 验证用户证书时只查用户CA登记表，两者不能互换。
type Authority struct {
	userCAs map[string]ed25519.PublicKey
	hostCAs map[string]ed25519.PublicKey
	now     func() time.Time
}

// NewAuthority 返回一个空的 CA 登记处。
func NewAuthority() *Authority {
	return &Authority{
		userCAs: make(map[string]ed25519.PublicKey),
		hostCAs: make(map[string]ed25519.PublicKey),
		now:     time.Now,
	}
}

// RegisterUserCA 把一把 CA 公钥登记为用户CA，可用于验证用户证书。
func (a *Authority) RegisterUserCA(pub ed25519.PublicKey) {
	a.userCAs[Fingerprint(pub)] = pub
}

// RegisterHostCA 把一把 CA 公钥登记为主机CA，仅用于验证主机证书。
func (a *Authority) RegisterHostCA(pub ed25519.PublicKey) {
	a.hostCAs[Fingerprint(pub)] = pub
}

// VerifyUserCertificate 验证一张用户证书：
//  1. 证书类型必须是用户证书；
//  2. 当前时间必须在证书有效期内；
//  3. 签发密钥必须登记为用户CA（仅登记为主机CA的密钥一律拒绝）；
//  4. 签名必须正确；
//  5. principal 非空时必须出现在证书的允许主体列表中。
func (a *Authority) VerifyUserCertificate(cert *Certificate, principal string) error {
	if cert.Type != CertTypeUser {
		return ErrNotUserCert
	}

	now := uint64(a.now().Unix())
	if now < cert.ValidAfter || now > cert.ValidBefore {
		return ErrExpired
	}

	caPub, err := parseSignatureKey(cert.SignatureKey)
	if err != nil {
		return err
	}
	fp := Fingerprint(caPub)
	userCA, ok := a.userCAs[fp]
	if !ok {
		if _, isHostCA := a.hostCAs[fp]; isHostCA {
			return ErrHostCAKeyNotUserCA
		}
		return ErrUnknownCA
	}

	if !verifySignature(userCA, cert.signed, cert.Signature) {
		return ErrBadSignature
	}

	if principal != "" && !principalAllowed(cert.ValidPrincipals, principal) {
		return fmt.Errorf("%w: %q", ErrPrincipal, principal)
	}
	return nil
}

// parseSignatureKey 从证书中提取 CA 公钥。
func parseSignatureKey(blob []byte) (ed25519.PublicKey, error) {
	r := &wireReader{data: blob}
	algo := r.string()
	key := r.string()
	if r.err != nil || len(r.data) != 0 {
		return nil, errWireFormat
	}
	if string(algo) != keyAlgorithm {
		return nil, fmt.Errorf("netsec: unsupported CA key algorithm %q", algo)
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, errors.New("netsec: invalid CA public key in certificate")
	}
	return ed25519.PublicKey(key), nil
}

// verifySignature 校验签名 blob 对 signed 字节的 Ed25519 签名。
func verifySignature(caPub ed25519.PublicKey, signed, sigBlob []byte) bool {
	r := &wireReader{data: sigBlob}
	algo := r.string()
	sig := r.string()
	if r.err != nil || len(r.data) != 0 {
		return false
	}
	if string(algo) != sigAlgorithm {
		return false
	}
	return ed25519.Verify(caPub, signed, sig)
}

func principalAllowed(principals []string, principal string) bool {
	for _, p := range principals {
		if p == principal {
			return true
		}
	}
	return false
}
