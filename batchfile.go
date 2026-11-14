package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// 金额以最小货币单位（分）表示，避免浮点误差。

// Header 绑定企业、批次、日期、笔数与总金额。
type Header struct {
	EnterpriseID string // 企业标识
	BatchNo      string // 批次号
	Date         string // 业务日期，格式 YYYYMMDD
	Count        int    // 文件内明细笔数
	TotalAmount  int64  // 文件内有效明细金额合计（分）
}

// Detail 为文件内单笔付款指令。
type Detail struct {
	InstructionID string // 指令标识，文件内唯一
	PayerAccount  string // 付款账号
	PayeeAccount  string // 收款账号
	Amount        int64  // 金额（分）
}

// File 为客户签名后的批量付款文件。
type File struct {
	Header    Header
	Details   []Detail
	Signature []byte // 客户对 CanonicalBytes 的 HMAC-SHA256 签名
}

// KeyStore 按企业标识提供签名密钥。
type KeyStore interface {
	SignKey(enterpriseID string) ([]byte, error)
}

// KeyStoreFunc 适配函数为 KeyStore。
type KeyStoreFunc func(enterpriseID string) ([]byte, error)

func (f KeyStoreFunc) SignKey(enterpriseID string) ([]byte, error) {
	return f(enterpriseID)
}

var (
	ErrUnknownEnterprise = errors.New("netsec: unknown enterprise")
	ErrBadSignature      = errors.New("netsec: invalid file signature")
	ErrCountMismatch     = errors.New("netsec: detail count does not match header")
	ErrTotalMismatch     = errors.New("netsec: detail amount sum does not match header total")
	ErrConflictingDetail = errors.New("netsec: duplicate instruction id with conflicting content")
	ErrNonPositiveAmount = errors.New("netsec: detail amount must be positive")
	ErrEmptyInstruction  = errors.New("netsec: detail instruction id is empty")
)

func writeField(buf []byte, s string) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	buf = append(buf, n[:]...)
	return append(buf, s...)
}

func writeInt(buf []byte, v int64) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(v))
	return append(buf, n[:]...)
}

// CanonicalBytes 输出文件头与明细的规范化编码，用于签名与摘要。
// 明细按指令标识排序，使编码与明细在文件中的排列顺序无关。
func (f *File) CanonicalBytes() []byte {
	buf := make([]byte, 0, 256)
	buf = writeField(buf, "batchpay/v1")
	buf = writeField(buf, f.Header.EnterpriseID)
	buf = writeField(buf, f.Header.BatchNo)
	buf = writeField(buf, f.Header.Date)
	buf = writeInt(buf, int64(f.Header.Count))
	buf = writeInt(buf, f.Header.TotalAmount)

	sorted := make([]Detail, len(f.Details))
	copy(sorted, f.Details)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].InstructionID < sorted[j].InstructionID
	})
	for _, d := range sorted {
		buf = writeField(buf, d.InstructionID)
		buf = writeField(buf, d.PayerAccount)
		buf = writeField(buf, d.PayeeAccount)
		buf = writeInt(buf, d.Amount)
	}
	return buf
}

// Digest 返回文件摘要，作为幂等键。
func (f *File) Digest() [32]byte {
	return sha256.Sum256(f.CanonicalBytes())
}

// Sign 使用企业密钥对文件签名，供客户端构造文件使用。
func (f *File) Sign(key []byte) {
	mac := hmac.New(sha256.New, key)
	mac.Write(f.CanonicalBytes())
	f.Signature = mac.Sum(nil)
}

// VerifySignature 校验客户签名。
func (f *File) VerifySignature(keys KeyStore) error {
	key, err := keys.SignKey(f.Header.EnterpriseID)
	if err != nil {
		return err
	}
	if len(key) == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownEnterprise, f.Header.EnterpriseID)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(f.CanonicalBytes())
	if !hmac.Equal(mac.Sum(nil), f.Signature) {
		return ErrBadSignature
	}
	return nil
}

// Validate 校验文件头与明细的一致性，并按指令标识去重。
// 返回去重后的有效明细（同一指令标识重复且内容一致时只保留一笔）。
func (f *File) Validate() ([]Detail, error) {
	if f.Header.Count != len(f.Details) {
		return nil, fmt.Errorf("%w: header=%d actual=%d",
			ErrCountMismatch, f.Header.Count, len(f.Details))
	}
	seen := make(map[string]Detail, len(f.Details))
	unique := make([]Detail, 0, len(f.Details))
	var sum int64
	for _, d := range f.Details {
		if d.InstructionID == "" {
			return nil, ErrEmptyInstruction
		}
		if d.Amount <= 0 {
			return nil, fmt.Errorf("%w: instruction %s amount=%d",
				ErrNonPositiveAmount, d.InstructionID, d.Amount)
		}
		if prev, ok := seen[d.InstructionID]; ok {
			if prev != d {
				return nil, fmt.Errorf("%w: instruction %s",
					ErrConflictingDetail, d.InstructionID)
			}
			continue // 完全相同的重复明细，去重
		}
		seen[d.InstructionID] = d
		unique = append(unique, d)
		sum += d.Amount
	}
	if sum != f.Header.TotalAmount {
		return nil, fmt.Errorf("%w: header=%d actual=%d",
			ErrTotalMismatch, f.Header.TotalAmount, sum)
	}
	return unique, nil
}
