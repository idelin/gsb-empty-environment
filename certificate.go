package spacetime

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GenesisHash 是证书链的创世前驱哈希。
const GenesisHash = "spacetime-genesis"

// Certificate 是一次时空相交的地点证书。
//
// 不可抵赖性由三层机制保证:
//  1. DigestA/DigestB 把证书绑定到双方原始轨迹数据的 SHA-256
//     摘要,数据在签发前已由数据方承诺(commit),事后无法替换;
//  2. Hash 覆盖证书全部字段并链接前一张证书(PrevHash),
//     篡改任何一张证书都会使整条链校验失败;
//  3. Signature 是证明机构用私钥对 Hash 计算的 HMAC-SHA256,
//     没有私钥的一方无法伪造合法证书。
type Certificate struct {
	Seq       int     // 链内序号,从 1 开始
	EntityA   string  // 实体 A 标识
	EntityB   string  // 实体 B 标识
	Start     float64 // 相交开始时刻(秒)
	End       float64 // 相交结束时刻(秒)
	Threshold float64 // 判定阈值(米)
	Location  string  // 相遇地点(最近兴趣点)
	MidX      float64 // 相遇中心点 X(米)
	MidY      float64 // 相遇中心点 Y(米)
	DigestA   string  // 实体 A 轨迹数据摘要
	DigestB   string  // 实体 B 轨迹数据摘要
	PrevHash  string  // 前一张证书的哈希
	Hash      string  // 本证书内容的 SHA-256
	Signature string  // 证明机构对 Hash 的 HMAC-SHA256
}

// payload 是参与哈希计算的规范序列化。
func (c *Certificate) payload() string {
	return fmt.Sprintf("%d|%s|%s|%.6f|%.6f|%.6f|%s|%.6f|%.6f|%s|%s|%s",
		c.Seq, c.EntityA, c.EntityB, c.Start, c.End, c.Threshold,
		c.Location, c.MidX, c.MidY, c.DigestA, c.DigestB, c.PrevHash)
}

// computeHash 计算证书内容哈希。
func (c *Certificate) computeHash() string {
	sum := sha256.Sum256([]byte(c.payload()))
	return hex.EncodeToString(sum[:])
}

// Prover 是证明机构,持有签名私钥,负责签发与核验证书。
type Prover struct {
	key []byte
}

// NewProver 以给定私钥创建证明机构。
func NewProver(key []byte) *Prover {
	return &Prover{key: key}
}

// Issue 签发一张证书并链接到 prevHash。
func (p *Prover) Issue(seq int, a, b *Trajectory, iv Interval, threshold float64,
	location string, midX, midY float64, prevHash string) Certificate {
	cert := Certificate{
		Seq:       seq,
		EntityA:   a.EntityID,
		EntityB:   b.EntityID,
		Start:     iv.Start,
		End:       iv.End,
		Threshold: threshold,
		Location:  location,
		MidX:      midX,
		MidY:      midY,
		DigestA:   a.Digest(),
		DigestB:   b.Digest(),
		PrevHash:  prevHash,
	}
	cert.Hash = cert.computeHash()
	mac := hmac.New(sha256.New, p.key)
	mac.Write([]byte(cert.Hash))
	cert.Signature = hex.EncodeToString(mac.Sum(nil))
	return cert
}

// Verify 核验单张证书的内容哈希与签名。
func (p *Prover) Verify(cert Certificate) bool {
	if cert.Hash != cert.computeHash() {
		return false
	}
	mac := hmac.New(sha256.New, p.key)
	mac.Write([]byte(cert.Hash))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(cert.Signature))
}

// VerifyChain 核验一条证书链:链式前驱、内容哈希与签名全部合法。
func (p *Prover) VerifyChain(certs []Certificate) bool {
	prev := GenesisHash
	for i, cert := range certs {
		if cert.Seq != i+1 || cert.PrevHash != prev || !p.Verify(cert) {
			return false
		}
		prev = cert.Hash
	}
	return true
}
