package cryptoplan

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Issuer 代表持有宿主 Ed25519 私钥的签发方，用于签发设备证书与别名边。
type Issuer struct {
	PrivateKey ed25519.PrivateKey
}

// NewIssuer 生成一把新的宿主 Ed25519 密钥并返回签发器与验签公钥。
func NewIssuer() (*Issuer, ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return &Issuer{PrivateKey: priv}, pub, nil
}

// certPayload 是参与签名的证书规范载荷，字段顺序固定。
type certPayload struct {
	Type        string    `json:"type"`
	CertID      string    `json:"certID"`
	PrincipalID string    `json:"principalID"`
	DeviceID    string    `json:"deviceID"`
	Generation  uint64    `json:"generation"`
	KeyID       string    `json:"keyID"`
	PublicKey   string    `json:"publicKey"`
	Algorithms  []string  `json:"algorithms"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
}

// edgePayload 是参与签名的别名边规范载荷，字段顺序固定。
type edgePayload struct {
	Type        string    `json:"type"`
	EdgeID      string    `json:"edgeID"`
	Address     string    `json:"address"`
	PrincipalID string    `json:"principalID"`
	DisplayName string    `json:"displayName"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
}

func canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// KeyIDFromPublicKey 返回设备公钥的规范 KeyID：公钥原始字节的 SHA-256 十六进制。
func KeyIDFromPublicKey(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// SignDeviceCert 用宿主私钥签发设备能力声明，自动填充签名并回填 KeyID。
func (i *Issuer) SignDeviceCert(cert *DeviceCert) error {
	if i == nil || len(i.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: 宿主签发私钥缺失", ErrInvalidInput)
	}
	if err := cert.validateShape(); err != nil {
		return err
	}
	if len(cert.KeyID) == 0 {
		cert.KeyID = KeyIDFromPublicKey(cert.PublicKey)
	}
	payload, err := canonical(cert.toPayload())
	if err != nil {
		return err
	}
	cert.Signature = ed25519.Sign(i.PrivateKey, payload)
	return nil
}

// SignAliasEdge 用宿主私钥签发地址归并边，自动填充签名。
func (i *Issuer) SignAliasEdge(edge *AliasEdge) error {
	if i == nil || len(i.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: 宿主签发私钥缺失", ErrInvalidInput)
	}
	if err := edge.validateShape(); err != nil {
		return err
	}
	payload, err := canonical(edge.toPayload())
	if err != nil {
		return err
	}
	edge.Signature = ed25519.Sign(i.PrivateKey, payload)
	return nil
}

func (c DeviceCert) toPayload() certPayload {
	return certPayload{
		Type:        "device-cert/v1",
		CertID:      c.CertID,
		PrincipalID: c.PrincipalID,
		DeviceID:    c.DeviceID,
		Generation:  c.Generation,
		KeyID:       c.KeyID,
		PublicKey:   hex.EncodeToString(c.PublicKey),
		Algorithms:  append([]string(nil), c.Algorithms...),
		NotBefore:   c.NotBefore.UTC(),
		NotAfter:    c.NotAfter.UTC(),
	}
}

func (e AliasEdge) toPayload() edgePayload {
	return edgePayload{
		Type:        "alias-edge/v1",
		EdgeID:      e.EdgeID,
		Address:     normalizeAddress(e.Address),
		PrincipalID: e.PrincipalID,
		DisplayName: e.DisplayName,
		NotBefore:   e.NotBefore.UTC(),
		NotAfter:    e.NotAfter.UTC(),
	}
}

func (c DeviceCert) validateShape() error {
	switch {
	case c.CertID == "":
		return fmt.Errorf("%w: 证书缺少 CertID", ErrInvalidInput)
	case c.PrincipalID == "":
		return fmt.Errorf("%w: 证书缺少 PrincipalID", ErrInvalidInput)
	case c.DeviceID == "":
		return fmt.Errorf("%w: 证书缺少 DeviceID", ErrInvalidInput)
	case len(c.PublicKey) == 0:
		return fmt.Errorf("%w: 证书缺少设备公钥", ErrInvalidInput)
	case len(c.Algorithms) == 0:
		return fmt.Errorf("%w: 证书未授权任何算法", ErrInvalidInput)
	case !c.NotBefore.Before(c.NotAfter):
		return fmt.Errorf("%w: 证书有效期必须满足 NotBefore < NotAfter", ErrInvalidInput)
	}
	for _, alg := range c.Algorithms {
		if alg == "" {
			return fmt.Errorf("%w: 证书存在空算法标识", ErrInvalidInput)
		}
	}
	return nil
}

func (e AliasEdge) validateShape() error {
	switch {
	case e.EdgeID == "":
		return fmt.Errorf("%w: 别名边缺少 EdgeID", ErrInvalidInput)
	case normalizeAddress(e.Address) == "":
		return fmt.Errorf("%w: 别名边缺少地址", ErrInvalidInput)
	case e.PrincipalID == "":
		return fmt.Errorf("%w: 别名边缺少 PrincipalID", ErrInvalidInput)
	case !e.NotBefore.Before(e.NotAfter):
		return fmt.Errorf("%w: 别名边有效期必须满足 NotBefore < NotAfter", ErrInvalidInput)
	}
	return nil
}

// validAt 采用半开区间 [NotBefore, NotAfter)：终点时刻已失效。
func validAt(notBefore, notAfter, t time.Time) bool {
	return !t.Before(notBefore) && t.Before(notAfter)
}

// VerifyDeviceCert 用宿主公钥验证证书签名与结构。
func VerifyDeviceCert(cert DeviceCert, hostKey ed25519.PublicKey) error {
	if len(hostKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: 宿主公钥缺失", ErrInvalidSignature)
	}
	if err := cert.validateShape(); err != nil {
		return err
	}
	if len(cert.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: 证书签名长度非法", ErrInvalidSignature)
	}
	expectedKeyID := KeyIDFromPublicKey(cert.PublicKey)
	if cert.KeyID != expectedKeyID {
		return fmt.Errorf("%w: 证书 KeyID 与设备公钥不一致", ErrInvalidSignature)
	}
	payload, err := canonical(cert.toPayload())
	if err != nil {
		return err
	}
	if !ed25519.Verify(hostKey, payload, cert.Signature) {
		return fmt.Errorf("%w: 证书 %s", ErrInvalidSignature, cert.CertID)
	}
	return nil
}

// VerifyAliasEdge 用宿主公钥验证别名边签名与结构。
func VerifyAliasEdge(edge AliasEdge, hostKey ed25519.PublicKey) error {
	if len(hostKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: 宿主公钥缺失", ErrInvalidSignature)
	}
	if err := edge.validateShape(); err != nil {
		return err
	}
	if len(edge.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: 别名边签名长度非法", ErrInvalidSignature)
	}
	payload, err := canonical(edge.toPayload())
	if err != nil {
		return err
	}
	if !ed25519.Verify(hostKey, payload, edge.Signature) {
		return fmt.Errorf("%w: 别名边 %s", ErrInvalidSignature, edge.EdgeID)
	}
	return nil
}

// normalizeAddress 把邮件地址规范化为小写并去除首尾空白，作为唯一比较口径。
func normalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}
