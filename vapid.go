package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// VAPID 公钥为 P-256 曲线上的未压缩点，固定 65 字节。
	uncompressedPointLen = 65
	// JWT 有效期，签发时起 12 小时，远大于一次推送的耗时。
	vapidTokenTTL = 12 * time.Hour
)

// VAPIDCredentials 保存服务端 VAPID 签名密钥与联系信息（RFC 8292）。
type VAPIDCredentials struct {
	PrivateKey *ecdsa.PrivateKey
	// Subject 为 mailto: 或 https 形式的管理员联系地址。
	Subject string
	// now 用于测试中控制时间，默认取 time.Now。
	now func() time.Time
}

// NewVAPIDCredentials 生成一套新的 P-256 VAPID 凭据。
func NewVAPIDCredentials(subject string) (*VAPIDCredentials, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate VAPID key: %w", err)
	}
	return &VAPIDCredentials{PrivateKey: privateKey, Subject: subject}, nil
}

// ParseVAPIDPublicKey 接受以下任一种 P-256 公钥形式：
// 65 字节未压缩点（可作为原始字节或 base64url 文本传入），
// 或 PKIX/SPKI DER（原始字节或 PEM 之外的 base64 文本）。
func ParseVAPIDPublicKey(publicKey []byte) (*ecdsa.PublicKey, error) {
	if len(publicKey) == 0 {
		return nil, errors.New("netsec: empty VAPID public key")
	}
	if key, err := parseUncompressedPoint(publicKey); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKIXPublicKey(publicKey); err == nil {
		ecdsaKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("netsec: SPKI key is not an ECDSA public key")
		}
		if ecdsaKey.Curve != elliptic.P256() {
			return nil, errors.New("netsec: VAPID public key must use P-256")
		}
		return ecdsaKey, nil
	}
	return nil, errors.New("netsec: unrecognized VAPID public key encoding")
}

// DecodeVAPIDPublicKey 解码 base64url（允许填充）文本形式的公钥，
// 先按 65 字节原始点尝试，再按 SPKI DER 尝试。
func DecodeVAPIDPublicKey(encoded string) (*ecdsa.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return nil, fmt.Errorf("netsec: decode VAPID public key: %w", err)
	}
	return ParseVAPIDPublicKey(raw)
}

func parseUncompressedPoint(raw []byte) (*ecdsa.PublicKey, error) {
	if len(raw) != uncompressedPointLen || raw[0] != 0x04 {
		return nil, errors.New("netsec: not an uncompressed P-256 point")
	}
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(raw[1:33])
	y := new(big.Int).SetBytes(raw[33:65])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("netsec: point is not on P-256")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// EncodeVAPIDPublicKey 返回 65 字节未压缩点编码（UA 与推送服务使用的形式）。
func EncodeVAPIDPublicKey(publicKey *ecdsa.PublicKey) ([]byte, error) {
	return marshalPoint(publicKey)
}

// MarshalVAPIDPublicKeySPKI 返回 PKIX/SPKI DER 编码，便于跨系统交换公钥。
func MarshalVAPIDPublicKeySPKI(publicKey *ecdsa.PublicKey) ([]byte, error) {
	return marshalSPKI(publicKey)
}

func marshalSPKI(publicKey *ecdsa.PublicKey) ([]byte, error) {
	if publicKey == nil || publicKey.Curve != elliptic.P256() {
		return nil, errors.New("netsec: nil or non-P-256 public key")
	}
	return x509.MarshalPKIXPublicKey(publicKey)
}

// VAPIDFingerprint 返回公钥的 SHA-256 指纹（32 字节），
// 作为订阅绑定与租户隔离的公钥标识。
func VAPIDFingerprint(publicKey *ecdsa.PublicKey) ([32]byte, error) {
	raw, err := marshalPoint(publicKey)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func marshalPoint(publicKey *ecdsa.PublicKey) ([]byte, error) {
	if publicKey == nil || publicKey.Curve != elliptic.P256() {
		return nil, errors.New("netsec: nil or non-P-256 public key")
	}
	raw := make([]byte, 0, uncompressedPointLen)
	raw = append(raw, 0x04)
	raw = append(raw, fixedWidthBytes(publicKey.X, 32)...)
	raw = append(raw, fixedWidthBytes(publicKey.Y, 32)...)
	return raw, nil
}

func fixedWidthBytes(value *big.Int, width int) []byte {
	raw := value.Bytes()
	if len(raw) >= width {
		return raw[len(raw)-width:]
	}
	padded := make([]byte, width)
	copy(padded[width-len(raw):], raw)
	return padded
}

// Authorization 生成推送服务期望的 VAPID Authorization 头：
// "vapid-t=" + base64url(JWT) + ",k=" + base64url(公钥)。
func (c *VAPIDCredentials) Authorization(audience string) (string, error) {
	if c == nil || c.PrivateKey == nil {
		return "", errors.New("netsec: missing VAPID private key")
	}
	token, err := c.signJWT(audience)
	if err != nil {
		return "", err
	}
	publicKey, err := marshalPoint(&c.PrivateKey.PublicKey)
	if err != nil {
		return "", err
	}
	return "vapid-t=" + token + ",k=" + base64.RawURLEncoding.EncodeToString(publicKey), nil
}

func (c *VAPIDCredentials) signJWT(audience string) (string, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	header := map[string]string{"typ": "JWT", "alg": "ES256"}
	claims := map[string]any{
		"aud": audience,
		"exp": now().Add(vapidTokenTTL).Unix(),
		"iat": now().Unix(),
		"sub": c.Subject,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) +
		"." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, c.PrivateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("netsec: sign VAPID JWT: %w", err)
	}
	// ES256 签名为 R||S 各 32 字节的定长拼接。
	signature := append(fixedWidthBytes(r, 32), fixedWidthBytes(s, 32)...)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
