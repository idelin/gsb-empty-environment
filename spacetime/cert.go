package spacetime

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// genesisPrevHash 证书链首证书的前驱哈希。
const genesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Certificate 单段相交时段的地点证书。
//
// 不可抵赖性由三层机制保证:
//  1. 证书绑定双方轨迹的内容哈希(TrajHashA/B),原始证据不可事后替换;
//  2. 证书经 PrevHash 串联成哈希链,任何篡改都会破坏后续所有证书;
//  3. 证明方使用 Ed25519 私钥对证书哈希签名,公钥可公开验证。
type Certificate struct {
	Seq        int     // 链内序号(从 0 开始)
	EntityA    string  // 实体 A 标识
	EntityB    string  // 实体 B 标识
	TrajHashA  string  // A 轨迹 SHA-256(hex)
	TrajHashB  string  // B 轨迹 SHA-256(hex)
	Start      float64 // 相交开始时刻(Unix 秒)
	End        float64 // 相交结束时刻(Unix 秒)
	ThresholdM float64 // 距离阈值(米)
	MinDistM   float64 // 时段内最近距离(米)
	MinDistT   float64 // 最近距离时刻(Unix 秒)
	Location   Vec3    // 最近时刻双方位置中点
	Place      string  // 语义地点(柜台级)
	PrevHash   string  // 前序证书哈希
	Hash       string  // 本证书内容哈希
	Signature  string  // 对 Hash 的 Ed25519 签名(hex)
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

// canonical 证书的规范序列化,作为哈希输入。
func (c *Certificate) canonical() string {
	return strings.Join([]string{
		strconv.Itoa(c.Seq),
		c.EntityA, c.EntityB,
		c.TrajHashA, c.TrajHashB,
		ff(c.Start), ff(c.End),
		ff(c.ThresholdM), ff(c.MinDistM), ff(c.MinDistT),
		ff(c.Location.X), ff(c.Location.Y), ff(c.Location.Z),
		c.Place, c.PrevHash,
	}, "|")
}

func (c *Certificate) computeHash() [32]byte {
	return sha256.Sum256([]byte(c.canonical()))
}

// ProofBundle 一次相交证明的完整产物:证书链 + 验证公钥 + 捆扎哈希。
type ProofBundle struct {
	EntityA    string
	EntityB    string
	ThresholdM float64
	Certs      []*Certificate
	PubKey     string // 证明方 Ed25519 公钥(hex)
	BundleHash string // 全部证书哈希串联后的 SHA-256(hex)
}

// Verify 独立验证证明包:哈希链完整性、每份证书签名、捆扎哈希。
// 任何一环被篡改都会返回 false。
func (b *ProofBundle) Verify() bool {
	pub, err := hex.DecodeString(b.PubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	prev := genesisPrevHash
	chain := sha256.New()
	for _, c := range b.Certs {
		if c.PrevHash != prev {
			return false
		}
		sum := c.computeHash()
		if hex.EncodeToString(sum[:]) != c.Hash {
			return false
		}
		sig, err := hex.DecodeString(c.Signature)
		if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), sum[:], sig) {
			return false
		}
		chain.Write(sum[:])
		prev = c.Hash
	}
	return hex.EncodeToString(chain.Sum(nil)) == b.BundleHash
}

// Prover 相交证明器:基于倒排索引求交,并签发证书链。
type Prover struct {
	Idx    *GridIndex
	Places *PlaceRegistry
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
}

// NewProver 以确定性种子创建证明器(seed 32 字节;nil 则随机)。
func NewProver(idx *GridIndex, places *PlaceRegistry, seed []byte) *Prover {
	var pub ed25519.PublicKey
	var priv ed25519.PrivateKey
	if len(seed) == ed25519.SeedSize {
		priv = ed25519.NewKeyFromSeed(seed)
		pub = priv.Public().(ed25519.PublicKey)
	} else {
		pub, priv, _ = ed25519.GenerateKey(nil)
	}
	return &Prover{Idx: idx, Places: places, priv: priv, pub: pub}
}

// PubKeyHex 返回证明方公钥(hex),供验证方离线核验。
func (p *Prover) PubKeyHex() string { return hex.EncodeToString(p.pub) }

// Prove 对索引内编号 ai、bi 的两个实体执行时空相交证明。
func (p *Prover) Prove(ai, bi int, radius float64) *ProofBundle {
	ta, tb := p.Idx.Traj(ai), p.Idx.Traj(bi)
	ha, hb := ta.Hash(), tb.Hash()
	intervals := p.Idx.Contacts(ai, bi, radius)

	bundle := &ProofBundle{
		EntityA:    ta.EntityID,
		EntityB:    tb.EntityID,
		ThresholdM: radius,
		PubKey:     p.PubKeyHex(),
	}
	prev := genesisPrevHash
	chain := sha256.New()
	for i, iv := range intervals {
		mid := ta.PosAt(iv.MinDistT).Mid(tb.PosAt(iv.MinDistT))
		cert := &Certificate{
			Seq:        i,
			EntityA:    ta.EntityID,
			EntityB:    tb.EntityID,
			TrajHashA:  hex.EncodeToString(ha[:]),
			TrajHashB:  hex.EncodeToString(hb[:]),
			Start:      iv.Start,
			End:        iv.End,
			ThresholdM: radius,
			MinDistM:   iv.MinDist,
			MinDistT:   iv.MinDistT,
			Location:   mid,
			Place:      p.Places.Locate(mid),
			PrevHash:   prev,
		}
		sum := cert.computeHash()
		cert.Hash = hex.EncodeToString(sum[:])
		cert.Signature = hex.EncodeToString(ed25519.Sign(p.priv, sum[:]))
		chain.Write(sum[:])
		prev = cert.Hash
		bundle.Certs = append(bundle.Certs, cert)
	}
	bundle.BundleHash = hex.EncodeToString(chain.Sum(nil))
	return bundle
}
