// Package admission 实现机密计算消息宿主的工作负载请求准入控制。
//
// 它围绕四类事实工作：
//   - 已认证启动度量与根声明（信任锚）；
//   - 静态工作负载声明树（可创建的子节点、卷标识、委托层数）；
//   - 动态实例树（实际实例随机数 + 恢复代次）；
//   - 父实例签出、宿主背书的票据（绑定父子声明摘要、实例随机数、恢复代次与用途）。
//
// 所有密码学对象仅依赖标准库：摘要使用 SHA-256，签名使用 Ed25519，
// 规范编码使用固定键序的 encoding/json。
package admission

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Digest 是对规范 JSON 字节求 SHA-256 得到的内容摘要。
type Digest [sha256.Size]byte

// canonicalJSON 返回确定性 JSON 字节：不允许 HTML 转义、键按字典序排列。
// 签名与摘要必须基于同一规范编码，否则跨进程重建链时无法复现。
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode 追加一个换行，去掉它得到稳定的字节序列。
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SumDigest 计算任意可编码对象的规范摘要。
func SumDigest(v any) (Digest, error) {
	b, err := canonicalJSON(v)
	if err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(b), nil
}

// String 返回小写十六进制表示。
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// MarshalText 让 Digest 在 JSON 中以十六进制字符串稳定出现。
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText 解析十六进制摘要。
func (d *Digest) UnmarshalText(text []byte) error {
	raw, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	if len(raw) != sha256.Size {
		return ErrDigestLength
	}
	copy(d[:], raw)
	return nil
}

// Nonce 是实例创建时生成的实际实例随机数。
type Nonce [32]byte

// NewNonce 生成密码学随机实例随机数。
func NewNonce() (Nonce, error) {
	var n Nonce
	if _, err := rand.Read(n[:]); err != nil {
		return Nonce{}, err
	}
	return n, nil
}

// MarshalText 以十六进制字符串编码随机数。
func (n Nonce) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(n[:])), nil
}

// UnmarshalText 解析十六进制随机数。
func (n *Nonce) UnmarshalText(text []byte) error {
	raw, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	if len(raw) != len(n) {
		return ErrNonceLength
	}
	copy(n[:], raw)
	return nil
}

// VolumeID 标识宿主可读写的卷。
type VolumeID string

// VolumePerm 描述对单个卷的读写权限。
type VolumePerm struct {
	Volume VolumeID `json:"volume"`
	Read   bool     `json:"read"`
	Write  bool     `json:"write"`
}

// Allows 报告 p 是否完整覆盖 want（只可转授自己拥有的权限）。
func (p VolumePerm) Allows(want VolumePerm) bool {
	if p.Volume != want.Volume {
		return false
	}
	if want.Read && !p.Read {
		return false
	}
	if want.Write && !p.Write {
		return false
	}
	return true
}

// Purpose 是票据的显式用途，防止票据被跨场景重放。
type Purpose string

const (
	// PurposeSpawn 授权在实例树上创建一个子工作负载实例。
	PurposeSpawn Purpose = "spawn"
)

// Claim 是静态工作负载声明树中的一个节点声明。
type Claim struct {
	// ID 是声明在树内的稳定标识。
	ID string `json:"id"`
	// AllowedChildren 列出该节点允许创建的子声明 ID。
	AllowedChildren []string `json:"allowed_children"`
	// Volumes 是该声明可读写的卷集合。
	Volumes []VolumePerm `json:"volumes"`
	// MaxDelegationDepth 是允许的委托层数：0 表示不能再向下委托。
	MaxDelegationDepth int `json:"max_delegation_depth"`
}

// Digest 计算声明内容摘要，票据绑定的就是该摘要而非可变的节点名称。
func (c Claim) Digest() (Digest, error) { return SumDigest(c) }

// ClaimTree 是由宿主度量背书的静态工作负载声明树。
type ClaimTree struct {
	Root   string           `json:"root"`
	Claims map[string]Claim `json:"claims"`
}

// Policy 携带策略版本与其声明树。策略版本随票据绑定，旧策略票据在新策略下失效。
type Policy struct {
	Version uint64    `json:"version"`
	Tree    ClaimTree `json:"tree"`
}

// Validate 检查声明树结构：根存在、子节点存在、引用收敛到同一棵树。
func (p Policy) Validate() error {
	if p.Version == 0 {
		return ErrPolicyVersion
	}
	tree := p.Tree
	root, ok := tree.Claims[tree.Root]
	if !ok {
		return ErrRootClaimMissing
	}
	visited := map[string]bool{root.ID: true}
	queue := []string{root.ID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		node := tree.Claims[id]
		seenChild := map[string]bool{}
		for _, childID := range node.AllowedChildren {
			if seenChild[childID] {
				return ErrDuplicateChild
			}
			seenChild[childID] = true
			child, ok := tree.Claims[childID]
			if !ok {
				return ErrUnknownClaim
			}
			if !visited[child.ID] {
				visited[child.ID] = true
				queue = append(queue, child.ID)
			}
		}
	}
	// 存在从根不可达的孤立声明即视为多棵树，拒绝。
	if len(visited) != len(tree.Claims) {
		return ErrOrphanClaim
	}
	return nil
}

// ClaimDigest 返回某声明 ID 的内容摘要。
func (p Policy) ClaimDigest(id string) (Digest, error) {
	claim, ok := p.Tree.Claims[id]
	if !ok {
		return Digest{}, ErrUnknownClaim
	}
	return claim.Digest()
}

// BootMeasurement 是宿主启动时的已认证度量输入。
type BootMeasurement struct {
	// Measurement 是由可信硬件/虚拟化层给出的度量值（不透明）。
	Measurement Digest `json:"measurement"`
	// PolicyVersion 与 RootClaimDigest 将度量绑定到具体策略与根声明。
	PolicyVersion   uint64 `json:"policy_version"`
	RootClaimDigest Digest `json:"root_claim_digest"`
}

// RootEvidenceBody 是信任锚的可签名内容。
type RootEvidenceBody struct {
	Boot              BootMeasurement `json:"boot"`
	Policy            Policy          `json:"policy"`
	RootNonce         Nonce           `json:"root_nonce"`
	RootName          string          `json:"root_name"`
	InitialGeneration uint64          `json:"initial_generation"`
}

// RootEvidence 是宿主对启动度量、策略和根实例的签名背书。
type RootEvidence struct {
	Body      RootEvidenceBody `json:"body"`
	Signature []byte           `json:"signature"`
}

// Sign 用宿主证明密钥对信任锚内容签名。
func (e *RootEvidence) Sign(priv ed25519.PrivateKey) error {
	b, err := canonicalJSON(e.Body)
	if err != nil {
		return err
	}
	e.Signature = ed25519.Sign(priv, b)
	return nil
}

// Verify 用宿主证明公钥校验信任锚签名。
func (e RootEvidence) Verify(pub ed25519.PublicKey) error {
	b, err := canonicalJSON(e.Body)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, b, e.Signature) {
		return ErrBadSignature
	}
	return nil
}

// TicketBody 是票据的可签名内容。
//
// 票据同时绑定：父声明摘要、子声明摘要、父子实例随机数、恢复代次与用途，
// 因此旧代次实例（即使同名重建）无法复用旧票据。
type TicketBody struct {
	ParentClaimDigest Digest       `json:"parent_claim_digest"`
	ChildClaimDigest  Digest       `json:"child_claim_digest"`
	ParentNonce       Nonce        `json:"parent_nonce"`
	ChildNonce        Nonce        `json:"child_nonce"`
	Generation        uint64       `json:"generation"`
	PolicyVersion     uint64       `json:"policy_version"`
	Purpose           Purpose      `json:"purpose"`
	Volumes           []VolumePerm `json:"volumes"`
	RemainingDepth    int          `json:"remaining_depth"`
}

// Ticket 是父实例经宿主签出的委托票据。
type Ticket struct {
	Body      TicketBody `json:"body"`
	Signature []byte     `json:"signature"`
}

// Digest 是票据的全局唯一标识，消费记录与快照校验都以它为准。
func (t Ticket) Digest() (Digest, error) { return SumDigest(t) }

// Sign 用宿主证明密钥对票据内容签名（宿主代表父实例背书）。
func (t *Ticket) Sign(priv ed25519.PrivateKey) error {
	b, err := canonicalJSON(t.Body)
	if err != nil {
		return err
	}
	t.Signature = ed25519.Sign(priv, b)
	return nil
}

// Verify 校验票据签名。
func (t Ticket) Verify(pub ed25519.PublicKey) error {
	b, err := canonicalJSON(t.Body)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, b, t.Signature) {
		return ErrBadSignature
	}
	return nil
}

// Instance 是已发布的工作负载实例。
type Instance struct {
	Name        string       `json:"name"`
	Nonce       Nonce        `json:"nonce"`
	ClaimID     string       `json:"claim_id"`
	ClaimDigest Digest       `json:"claim_digest"`
	Generation  uint64       `json:"generation"`
	Parent      Nonce        `json:"parent_nonce"`
	Volumes     []VolumePerm `json:"volumes"`
	Depth       int          `json:"depth"`
}

// SpawnRequest 是批请求中的一个创建项，引用一张签出票据。
type SpawnRequest struct {
	Name   string `json:"name"`
	Ticket Ticket `json:"ticket"`
}

// Batch 是一次原子提交的全部创建请求。
type Batch struct {
	PolicyVersion uint64         `json:"policy_version"`
	Generation    uint64         `json:"generation"`
	Requests      []SpawnRequest `json:"requests"`
}

// Digest 计算批次摘要，幂等重放以该摘要取回原回执。
func (b Batch) Digest() (Digest, error) { return SumDigest(b) }

// ReceiptBody 是提交回执的可签名内容，并内嵌批次与票据以便独立重建授权链。
type ReceiptBody struct {
	BatchDigest Digest     `json:"batch_digest"`
	Generation  uint64     `json:"generation"`
	Batch       Batch      `json:"batch"`
	Instances   []Instance `json:"instances"`
	Tickets     []Ticket   `json:"tickets"`
}

// Receipt 是宿主对一次成功批提交的签名收据。
type Receipt struct {
	Body      ReceiptBody `json:"body"`
	Signature []byte      `json:"signature"`
}

// Sign 用宿主证明密钥对回执签名。
func (r *Receipt) Sign(priv ed25519.PrivateKey) error {
	b, err := canonicalJSON(r.Body)
	if err != nil {
		return err
	}
	r.Signature = ed25519.Sign(priv, b)
	return nil
}

// Verify 校验回执签名。
func (r Receipt) Verify(pub ed25519.PublicKey) error {
	b, err := canonicalJSON(r.Body)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, b, r.Signature) {
		return ErrBadSignature
	}
	return nil
}

// Snapshot 是可被独立校验函数消费的宿主发布快照。
type Snapshot struct {
	Root      RootEvidence `json:"root"`
	Policy    Policy       `json:"policy"`
	Instances []Instance   `json:"instances"`
	Receipts  []Receipt    `json:"receipts"`
}
