package shareguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
)

// CanonicalAliveSet 返回存活集合的规范序列化形式：按字典序排序后，
// 以逗号连接。守卫在调用 SignatureVerifier 前已完成排序，这里仍做一次
// 防御性排序，保证独立使用该函数时结果确定。
//
// 逗号分隔要求客户端标识本身不含逗号；若标识空间允许任意字节，
// 调用方应改用带长度前缀的自定义编码并实现自己的 SignatureVerifier。
func CanonicalAliveSet(aliveSet []string) []byte {
	sorted := append([]string(nil), aliveSet...)
	sort.Strings(sorted)
	return []byte(strings.Join(sorted, ","))
}

// HMACVerifier 是基于 HMAC-SHA256 的 SignatureVerifier 参考实现。
//
// 每个客户端持有一个独立密钥，适用于测试或客户端与验证端已通过带外
// 通道共享对称密钥的部署；生产环境若使用非对称签名（如 Ed25519），
// 可实现同等接口替换它。
type HMACVerifier struct {
	keys map[string][]byte
}

// NewHMACVerifier 以“客户端标识 -> 共享密钥”映射创建验证器。
// map 会被复制，调用方后续修改原 map 不影响验证器。
func NewHMACVerifier(keys map[string][]byte) *HMACVerifier {
	copied := make(map[string][]byte, len(keys))
	for id, key := range keys {
		copied[id] = append([]byte(nil), key...)
	}
	return &HMACVerifier{keys: copied}
}

// SignAliveSet 用 signer 的密钥对存活集合生成 HMAC 标签，供测试或
// 客户端本地构造签名使用。
func (v *HMACVerifier) SignAliveSet(signer string, aliveSet []string) ([]byte, error) {
	key, ok := v.keys[signer]
	if !ok {
		return nil, errors.New("shareguard: no HMAC key registered for signer " + signer)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(CanonicalAliveSet(aliveSet))
	return mac.Sum(nil), nil
}

// VerifyAliveSet 实现 SignatureVerifier，使用恒定时间比较校验标签。
func (v *HMACVerifier) VerifyAliveSet(signer string, aliveSet []string, signature []byte) error {
	key, ok := v.keys[signer]
	if !ok {
		return errors.New("no HMAC key registered for signer " + signer)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(CanonicalAliveSet(aliveSet))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(expected, signature) != 1 {
		return errors.New("HMAC tag mismatch")
	}
	return nil
}

var _ SignatureVerifier = (*HMACVerifier)(nil)
