package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// AuthLevel 标识客户端认证强度，数值越大要求越高。
type AuthLevel uint8

const (
	// AuthAnonymous 表示无需客户端认证。
	AuthAnonymous AuthLevel = iota
	// AuthCertificate 表示需要客户端证书认证。
	AuthCertificate
	// AuthMutual 表示需要双向认证（例如证书加固定凭据）。
	AuthMutual
)

// String 返回认证等级的可读名称。
func (l AuthLevel) String() string {
	switch l {
	case AuthAnonymous:
		return "anonymous"
	case AuthCertificate:
		return "certificate"
	case AuthMutual:
		return "mutual"
	default:
		return "unknown"
	}
}

// ProtocolOptions 描述签发票据时协商出的协议选项。
type ProtocolOptions struct {
	// MinVersion 是当前策略允许的最低 TLS 版本（如 tls.VersionTLS13）。
	MinVersion uint16
	// MaxVersion 是当前策略允许的最高 TLS 版本。
	MaxVersion uint16
	// ALPN 是允许的应用层协议列表，顺序不影响摘要。
	ALPN []string
}

// Policy 是宿主在某个时刻生效的安全配置。
type Policy struct {
	// Tenant 是策略所属租户标识，空租户非法。
	Tenant string
	// MinAuthLevel 是当前要求的最低客户端认证等级。
	MinAuthLevel AuthLevel
	// Options 是当前生效的协议选项。
	Options ProtocolOptions
	// TicketTTL 是票据有效期，必须大于零。
	TicketTTL uint64
}

// Allows 报告当前策略是否允许以给定认证等级和协议选项恢复会话。
//
// 恢复票据时必须先调用本方法：仅验证 AEAD 密文不足以保证票据符合
// 服务配置收紧后的新要求。
func (p Policy) Allows(level AuthLevel, options ProtocolOptions) bool {
	if level < p.MinAuthLevel {
		return false
	}
	if options.MinVersion < p.Options.MinVersion {
		return false
	}
	if options.MaxVersion > p.Options.MaxVersion {
		return false
	}
	for _, proto := range options.ALPN {
		if !containsString(p.Options.ALPN, proto) {
			return false
		}
	}
	return true
}

// Digest 返回策略的确定性摘要（SHA-256）。
//
// 摘要对切片做规范化排序，因此与元素顺序无关；任何配置字段变化都会
// 产生不同摘要，恢复时可据此识别配置变更。
func (p Policy) Digest() [32]byte {
	alpn := append([]string(nil), p.Options.ALPN...)
	sort.Strings(alpn)

	h := sha256.New()
	writeCanonicalString(h, "tenant", p.Tenant)
	writeCanonicalUint64(h, "min_auth_level", uint64(p.MinAuthLevel))
	writeCanonicalUint64(h, "min_version", uint64(p.Options.MinVersion))
	writeCanonicalUint64(h, "max_version", uint64(p.Options.MaxVersion))
	writeCanonicalUint64(h, "ticket_ttl", p.TicketTTL)
	writeCanonicalString(h, "alpn_count", uintToDecimal(uint64(len(alpn))))
	for _, proto := range alpn {
		writeCanonicalString(h, "alpn", proto)
	}

	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func writeCanonicalString(h interface{ Write([]byte) (int, error) }, key, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(key)))
	h.Write(length[:])
	h.Write([]byte(key))
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	h.Write(length[:])
	h.Write([]byte(value))
}

func writeCanonicalUint64(h interface{ Write([]byte) (int, error) }, key string, value uint64) {
	writeCanonicalString(h, key, uintToDecimal(value))
}

func uintToDecimal(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[pos:])
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
