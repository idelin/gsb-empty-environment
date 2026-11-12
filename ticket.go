package netsec

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	// maxStringLength 限制票据内字符串字段长度，防止畸形输入放大。
	maxStringLength = 4096
	// maxSessionDataSize 限制宿主会话数据大小（16 位长度前缀）。
	maxSessionDataSize = 1 << 16
)

// claims 是被 AEAD 完整保护的票据明文，绑定租户、协议选项、
// 认证等级和签发时的策略摘要。
type claims struct {
	tenant       string
	authLevel    AuthLevel
	minVersion   uint16
	maxVersion   uint16
	alpn         []string
	policyDigest [32]byte
	issuedAt     uint64
	expiresAt    uint64
	sessionData  []byte
}

// marshal 将声明编码为确定性字节串：所有变长字段带长度前缀，
// 整数使用大端序，便于跨平台与跨版本复现。
func (c claims) marshal() ([]byte, error) {
	if len(c.tenant) > maxStringLength {
		return nil, fmt.Errorf("%w: tenant too long", ErrMalformedTicket)
	}
	for _, proto := range c.alpn {
		if len(proto) > maxStringLength {
			return nil, fmt.Errorf("%w: alpn entry too long", ErrMalformedTicket)
		}
	}
	if len(c.sessionData) > maxSessionDataSize {
		return nil, fmt.Errorf("%w: session data too large", ErrMalformedTicket)
	}

	size := 2 + len(c.tenant) + 1 + 2 + 2 + 4 + len(c.policyDigest) + 8 + 8 + 2
	for _, proto := range c.alpn {
		size += 2 + len(proto)
	}
	size += len(c.sessionData)

	buf := make([]byte, 0, size)
	buf = appendString16(buf, c.tenant)
	buf = append(buf, byte(c.authLevel))
	buf = binary.BigEndian.AppendUint16(buf, c.minVersion)
	buf = binary.BigEndian.AppendUint16(buf, c.maxVersion)

	buf = binary.BigEndian.AppendUint32(buf, uint32(len(c.alpn)))
	for _, proto := range c.alpn {
		buf = appendString16(buf, proto)
	}

	buf = append(buf, c.policyDigest[:]...)
	buf = binary.BigEndian.AppendUint64(buf, c.issuedAt)
	buf = binary.BigEndian.AppendUint64(buf, c.expiresAt)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(c.sessionData)))
	buf = append(buf, c.sessionData...)
	return buf, nil
}

func appendString16(buf []byte, value string) []byte {
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(value)))
	return append(buf, value...)
}

// cursor 是带越界保护的只读字节游标。
type cursor struct {
	data []byte
	pos  int
	err  error
}

func (c *cursor) readByte() byte {
	if c.err != nil || c.pos+1 > len(c.data) {
		c.err = ErrMalformedTicket
		return 0
	}
	value := c.data[c.pos]
	c.pos++
	return value
}

func (c *cursor) readUint16() uint16 {
	if c.err != nil || c.pos+2 > len(c.data) {
		c.err = ErrMalformedTicket
		return 0
	}
	value := binary.BigEndian.Uint16(c.data[c.pos:])
	c.pos += 2
	return value
}

func (c *cursor) readUint32() uint32 {
	if c.err != nil || c.pos+4 > len(c.data) {
		c.err = ErrMalformedTicket
		return 0
	}
	value := binary.BigEndian.Uint32(c.data[c.pos:])
	c.pos += 4
	return value
}

func (c *cursor) readUint64() uint64 {
	if c.err != nil || c.pos+8 > len(c.data) {
		c.err = ErrMalformedTicket
		return 0
	}
	value := binary.BigEndian.Uint64(c.data[c.pos:])
	c.pos += 8
	return value
}

func (c *cursor) readBytes(length int) []byte {
	if c.err != nil || length < 0 || c.pos+length > len(c.data) {
		c.err = ErrMalformedTicket
		return nil
	}
	value := c.data[c.pos : c.pos+length]
	c.pos += length
	return value
}

func (c *cursor) readString16() string {
	length := int(c.readUint16())
	if c.err != nil {
		return ""
	}
	if length > maxStringLength {
		c.err = ErrMalformedTicket
		return ""
	}
	return string(c.readBytes(length))
}

// unmarshalClaims 解析票据明文，拒绝尾随字节与未知认证等级。
func unmarshalClaims(data []byte) (claims, error) {
	c := &cursor{data: data}
	result := claims{
		tenant:     c.readString16(),
		authLevel:  AuthLevel(c.readByte()),
		minVersion: c.readUint16(),
		maxVersion: c.readUint16(),
	}

	alpnCount := c.readUint32()
	if c.err == nil && alpnCount > maxStringLength {
		return claims{}, fmt.Errorf("%w: too many alpn entries", ErrMalformedTicket)
	}
	result.alpn = make([]string, 0, alpnCount)
	for i := uint32(0); i < alpnCount; i++ {
		result.alpn = append(result.alpn, c.readString16())
	}

	copy(result.policyDigest[:], c.readBytes(len(result.policyDigest)))
	result.issuedAt = c.readUint64()
	result.expiresAt = c.readUint64()
	sessionLength := int(c.readUint16())
	result.sessionData = append([]byte(nil), c.readBytes(sessionLength)...)

	if c.err != nil {
		return claims{}, c.err
	}
	if c.pos != len(data) {
		return claims{}, fmt.Errorf("%w: trailing bytes", ErrMalformedTicket)
	}
	switch result.authLevel {
	case AuthAnonymous, AuthCertificate, AuthMutual:
	default:
		return claims{}, fmt.Errorf("%w: unknown auth level %d", ErrMalformedTicket, result.authLevel)
	}
	if result.tenant == "" || result.expiresAt <= result.issuedAt {
		return claims{}, fmt.Errorf("%w: invalid claim fields", ErrMalformedTicket)
	}
	return result, nil
}

// unixNow 返回当前 Unix 时间戳（秒）。
func unixNow() uint64 {
	return uint64(time.Now().Unix())
}
