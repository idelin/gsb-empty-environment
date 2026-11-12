package netsec

import (
	"crypto/hmac"
	"fmt"
	"hash"
)

// HMACTagger 以标准 HMAC 实现注入式 MAC 接口，可直接对接运行时的
// 指针认证标签需求。标签长度等于底层哈希输出长度（如 SHA-256 为 32 字节）。
type HMACTagger struct {
	hashFunc func() hash.Hash
	key      []byte
}

// NewHMACTagger 用给定哈希构造器（如 sha256.New）与密钥构造 HMAC 标签器。
// 密钥会被拷贝，避免调用方随后修改切片影响标签。
func NewHMACTagger(hashFunc func() hash.Hash, key []byte) (*HMACTagger, error) {
	if hashFunc == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("netsec: empty HMAC key")
	}
	return &HMACTagger{
		hashFunc: hashFunc,
		key:      append([]byte(nil), key...),
	}, nil
}

// Tag 对输入计算 HMAC，返回独立分配的标签切片。
func (h *HMACTagger) Tag(input []byte) []byte {
	mac := hmac.New(h.hashFunc, h.key)
	mac.Write(input)
	return mac.Sum(nil)
}

// Equal 使用 hmac.Equal（常量时间）比较标签，长度不同也直接安全返回 false。
func (h *HMACTagger) Equal(expected, actual []byte) bool {
	return hmac.Equal(expected, actual)
}

// 编译期保证 HMACTagger 满足 MAC 接口与常量时间比较语义。
var _ MAC = (*HMACTagger)(nil)
