package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
)

// Block 是归档中的一个数据块。归档自带每个块的摘要，
// 审计时对抽中的块重新计算摘要并与归档内记录比对。
type Block struct {
	ID     string   `json:"id"`
	Length *big.Int `json:"logical_length"` // 逻辑长度，作为抽样权重；大整数，可超过 64 位
	Digest string   `json:"sha256"`         // 归档自带的块摘要（十六进制）
	Data   []byte   `json:"data"`           // 块内容（JSON 中为 base64）
}

// Archive 是由若干块组成的归档。
type Archive struct {
	Blocks []*Block `json:"blocks"`
}

// TotalLength 返回全部块逻辑长度之和，用大整数计算，不会溢出。
func (a *Archive) TotalLength() *big.Int {
	total := new(big.Int)
	for _, b := range a.Blocks {
		total.Add(total, b.Length)
	}
	return total
}

// VerifyDigest 重新计算块内容摘要并与归档自带摘要比对。
func (b *Block) VerifyDigest() bool {
	sum := sha256.Sum256(b.Data)
	return hex.EncodeToString(sum[:]) == b.Digest
}

// Validate 做基本一致性检查：块 ID 唯一、长度为正、摘要格式合法。
func (a *Archive) Validate() error {
	if len(a.Blocks) == 0 {
		return fmt.Errorf("归档不包含任何块")
	}
	seen := make(map[string]bool)
	for i, b := range a.Blocks {
		if b.ID == "" {
			return fmt.Errorf("第 %d 个块缺少 id", i)
		}
		if seen[b.ID] {
			return fmt.Errorf("块 id 重复: %q", b.ID)
		}
		seen[b.ID] = true
		if b.Length == nil || b.Length.Sign() <= 0 {
			return fmt.Errorf("块 %q 的逻辑长度必须为正整数", b.ID)
		}
		if _, err := hex.DecodeString(b.Digest); err != nil || len(b.Digest) != 64 {
			return fmt.Errorf("块 %q 的 sha256 摘要格式非法", b.ID)
		}
	}
	return nil
}

// LoadArchive 从 JSON 文件读取归档。
func LoadArchive(path string) (*Archive, error) {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Archive
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("解析归档失败: %v", err)
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return &a, nil
}

// Save 将归档写入 JSON 文件。
func (a *Archive) Save(path string) error {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, raw, 0644)
}
