package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// EVM security.evm 扩展属性首字节的类型标识。
//
//   - TypeHMAC（2）：本机 HMAC，覆盖 inode 号与 i_generation，
//     因此离开原 inode（复制、离线搬运）后必然校验失败。
//   - TypePortableSignature（5）：可移植签名，刻意不覆盖 inode/generation，
//     允许文件在不同主机/inode 间移动，只保护安全属性本身。
const (
	TypeHMAC              byte = 2
	TypePortableSignature byte = 5
)

const hmacDigestSize = sha256.Size // 32 字节，SHA-256 摘要

// 错误集合，供调用方做 sentinel 判断。
var (
	ErrEmptyHMACKey   = errors.New("netsec: EVM host HMAC key must not be empty")
	ErrUnsupportedKey = errors.New("netsec: unsupported portable signature public key")
)

// PublicKey 抽象可移植签名的受信验证公钥，避免评估器绑定具体算法。
type PublicKey interface {
	Verify(message, signature []byte) bool
}

// Signer 是生成可移植签名所需的私钥抽象，仅出现在签名/测试路径上。
type Signer interface {
	PublicKey
	Sign(message []byte) ([]byte, error)
}

// Ed25519PublicKey 把标准库 ed25519.PublicKey 适配为受信公钥。
type Ed25519PublicKey ed25519.PublicKey

func (k Ed25519PublicKey) Verify(message, signature []byte) bool {
	return ed25519.Verify(ed25519.PublicKey(k), message, signature)
}

// Ed25519PrivateKey 适配 ed25519 私钥，用于在构建期生成可移植签名。
type Ed25519PrivateKey ed25519.PrivateKey

func (k Ed25519PrivateKey) Verify(message, signature []byte) bool {
	pub := ed25519.PrivateKey(k).Public().(ed25519.PublicKey)
	return ed25519.Verify(pub, message, signature)
}

func (k Ed25519PrivateKey) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(ed25519.PrivateKey(k), message), nil
}

// canonicalInput 固定安全属性参与计算的顺序：
// 类型字节 → security.ima → security.selinux → inode → i_generation
// → uid → gid → mode。
//
// 可移植签名（类型 5）不覆盖 inode 与 i_generation，这两个字段会被跳过；
// 其余字段顺序保持不变，保证两种形式对同一组安全属性编码一致。
//
// 第二个返回值是 inode 字段在缓冲中的字节偏移；includeInode 为 false 时
// 指向 uid 字段位置，调用方不应在该情形下改写 inode/generation。
func canonicalInput(meta FileMeta, typ byte, includeInode bool) ([]byte, int) {
	var buf bytes.Buffer
	buf.Grow(256)

	appendBytes := func(b []byte) {
		var length [4]byte
		binary.LittleEndian.PutUint32(length[:], uint32(len(b)))
		buf.Write(length[:])
		buf.Write(b)
	}

	buf.WriteByte(typ)
	appendBytes(meta.IMA)
	appendBytes(meta.SELinux)
	inodeOffset := buf.Len()
	if includeInode {
		var ino [8]byte
		binary.LittleEndian.PutUint64(ino[:], meta.Ino)
		buf.Write(ino[:])
		var gen [4]byte
		binary.LittleEndian.PutUint32(gen[:], meta.Generation)
		buf.Write(gen[:])
	}
	var uid [4]byte
	binary.LittleEndian.PutUint32(uid[:], meta.UID)
	buf.Write(uid[:])
	var gid [4]byte
	binary.LittleEndian.PutUint32(gid[:], meta.GID)
	buf.Write(gid[:])
	var mode [4]byte
	binary.LittleEndian.PutUint32(mode[:], meta.Mode)
	buf.Write(mode[:])

	return buf.Bytes(), inodeOffset
}

// evmHMAC 使用主机密钥对规范输入做 HMAC-SHA256。
func evmHMAC(key, input []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(input)
	return mac.Sum(nil)
}

// hmacEqual 以常量时间比较两个摘要。
func hmacEqual(a, b []byte) bool {
	return hmac.Equal(a, b)
}

// NewHMACAttr 按本机 HMAC 形式（类型字节 2）构造完整的 security.evm 值：
// 1 字节类型 + 32 字节 HMAC-SHA256。
func NewHMACAttr(hostKey []byte, meta FileMeta) ([]byte, error) {
	if len(hostKey) == 0 {
		return nil, ErrEmptyHMACKey
	}
	input, _ := canonicalInput(meta, TypeHMAC, true)
	digest := evmHMAC(hostKey, input)
	attr := make([]byte, 0, 1+len(digest))
	attr = append(attr, TypeHMAC)
	attr = append(attr, digest...)
	return attr, nil
}

// NewPortableSignatureAttr 按可移植签名形式（类型字节 5）构造 security.evm 值：
// 1 字节类型 + 签名字节。规范输入刻意不含 inode 与 i_generation。
func NewPortableSignatureAttr(signer Signer, meta FileMeta) ([]byte, error) {
	input, _ := canonicalInput(meta, TypePortableSignature, false)
	signature, err := signer.Sign(input)
	if err != nil {
		return nil, err
	}
	attr := make([]byte, 0, 1+len(signature))
	attr = append(attr, TypePortableSignature)
	attr = append(attr, signature...)
	return attr, nil
}
