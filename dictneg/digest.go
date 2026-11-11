package dictneg

import (
	"crypto/sha256"
	"encoding/hex"
)

// DigestSize 是摘要的固定字节长度（SHA-256）。
const DigestSize = sha256.Size

// Digest 是字典内容的摘要。零值代表“无字典”（如空压头）。
type Digest [DigestSize]byte

// ZeroDigest 是 Digest 的零值，表示未绑定任何字典。
var ZeroDigest Digest

// SumDigest 计算字典内容的摘要。
func SumDigest(content []byte) Digest {
	return sha256.Sum256(content)
}

// IsZero 报告摘要是否为零值。
func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

// String 返回十六进制形式，便于日志与调试。
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// MarshalText 实现 encoding.TextMarshaler。
func (d Digest) MarshalText() ([]byte, error) {
	out := make([]byte, hex.EncodedLen(DigestSize))
	hex.Encode(out, d[:])
	return out, nil
}

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (d *Digest) UnmarshalText(text []byte) error {
	raw, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	if len(raw) != DigestSize {
		return ErrInvalidWire
	}
	copy(d[:], raw)
	return nil
}
