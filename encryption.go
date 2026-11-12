package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	authSecretLen = 16
	recordSize    = 4096
	saltLen       = 16
	cekLen        = 16
	nonceLen      = 12
)

// ValidateSubscriptionKeys 校验 RFC 8291 加密所需的用户代理密钥材料。
func ValidateSubscriptionKeys(keys SubscriptionKeys) error {
	if len(keys.Auth) != authSecretLen {
		return fmt.Errorf("netsec: auth secret must be %d bytes", authSecretLen)
	}
	if len(keys.P256DH) != uncompressedPointLen || keys.P256DH[0] != 0x04 {
		return errors.New("netsec: p256dh must be a 65-byte uncompressed P-256 point")
	}
	if _, err := parseUncompressedPoint(keys.P256DH); err != nil {
		return err
	}
	return nil
}

// EncryptPayload 按 RFC 8291 生成自描述的 aes128gcm 消息体，
// 头部内嵌 salt、记录大小与临时公钥，推送服务与用户代理可直接解密。
func EncryptPayload(keys SubscriptionKeys, plaintext []byte) ([]byte, error) {
	if err := ValidateSubscriptionKeys(keys); err != nil {
		return nil, err
	}
	uaECDH, err := parseECDHPublic(keys.P256DH)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate ephemeral key: %w", err)
	}
	asPublic := ephemeral.PublicKey().Bytes() // 65 字节未压缩点

	ecdhSecret, err := ephemeral.ECDH(uaECDH)
	if err != nil {
		return nil, fmt.Errorf("netsec: ECDH: %w", err)
	}

	// RFC 8291 §3.4 第一步：PRK_key = HMAC(auth_secret, ecdh_secret)。
	authPRK := hkdfExtract(keys.Auth, ecdhSecret)

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("netsec: generate salt: %w", err)
	}

	// 第二步：IM = Expand(PRK_key, "WebPush: info" || 0x00 || ua_public || as_public, 32)。
	keyInfo := buildKeyInfo(keys.P256DH, asPublic)
	intermediateIKM := hkdfExpand(authPRK, keyInfo, sha256.Size)

	// 第三步：PRK = HMAC(salt, IM)，再派生 CEK 与 nonce。
	recordPRK := hkdfExtract(salt, intermediateIKM)
	cek := hkdfExpand(recordPRK, contentEncodingInfo("aes128gcm", cekLen), cekLen)
	nonce := hkdfExpand(recordPRK, contentEncodingInfo("nonce", nonceLen), nonceLen)

	// 记录 = 明文 || 0x02（填充定界符），本实现仅产生单条记录。
	record := make([]byte, 0, len(plaintext)+1)
	record = append(record, plaintext...)
	record = append(record, 0x02)
	if len(record) > recordSize {
		return nil, errors.New("netsec: payload exceeds the supported record size")
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// aes128gcm 自描述头部：salt(16) || rs(uint32) || idlen(uint8) || keyid。
	header := make([]byte, 0, saltLen+4+1+len(asPublic)+len(ciphertext))
	header = append(header, salt...)
	var recordSizeBytes [4]byte
	binary.BigEndian.PutUint32(recordSizeBytes[:], recordSize)
	header = append(header, recordSizeBytes[:]...)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)
	return append(header, ciphertext...), nil
}

func parseECDHPublic(raw []byte) (*ecdh.PublicKey, error) {
	ecdsaKey, err := parseUncompressedPoint(raw)
	if err != nil {
		return nil, err
	}
	return ecdsaKey.ECDH()
}

// ECDSAPublicToECDH 便于测试与复用：将 *ecdsa.PublicKey 转为 crypto/ecdh 公钥。
func ECDSAPublicToECDH(key *ecdsa.PublicKey) (*ecdh.PublicKey, error) {
	if key == nil || key.Curve != elliptic.P256() {
		return nil, errors.New("netsec: nil or non-P-256 public key")
	}
	return key.ECDH()
}

func buildKeyInfo(uaPublic, asPublic []byte) []byte {
	info := make([]byte, 0, len("WebPush: info")+1+len(uaPublic)+len(asPublic))
	info = append(info, []byte("WebPush: info")...)
	info = append(info, 0x00)
	info = append(info, uaPublic...)
	info = append(info, asPublic...)
	return info
}

func contentEncodingInfo(encoding string, length int) []byte {
	prefix := "Content-Encoding: " + encoding
	info := make([]byte, 0, len(prefix)+1+2)
	info = append(info, []byte(prefix)...)
	info = append(info, 0x00)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(length))
	info = append(info, size[:]...)
	return info
}

// hkdfExtract 对应 RFC 5869 的 Extract：HMAC(salt, ikm)。
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand 对应 RFC 5869 的 Expand，length 不超过 HashLen 时仅一轮。
func hkdfExpand(prk, info []byte, length int) []byte {
	if length > sha256.Size {
		// 本组件所有派生长度均不超过 32 字节，单轮足够。
		panic("netsec: unsupported HKDF expand length")
	}
	mac := hmac.New(sha256.New, prk)
	mac.Write(info)
	mac.Write([]byte{0x01})
	return mac.Sum(nil)[:length]
}
