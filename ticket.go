package taskpkg

import (
	"crypto/ed25519"
	"encoding/binary"
	"time"
)

// Ticket 是跨租户移交所需的“目标绑定票据”。
//
// 票据把源租户、目标租户、对象 ID、移交时的版本与能力代次以及一次性
// nonce 固化在同一份待签名材料中，源租户与目标租户分别对完全相同的
// 规范字节流签名：
//   - 源租户签名证明“当前所有者同意移交”；
//   - 目标租户签名证明“指定目标接受绑定”，防止票据被转手给第三方。
//
// 缺任何一方签名、签名不匹配或目标被替换，宿主都拒绝移交。
type Ticket struct {
	ObjectID   string
	FromTenant string
	ToTenant   string
	Version    int64
	Generation int64
	Nonce      []byte
	ExpiresAt  time.Time

	FromSignature []byte
	ToSignature   []byte
}

// signingBytes 返回确定性的规范编码，长度前缀防止字段拼接歧义。
func (t Ticket) signingBytes() []byte {
	var buf []byte
	put := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		buf = append(buf, n[:]...)
		buf = append(buf, b...)
	}
	putStr := func(s string) { put([]byte(s)) }
	var num [8]byte

	putStr(t.ObjectID)
	putStr(t.FromTenant)
	putStr(t.ToTenant)
	binary.BigEndian.PutUint64(num[:], uint64(t.Version))
	put(num[:])
	binary.BigEndian.PutUint64(num[:], uint64(t.Generation))
	put(num[:])
	put(t.Nonce)
	binary.BigEndian.PutUint64(num[:], uint64(t.ExpiresAt.UnixNano()))
	put(num[:])
	return buf
}

// NewTicket 构造一份尚未签名的票据，nonce 必须由调用方随机生成且全局唯一。
func NewTicket(objectID, fromTenant, toTenant string, version, generation int64, nonce []byte, expiresAt time.Time) Ticket {
	return Ticket{
		ObjectID:   objectID,
		FromTenant: fromTenant,
		ToTenant:   toTenant,
		Version:    version,
		Generation: generation,
		Nonce:      append([]byte(nil), nonce...),
		ExpiresAt:  expiresAt,
	}
}

// SignSource 由源租户对票据签名。
func (t *Ticket) SignSource(priv ed25519.PrivateKey) {
	t.FromSignature = append([]byte(nil), ed25519.Sign(priv, t.signingBytes())...)
}

// SignTarget 由目标租户对同一份规范字节流签名，形成目标绑定。
func (t *Ticket) SignTarget(priv ed25519.PrivateKey) {
	t.ToSignature = append([]byte(nil), ed25519.Sign(priv, t.signingBytes())...)
}

// Verify 只做密码学核验：双方签名都必须对同一份规范字节流有效。
// 租户登记、版本/代次、时效与重放由宿主在移交时额外核对。
func (t Ticket) Verify(fromPub, toPub ed25519.PublicKey) bool {
	if len(t.FromSignature) == 0 || len(t.ToSignature) == 0 {
		return false
	}
	msg := t.signingBytes()
	return ed25519.Verify(fromPub, msg, t.FromSignature) &&
		ed25519.Verify(toPub, msg, t.ToSignature)
}
