// Package evidence 是一个可导入 Go 宿主的增量来源证据组件。
//
// 它解决的问题：构建门禁持有的“最优来源证书”（一个提交全部分解袋的受签
// 根）在一批构建器/信任域被撤销后必须更新，但不能每次都从全图枚举所有
// 签名组合。组件只接受带签名的节点补丁和信任域补丁，先证明补丁确实把旧
// 根确定性地变成新根，再只重算受影响的分解袋及其必要祖先，其余袋凭与新
// 约束边界完全相同的边界指纹复用。输出新根、新袋证书以及从旧证书到新证书
// 的最小披露差分；持有旧根与注册表的任何第三方都可以独立重验真实来源、
// 登记失效与验证成本。
//
// 关键不变量：
//   - 对象身份包含部署环境（NodeID = Env + 摘要），不同环境下摘要相同的
//     对象是两个不同的节点，任何边界或复用判断都不可互换。
//   - 袋的可复用性由边界指纹判定，指纹纳入环境身份、完整见证身份与注册表
//     序列号；同名变量（域名）换身份（域 ID）后指纹必然不同。
//   - 所有重验成本以固定权重从实际验证尝试数导出，差分中同时披露账本，
//     独立重验方按同一权重重算而不是直接相信声明值。
package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// EnvID 是部署环境标识。同一 PayloadDigest 在不同 EnvID 下是不同节点。
type EnvID string

// Digest 是内容摘要（SHA-256）。零值表示无摘要。
type Digest [sha256.Size]byte

// DomainID 是信任域的不可复用身份标识。域可以改名、合并或分裂，但
// DomainID 一经登记不再复用。
type DomainID string

// BuilderID 是构建器在信任域内的身份标识。
type BuilderID string

// BagID 是分解袋标识。
type BagID string

// HexDigest 解析十六进制摘要。
func HexDigest(s string) (Digest, error) {
	var d Digest
	raw, err := hex.DecodeString(s)
	if err != nil {
		return d, err
	}
	if len(raw) != sha256.Size {
		return d, ErrBadDigest
	}
	copy(d[:], raw)
	return d, nil
}

func (d Digest) Hex() string { return hex.EncodeToString(d[:]) }

// NodeID 是全局节点身份：部署环境 + 节点负载摘要。
// 两个环境里摘要相同的对象 NodeID 不同，证据全流程不允许互换。
type NodeID struct {
	Env     EnvID  `json:"env"`
	Payload Digest `json:"payload"`
}

func (n NodeID) String() string { return string(n.Env) + ":" + n.Payload.Hex() }

// Witness 是一条来源见证：某个构建器在某个信任域内对某节点的签名。
type Witness struct {
	Domain  DomainID  `json:"domain"`
	Builder BuilderID `json:"builder"`
	Node    NodeID    `json:"node"`
	PubKey  []byte    `json:"pubkey"` // 构建器 Ed25519 公钥
	Sig     []byte    `json:"sig"`    // 对 witnessStatement 的签名
}

// NodeState 是一个节点在某一代状态下的完整事实记录。
// Deps 按内容身份指向依赖；Witnesses 是当前登记的全部见证，每条见证的
// 有效性需结合注册表判定，组件不做全量组合枚举。
type NodeState struct {
	ID        NodeID    `json:"id"`
	Bag       BagID     `json:"bag"`
	Deps      []NodeID  `json:"deps"`
	Witnesses []Witness `json:"witnesses"`
}

// SourceClaim 是袋证书中为一个节点选定的最优来源。
type SourceClaim struct {
	Node      NodeID     `json:"node"`
	Chosen    *Witness   `json:"chosen,omitempty"`
	Failures  []Failure  `json:"failures,omitempty"` // 登记失效的见证及原因
	Unsourced bool       `json:"unsourced,omitempty"`
	Cost      CostLedger `json:"cost"`
}

// Failure 是一条登记失效记录：独立重验方可凭注册表撤销表复核。
type Failure struct {
	Domain  DomainID  `json:"domain"`
	Builder BuilderID `json:"builder"`
	Reason  string    `json:"reason"`
}

// CostLedger 是来源验证成本账本，所有字段都是可重算的物理计数。
type CostLedger struct {
	SigChecks int `json:"sig_checks"` // Ed25519 验签尝试次数（含失败尝试）
	HashOps   int `json:"hash_ops"`   // 编码哈希次数
	Weighted  int `json:"weighted"`   // 按固定权重导出的加权成本
}

// 固定且导出的成本权重。成本必须从计数导出，禁止直接声明总额。
const (
	WeightSignatureCheck = 100
	WeightHashOp         = 1
)

// CostWeights 返回成本权重，独立重验方使用同一份权重。
func CostWeights() (sigCheck, hashOp int) { return WeightSignatureCheck, WeightHashOp }

// BagBody 是分解袋的证书主体，经哈希后进入根的 Merkle 提交。
type BagBody struct {
	Bag         BagID         `json:"bag"`
	Nodes       []NodeState   `json:"nodes"`
	Sources     []SourceClaim `json:"sources"`
	RegistrySeq uint64        `json:"registry_seq"` // 本袋证据对应的注册表代数
}

// BagCert 是袋证书：主体及其摘要。袋本身不另设签名，其真实性由受签根的
// Merkle 提交锚定（见 RootBody.MerkleRoot）。
type BagCert struct {
	Body     BagBody `json:"body"`
	BodyHash Digest  `json:"body_hash"`
}

// BagHashEntry 是根中对单个袋的提交项。
type BagHashEntry struct {
	Bag  BagID  `json:"bag"`
	Hash Digest `json:"hash"`
}

// RootBody 是受签根的主体。
type RootBody struct {
	Epoch        uint64         `json:"epoch"`
	Bags         []BagHashEntry `json:"bags"` // 按 BagID 排序后构造 Merkle
	MerkleRoot   Digest         `json:"merkle_root"`
	RegistryHash Digest         `json:"registry_hash"`
	RegistrySeq  uint64         `json:"registry_seq"`
	PatchSeq     uint64         `json:"patch_seq"` // 已应用补丁信封最高序号
}

// SignedRoot 是根主体加上根权威的 Ed25519 签名。
type SignedRoot struct {
	Body RootBody `json:"body"`
	Sig  []byte   `json:"sig"`
}

// canonicalJSON 返回确定性 JSON：参与哈希的结构只使用定长字段、有序切片
// 与非 map 类型，并再经一次规范化，消除任何键序不确定性。
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return json.Marshal(canonicalize(generic))
}

// hashCanon 计算某结构的规范摘要。
func hashCanon(v any) (Digest, error) {
	raw, err := canonicalJSON(v)
	if err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(raw), nil
}

// canonicalize 递归把 map 换成有序键结构，保证跨实现编码一致。
func canonicalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([][2]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, [2]any{k, canonicalize(t[k])})
		}
		return orderedMap{Type: "map", Items: out}
	case []any:
		for i := range t {
			t[i] = canonicalize(t[i])
		}
		return t
	default:
		return v
	}
}

type orderedMap struct {
	Type  string   `json:"__canon_map__"`
	Items [][2]any `json:"items"`
}
