package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
)

// CaseMode 决定目录的规范化策略。
type CaseMode int

const (
	// CaseSensitive 规范名即原始 UTF-8 名。
	CaseSensitive CaseMode = iota
	// CaseFoldASCII 仅把 ASCII A-Z 折叠为 a-z，其他字节不变。
	CaseFoldASCII
)

// Policy 是目录的加密策略。目录创建后策略（含目录 nonce）不可变，
// 轮换仅推进 KeyGeneration。
type Policy struct {
	DirectoryID           [16]byte
	KeyGeneration         uint64
	DirectoryNonce        [32]byte
	CaseMode              CaseMode
	MaxRawNameBytes       int
	MaxCanonicalNameBytes int
}

// NewPolicy 生成带随机 directoryID 与 32 字节目录 nonce 的策略，密钥代次从 1 开始。
func NewPolicy(mode CaseMode, maxRawNameBytes, maxCanonicalNameBytes int) (Policy, error) {
	var p Policy
	if _, err := rand.Read(p.DirectoryID[:]); err != nil {
		return Policy{}, err
	}
	if _, err := rand.Read(p.DirectoryNonce[:]); err != nil {
		return Policy{}, err
	}
	p.KeyGeneration = 1
	p.CaseMode = mode
	p.MaxRawNameBytes = maxRawNameBytes
	p.MaxCanonicalNameBytes = maxCanonicalNameBytes
	if err := p.validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (p Policy) validate() error {
	if p.CaseMode != CaseSensitive && p.CaseMode != CaseFoldASCII {
		return ErrInvalidPolicy
	}
	if p.MaxRawNameBytes <= 0 || p.MaxCanonicalNameBytes <= 0 {
		return ErrInvalidPolicy
	}
	return nil
}

// digest 计算绑定指定密钥代次的目录策略摘要，用作 AEAD 附加数据的一部分。
func (p Policy) digest(gen uint64) [32]byte {
	h := sha256.New()
	h.Write([]byte("netsec/policy/v1"))
	h.Write(p.DirectoryID[:])
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], gen)
	h.Write(b[:])
	h.Write(p.DirectoryNonce[:])
	h.Write([]byte{byte(p.CaseMode)})
	binary.BigEndian.PutUint64(b[:], uint64(p.MaxRawNameBytes))
	h.Write(b[:])
	binary.BigEndian.PutUint64(b[:], uint64(p.MaxCanonicalNameBytes))
	h.Write(b[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
