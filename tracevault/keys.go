package tracevault

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// Keys 保存由单个主密钥按用途分离派生出的子密钥。
type Keys struct {
	pseudonym []byte // 假名化 PRF 密钥
	grid      []byte // 网格哈希盐
	record    []byte // 轨迹记录加密根密钥
	audit     []byte // 审计凭证 MAC 密钥
}

func derive(master []byte, label string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("tracevault/" + label))
	return mac.Sum(nil)
}

// DeriveKeys 从 32 字节主密钥派生出各用途子密钥。
func DeriveKeys(master []byte) *Keys {
	return &Keys{
		pseudonym: derive(master, "pseudonym"),
		grid:      derive(master, "grid"),
		record:    derive(master, "record"),
		audit:     derive(master, "audit"),
	}
}

// AuditKey 导出审计子密钥，供监管方离线签发授权凭证。
func (k *Keys) AuditKey() []byte {
	return append([]byte(nil), k.audit...)
}

// pseudonymFor 计算实体在指定时间桶内的时变假名。
func (k *Keys) pseudonymFor(entityID string, bucket int64) string {
	mac := hmac.New(sha256.New, k.pseudonym)
	mac.Write([]byte(entityID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(bucket))
	mac.Write(buf[:])
	return hexEncode(mac.Sum(nil))
}

// recordKey 派生“实体 × 时间桶”粒度的轨迹记录加密密钥。
func (k *Keys) recordKey(entityID string, bucket int64) []byte {
	mac := hmac.New(sha256.New, k.record)
	mac.Write([]byte(entityID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(bucket))
	mac.Write(buf[:])
	return mac.Sum(nil)
}
