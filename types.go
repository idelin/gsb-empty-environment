package netsec

import (
	"crypto"
	"strings"
)

// DNSSEC 算法号（RFC 8624 / RFC 9701 等），本包实现其中可由 Go
// 标准库直接校验的算法；其余算法在密码学校验时显式报不支持。
const (
	AlgRSASHA1   uint8 = 5  // RFC 3110
	AlgRSASHA256 uint8 = 8  // RFC 5702
	AlgRSASHA512 uint8 = 10 // RFC 5702
	AlgECDSAP256 uint8 = 13 // RFC 6605
	AlgECDSAP384 uint8 = 14 // RFC 6605
	AlgEd25519   uint8 = 15 // RFC 8080
	AlgEd448     uint8 = 16 // RFC 9374
)

// DS 摘要算法号（RFC 4509 / RFC 6605）。
const (
	DigestSHA1   uint8 = 1
	DigestSHA256 uint8 = 2
	DigestSHA384 uint8 = 4
)

// DNS RR 类型号。
const (
	TypeDNSKEY  uint16 = 48
	TypeCDS     uint16 = 59
	TypeCDNSKEY uint16 = 60
	TypeRRSIG   uint16 = 46
)

const classIN uint16 = 1

// DSRecord 是 DS/CDS 的 RDATA（RFC 4034 §5）。
type DSRecord struct {
	KeyTag     uint16
	Algorithm  uint8
	DigestType uint8
	Digest     []byte
}

// DNSKEYRecord 是 DNSKEY/CDNSKEY 的 RDATA（RFC 4034 §2）。
type DNSKEYRecord struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	PublicKey []byte
}

// IsKSK 报告该 DNSKEY 是否置 SEP 位（bit 15），即承担 KSK 角色。
func (k DNSKEYRecord) IsKSK() bool { return k.Flags&0x0001 != 0 }

// RRSIGRecord 是 RRSIG RDATA（RFC 4034 §3）。
type RRSIGRecord struct {
	TypeCovered uint16
	Algorithm   uint8
	Labels      uint8
	OrigTTL     uint32
	Expiration  uint32
	Inception   uint32
	KeyTag      uint16
	SignerName  string
	Signature   []byte
}

// RRset 是同一属主、同一类型的资源记录集及其签名。
type RRset struct {
	Name    string
	Type    uint16
	Records []DNSRecord
	Sig     *RRSIGRecord
}

// DNSRecord 是已解析 RDATA 的联合类型：DSRecord 或 DNSKEYRecord。
type DNSRecord interface {
	rdata() []byte
}

func (d DSRecord) rdata() []byte     { return dsRDATA(d) }
func (k DNSKEYRecord) rdata() []byte { return dnskeyRDATA(k) }

// Signer 汇总一把已发布 DNSKEY 及其对应的密码学公钥。
type Signer struct {
	Key       DNSKEYRecord
	PublicKey crypto.PublicKey
}

// canonicalName 返回小写、以根标签结尾的域名线格式（RFC 4034 §6.2）。
func canonicalName(name string) []byte {
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	if n == "" {
		return []byte{0}
	}
	var b strings.Builder
	for _, label := range strings.Split(n, ".") {
		b.WriteByte(byte(len(label)))
		b.WriteString(label)
	}
	b.WriteByte(0)
	return []byte(b.String())
}

func dsRDATA(d DSRecord) []byte {
	b := make([]byte, 0, 4+len(d.Digest))
	b = append(b, byte(d.KeyTag>>8), byte(d.KeyTag))
	b = append(b, d.Algorithm, d.DigestType)
	b = append(b, d.Digest...)
	return b
}

func dnskeyRDATA(k DNSKEYRecord) []byte {
	b := make([]byte, 0, 4+len(k.PublicKey))
	b = append(b, byte(k.Flags>>8), byte(k.Flags))
	b = append(b, k.Protocol, k.Algorithm)
	b = append(b, k.PublicKey...)
	return b
}

// KeyTag 按 RFC 4034 附录 B 计算 DNSKEY 的 key tag。
func KeyTag(k DNSKEYRecord) uint16 {
	wire := dnskeyRDATA(k)
	var ac uint32
	for i, v := range wire {
		if i%2 == 0 {
			ac += uint32(v) << 8
		} else {
			ac += uint32(v)
		}
	}
	ac += ac >> 16
	return uint16(ac)
}

// DSDigest 按 RFC 4509 §4 / RFC 4034 §5.1.4 计算 DS 摘要：
// digest(owner 名线格式 || DNSKEY RDATA)。
func DSDigest(owner string, k DNSKEYRecord, digestType uint8) ([]byte, error) {
	h, err := hashForDigest(digestType)
	if err != nil {
		return nil, err
	}
	h.Write(canonicalName(owner))
	h.Write(dnskeyRDATA(k))
	return h.Sum(nil), nil
}

// DSMatchesDNSKEY 报告给定 DS 是否恰好指向该 DNSKEY。
func DSMatchesDNSKEY(d DSRecord, owner string, k DNSKEYRecord) bool {
	if d.Algorithm != k.Algorithm || d.KeyTag != KeyTag(k) {
		return false
	}
	digest, err := DSDigest(owner, k, d.DigestType)
	if err != nil {
		return false
	}
	return bytesEqual(digest, d.Digest)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
