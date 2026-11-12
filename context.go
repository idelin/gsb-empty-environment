package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Context 是绑定到数据密钥上的认证上下文。
// 密文数据密钥(EDK)离开原项目后, 缺少完全相同的上下文即无法解包。
type Context struct {
	Tenant      string // 租户/项目标识
	Purpose     string // 用途标识
	DataVersion uint64 // 数据版本
}

// 规范编码魔数与版本, 便于未来演进编码格式。
const contextEncodingMagic = "NETSEC-CTX\x00\x01"

// ErrContextFieldTooLong 表示上下文字段超出规范编码允许的长度。
var ErrContextFieldTooLong = errors.New("netsec: context field too long")

// Canonical 返回上下文的规范编码。
//
// 编码采用长度前缀, 消除字段拼接歧义:
//
//	magic || u16len(Tenant) || Tenant || u16len(Purpose) || Purpose || u64be(DataVersion)
//
// 相同的逻辑上下文永远编码为相同的字节序列; 不同的上下文永不碰撞。
func (c Context) Canonical() ([]byte, error) {
	if len(c.Tenant) > 0xffff || len(c.Purpose) > 0xffff {
		return nil, fmt.Errorf("%w: tenant=%d purpose=%d bytes",
			ErrContextFieldTooLong, len(c.Tenant), len(c.Purpose))
	}
	buf := make([]byte, 0, len(contextEncodingMagic)+2+len(c.Tenant)+2+len(c.Purpose)+8)
	buf = append(buf, contextEncodingMagic...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(c.Tenant)))
	buf = append(buf, c.Tenant...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(c.Purpose)))
	buf = append(buf, c.Purpose...)
	buf = binary.BigEndian.AppendUint64(buf, c.DataVersion)
	return buf, nil
}

// MustCanonical 同 Canonical, 但在编码失败时 panic。
// 仅供长度已知合法的调用方与测试使用。
func (c Context) MustCanonical() []byte {
	b, err := c.Canonical()
	if err != nil {
		panic(err)
	}
	return b
}

// Digest 返回上下文规范编码的 SHA-256 摘要, 用于缓存键。
func (c Context) Digest() ([sha256.Size]byte, error) {
	b, err := c.Canonical()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(b), nil
}
