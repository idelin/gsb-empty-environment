package tracevault

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Credential 是司法审计授权凭证：仅对单个实体、有界时间段有效，
// 必须携带审计密钥对全部字段的 HMAC 签名，且存在过期时间。
type Credential struct {
	Issuer    string `json:"issuer"`     // 签发机构
	EntityID  string `json:"entity_id"`  // 授权目标实体
	From      int64  `json:"from"`       // 起始时间桶（含）
	To        int64  `json:"to"`         // 结束时间桶（含）
	ExpiresAt int64  `json:"expires_at"` // 凭证过期时间（Unix 秒）
	Nonce     string `json:"nonce"`      // 防重放随机数
	MAC       string `json:"mac"`        // 对上述字段的 HMAC-SHA256
}

func (c *Credential) macPayload() []byte {
	b, _ := json.Marshal(struct {
		Issuer    string `json:"issuer"`
		EntityID  string `json:"entity_id"`
		From      int64  `json:"from"`
		To        int64  `json:"to"`
		ExpiresAt int64  `json:"expires_at"`
		Nonce     string `json:"nonce"`
	}{c.Issuer, c.EntityID, c.From, c.To, c.ExpiresAt, c.Nonce})
	return b
}

func computeCredMAC(auditKey, payload []byte) []byte {
	mac := hmac.New(sha256.New, auditKey)
	mac.Write([]byte("tracevault/credential"))
	mac.Write(payload)
	return mac.Sum(nil)
}

// IssueCredential 由监管方使用审计密钥签发授权凭证。
func IssueCredential(auditKey []byte, issuer, entityID string, from, to, expiresAt int64) (*Credential, error) {
	if entityID == "" {
		return nil, errors.New("tracevault: credential requires a target entity")
	}
	if to < from {
		return nil, errors.New("tracevault: credential time range is empty")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	c := &Credential{
		Issuer:    issuer,
		EntityID:  entityID,
		From:      from,
		To:        to,
		ExpiresAt: expiresAt,
		Nonce:     hex.EncodeToString(nonce),
	}
	c.MAC = hex.EncodeToString(computeCredMAC(auditKey, c.macPayload()))
	return c, nil
}

// verify 校验凭证签名、有效期与时间范围。
func (c *Credential) verify(auditKey []byte, now int64) error {
	if c == nil {
		return errors.New("tracevault: nil credential")
	}
	macBytes, err := hex.DecodeString(c.MAC)
	if err != nil {
		return errors.New("tracevault: malformed credential MAC")
	}
	if !hmac.Equal(macBytes, computeCredMAC(auditKey, c.macPayload())) {
		return errors.New("tracevault: invalid credential MAC")
	}
	if now > c.ExpiresAt {
		return fmt.Errorf("tracevault: credential expired at %s", time.Unix(c.ExpiresAt, 0).UTC())
	}
	if c.To < c.From {
		return errors.New("tracevault: credential time range is empty")
	}
	return nil
}

// AuditEntry 是一条带哈希链的审计日志记录。
type AuditEntry struct {
	Seq      int64  `json:"seq"`
	Time     int64  `json:"time"`
	Action   string `json:"action"` // "reveal" / "reveal-denied"
	Issuer   string `json:"issuer"`
	EntityID string `json:"entity_id"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Records  int    `json:"records"`
	Detail   string `json:"detail,omitempty"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

func (e *AuditEntry) computeHash() string {
	b, _ := json.Marshal(struct {
		Seq      int64  `json:"seq"`
		Time     int64  `json:"time"`
		Action   string `json:"action"`
		Issuer   string `json:"issuer"`
		EntityID string `json:"entity_id"`
		From     int64  `json:"from"`
		To       int64  `json:"to"`
		Records  int    `json:"records"`
		Detail   string `json:"detail,omitempty"`
		PrevHash string `json:"prev_hash"`
	}{e.Seq, e.Time, e.Action, e.Issuer, e.EntityID, e.From, e.To, e.Records, e.Detail, e.PrevHash})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// AuditLog 是仅追加、哈希链保护的审计日志。
type AuditLog struct {
	entries []AuditEntry
}

// NewAuditLog 创建空审计日志。
func NewAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) append(action, issuer, entityID string, from, to int64, records int, detail string) {
	prev := "genesis"
	if n := len(l.entries); n > 0 {
		prev = l.entries[n-1].Hash
	}
	e := AuditEntry{
		Seq:      int64(len(l.entries)),
		Time:     time.Now().Unix(),
		Action:   action,
		Issuer:   issuer,
		EntityID: entityID,
		From:     from,
		To:       to,
		Records:  records,
		Detail:   detail,
		PrevHash: prev,
	}
	e.Hash = e.computeHash()
	l.entries = append(l.entries, e)
}

// Entries 返回审计日志副本。
func (l *AuditLog) Entries() []AuditEntry {
	return append([]AuditEntry(nil), l.entries...)
}

// Verify 校验审计日志哈希链的完整性，发现篡改即报错。
func (l *AuditLog) Verify() error {
	prev := "genesis"
	for i := range l.entries {
		e := &l.entries[i]
		if e.Seq != int64(i) {
			return fmt.Errorf("tracevault: audit entry %d has wrong seq %d", i, e.Seq)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("tracevault: audit entry %d breaks the hash chain", i)
		}
		if e.Hash != e.computeHash() {
			return fmt.Errorf("tracevault: audit entry %d hash mismatch (tampered)", i)
		}
		prev = e.Hash
	}
	return nil
}
