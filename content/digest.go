// Package content 实现供拉取器调用的内容库写入会话，
// 支持以 ref 命名的断点续传，并在恢复时从头重算摘要状态。
package content

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// 支持的摘要算法前缀。
const (
	AlgorithmSHA256 = "sha256"
	AlgorithmSHA512 = "sha512"
)

// Digest 标识一段内容的摘要，形如 "sha256:<hex>" 或 "sha512:<hex>"。
type Digest string

// ParseDigest 校验并返回摘要。仅接受 sha256 与 sha512 前缀，
// 且十六进制部分的长度必须与算法输出长度一致。
func ParseDigest(s string) (Digest, error) {
	algo, hexPart, found := strings.Cut(s, ":")
	if !found || algo == "" || hexPart == "" {
		return "", fmt.Errorf("content: digest %q missing algorithm prefix", s)
	}
	var wantLen int
	switch algo {
	case AlgorithmSHA256:
		wantLen = sha256.Size * 2
	case AlgorithmSHA512:
		wantLen = sha512.Size * 2
	default:
		return "", fmt.Errorf("content: unsupported digest algorithm %q", algo)
	}
	if len(hexPart) != wantLen {
		return "", fmt.Errorf("content: digest %q has hex length %d, want %d for %s", s, len(hexPart), wantLen, algo)
	}
	if hexPart != strings.ToLower(hexPart) {
		return "", fmt.Errorf("content: digest %q must be lowercase hex", s)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("content: digest %q is not valid hex", s)
	}
	return Digest(s), nil
}

// Validate 校验摘要是否合法。
func (d Digest) Validate() error {
	_, err := ParseDigest(string(d))
	return err
}

// Algorithm 返回摘要的算法前缀。
func (d Digest) Algorithm() string {
	algo, _, _ := strings.Cut(string(d), ":")
	return algo
}

// Encoded 返回摘要的十六进制部分。
func (d Digest) Encoded() string {
	_, hexPart, _ := strings.Cut(string(d), ":")
	return hexPart
}

// Hasher 返回该摘要算法对应的 hash.Hash。
func (d Digest) Hasher() (hash.Hash, error) {
	switch d.Algorithm() {
	case AlgorithmSHA256:
		return sha256.New(), nil
	case AlgorithmSHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("content: unsupported digest algorithm %q", d.Algorithm())
	}
}

// FromBytes 用指定算法计算内容的摘要。
func FromBytes(algo string, p []byte) (Digest, error) {
	d := Digest(algo + ":")
	h, err := d.Hasher()
	if err != nil {
		return "", err
	}
	if _, err := h.Write(p); err != nil {
		return "", err
	}
	return Digest(fmt.Sprintf("%s:%x", algo, h.Sum(nil))), nil
}
