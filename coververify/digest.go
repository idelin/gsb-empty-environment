package coververify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SumDigest 返回字节内容的 SHA-256 十六进制摘要。
func SumDigest(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest(hex.EncodeToString(sum[:]))
}

// JSONDigest 返回值的确定性 JSON 摘要。
//
// 依赖 encoding/json 对结构体按字段声明序、对 map 按键排序输出的确定性，宿主可
// 用它为状态、输入和输出构造可复现的内容摘要。
func JSONDigest(value any) (Digest, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return SumDigest(raw), nil
}
