// Package recov 实现加密信箱的“历史会话恢复验收包”。
//
// 它解决的问题是：消息第一次离线投递时只保存了握手摘要，几个月后接收方
// 可能已更换设备、轮换身份钥或撤销部分一次性预密钥（OPK）。宿主在导入
// 历史数据时，只能在密码学上确认以下三点后，才能拿到恢复密钥：
//
//  1. 同一公钥承诺在该会话发起时（发送时检查点所指的日志位置）仍获授权；
//  2. 该承诺只被这一个会话摘要消费（无双花）；
//  3. 当目录出现冲突后缀（分叉）时，会话身份在所有可成立分支上都相同。
//
// 账户（永久）与设备实例（可重注册）分别标识；同名设备重新注册会得到新的
// 实例标识，绝不继承旧实例的身份钥或 OPK。
package recov

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// 各类记录哈希的域分隔前缀，避免不同记录类型之间被混用。
const (
	domainEvent       = "recov/event/v1:"
	domainOPKBlock    = "recov/opkblock/v1:"
	domainReceipt     = "recov/receipt/v1:"
	domainCheckpoint  = "recov/checkpoint/v1:"
	domainManifest    = "recov/manifest/v1:"
	leafPrefixMTH     = 0x00 // RFC 6962 叶子前缀
	domainSessionBind = "recov/session/v1:"
)

// 记录类型标签，用于清单与 JSON kind 字段。
const (
	KindSignedCheckpoint = "signed-checkpoint"
	KindEvent            = "directory-event"
	KindOPKBlock         = "opk-block"
	KindReceipt          = "receipt"
	KindSessionEnvelope  = "session-envelope"
)

// AccountID 是永久账户标识，生命周期跨越所有设备实例。
type AccountID string

// DeviceName 是账户命名空间内可重复使用的设备名（如“手机”）。
type DeviceName string

// InstanceID 是设备实例标识。同名设备注销后重新注册会产生新的实例标识，
// 旧实例的密钥与 OPK 不会被新实例继承。
type InstanceID string

// EventKind 枚举目录日志中的设备生命周期事件。
type EventKind string

const (
	EvRegister    EventKind = "register"     // 注册一个全新设备实例
	EvRemove      EventKind = "remove"       // 注销设备实例
	EvRotateIDKey EventKind = "rotate-idkey" // 轮换该实例的长期身份钥
	EvOPKBlock    EventKind = "opk-block"    // 挂载一棵 OPK 承诺树
	EvOPKRevoke   EventKind = "opk-revoke"   // 撤销尚未使用的 OPK 叶子
)

// Event 是目录日志中的一条不可变事件。Seq 是它在日志中的序号（从 0 起）。
// 目录服务方对检查点签名，而事件叶子哈希链入检查点所承诺的树根。
type Event struct {
	Seq         uint64     `json:"seq"`
	Kind        EventKind  `json:"kind"`
	Account     AccountID  `json:"account"`
	Device      DeviceName `json:"device"`
	Instance    InstanceID `json:"instance"`
	IdentityKey []byte     `json:"identity_key,omitempty"` // 注册/换钥时的 P-256 公钥（SEC1 压缩点）
	OPKRoot     []byte     `json:"opk_root,omitempty"`     // opk-block 事件承诺的树根
	OPKCount    uint32     `json:"opk_count,omitempty"`    // 该树叶子数量
	OPKIndex    uint32     `json:"opk_index,omitempty"`    // opk-revoke 针对的叶子序号
}

// OPKBlock 是某设备实例发布的一棵一次性公钥承诺树及其内容。
// 目录事件只承诺 Root；验收包需要完整 Block 才能验证叶子包含证明。
type OPKBlock struct {
	Account   AccountID  `json:"account"`
	Instance  InstanceID `json:"instance"`
	Root      []byte     `json:"root"`
	LeafCount uint32     `json:"leaf_count"`
	LeafKeys  [][]byte   `json:"leaf_keys"` // 每片叶子的一次性公钥（P-256 SEC1 压缩点）
}

// Checkpoint 是目录日志或回执日志在某一规模下的签名检查点（头部）。
// LogID 区分 "directory" 与 "receipts" 两条日志。
type Checkpoint struct {
	LogID    string `json:"log_id"`
	TreeSize uint64 `json:"tree_size"`
	TreeRoot []byte `json:"tree_root"`
}

// SignedCheckpoint 是带 ed25519 签名的检查点。签名覆盖规范编码的检查点。
type SignedCheckpoint struct {
	Checkpoint Checkpoint `json:"checkpoint"`
	SignerKey  []byte     `json:"signer_key"` // ed25519 公钥
	Signature  []byte     `json:"signature"`
}

// Receipt 是离线见证对“某会话摘要消费了某 OPK 承诺”的确认记录。
// 它进入见证维护的回执日志，其叶子哈希被检查点签名覆盖。
type Receipt struct {
	Seq        uint64     `json:"seq"`
	Account    AccountID  `json:"account"`
	Instance   InstanceID `json:"instance"`
	OPKIndex   uint32     `json:"opk_index"`
	Commitment []byte     `json:"commitment"` // 被消费的 OPK 叶子承诺
	SessionID  []byte     `json:"session_id"` // 握手摘要派生的会话标识
}

// MerkleProof 是 RFC 6962 风格的包含证明。
type MerkleProof struct {
	LeafIndex uint64   `json:"leaf_index"`
	TreeSize  uint64   `json:"tree_size"`
	Hashes    [][]byte `json:"hashes"`
}

// SessionEnvelope 是消息携带的会话恢复信封。
type SessionEnvelope struct {
	Account         AccountID        `json:"account"`
	Instance        InstanceID       `json:"instance"`
	OPKIndex        uint32           `json:"opk_index"`
	OPKCommitment   []byte           `json:"opk_commitment"`
	OPKInclusion    MerkleProof      `json:"opk_inclusion"`    // 承诺在该实例 OPK 树内的包含证明
	SendCheckpoint  SignedCheckpoint `json:"send_checkpoint"`  // 消息发送时的目录检查点
	EphemeralPub    []byte           `json:"ephemeral_pub"`    // 发送方临时 ECDH 公钥（SEC1 点）
	HandshakeDigest []byte           `json:"handshake_digest"` // 首次离线投递保存的握手摘要
	WrappedKey      []byte           `json:"wrapped_key"`      // 用 ECDH 派生密钥加密的恢复密钥
	Nonce           []byte           `json:"nonce"`            // AES-GCM nonce
}

// Manifest 是验收包的清单：声明会话、信任锚与全部记录哈希。
// 见证对清单签名，使整包自证完整、防篡改。
type Manifest struct {
	SessionID         []byte   `json:"session_id"`
	DirectoryVerifier []byte   `json:"directory_verifier"` // 信任的目录 ed25519 公钥
	WitnessVerifier   []byte   `json:"witness_verifier"`   // 信任的见证 ed25519 公钥
	RecordHashes      [][]byte `json:"record_hashes"`      // 全部记录的哈希集合（按字典序写入）
}

// AcceptancePackage 是可导入宿主的、可独立验证的最小记录子集。
type AcceptancePackage struct {
	Manifest   Manifest     `json:"manifest"`
	WitnessSig []byte       `json:"witness_signature"`
	Records    []JSONRecord `json:"records"`
}

// JSONRecord 是带类型标签的记录容器，便于整包序列化与独立重放。
type JSONRecord struct {
	Kind string          `json:"kind"`
	Raw  json.RawMessage `json:"raw"`
}

// 决策结果错误码：缺少包含证明与确有双重消费必须分开报告。
var (
	// ErrPending 表示数据尚未到齐（例如回执先到、目录前缀后到），不能下结论。
	ErrPending = errors.New("recov: evidence incomplete, decision pending")
	// ErrBadSignature 表示检查点、回执或清单的签名验证失败。
	ErrBadSignature = errors.New("recov: signature verification failed")
	// ErrMalformed 表示记录结构或序号不合法。
	ErrMalformed = errors.New("recov: malformed record")
	// ErrMissingInclusion 表示缺少必要的包含证明/前缀，无法证明授权或消费事实。
	ErrMissingInclusion = errors.New("recov: missing inclusion proof or log prefix")
	// ErrNotAuthorized 表示会话发起时该实例或 OPK 承诺未获授权（已注销/换钥/撤销）。
	ErrNotAuthorized = errors.New("recov: commitment not authorized at session time")
	// ErrDoubleConsumption 表示确有证据证明同一承诺被两个不同会话摘要消费。
	ErrDoubleConsumption = errors.New("recov: double consumption proven")
	// ErrForkAmbiguity 表示目录存在冲突后缀，且会话身份在可成立分支上不一致。
	ErrForkAmbiguity = errors.New("recov: conflicting branches yield different session identity")
	// ErrUnknownInstance 表示引用了当前状态下不存在的设备实例。
	ErrUnknownInstance = errors.New("recov: unknown device instance")
	// ErrReRegistration 表示试图让同名设备的新实例继承旧实例的钥。
	ErrReRegistration = errors.New("recov: re-registered device must not inherit old keys")
	// ErrUnwrap 表示恢复密钥解封失败（ECDH/AEAD 不匹配）。
	ErrUnwrap = errors.New("recov: recovery key unwrap failed")
)

// DecisionError 给哨兵错误附加上下文，宿主可用 errors.Is 归类。
type DecisionError struct {
	Err    error
	Detail string
}

func (e *DecisionError) Error() string {
	if e.Detail == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Err.Error(), e.Detail)
}

func (e *DecisionError) Unwrap() error { return e.Err }

func wrapErr(err error, format string, args ...any) error {
	return &DecisionError{Err: err, Detail: fmt.Sprintf(format, args...)}
}

// ---- 规范编码：全部字段按固定顺序做长度前缀编码，空字段也参与，保证确定性 ----

func putBytes(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

func putString(s string) []byte { return putBytes([]byte(s)) }

func putU64(n uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, n)
	return out
}

func putU32(n uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, n)
	return out
}

func enc(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func hashDomain(domain string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write(body)
	sum := h.Sum(nil)
	return sum[:]
}

// EventHash 计算目录事件叶子的“叶子数据”哈希（再由 MTH 加 0x00 前缀）。
func EventHash(e *Event) []byte {
	body := enc(
		putU64(e.Seq), putString(string(e.Kind)), putString(string(e.Account)),
		putString(string(e.Device)), putString(string(e.Instance)),
		putBytes(e.IdentityKey), putBytes(e.OPKRoot), putU32(e.OPKCount), putU32(e.OPKIndex),
	)
	return hashDomain(domainEvent, body)
}

// ReceiptHash 计算回执叶子的“叶子数据”哈希。
func ReceiptHash(r *Receipt) []byte {
	body := enc(
		putU64(r.Seq), putString(string(r.Account)), putString(string(r.Instance)),
		putU32(r.OPKIndex), putBytes(r.Commitment), putBytes(r.SessionID),
	)
	return hashDomain(domainReceipt, body)
}

// OPKBlockHash 计算一棵 OPK 承诺树内容的规范哈希（作为验收包记录标识）。
func OPKBlockHash(b *OPKBlock) []byte {
	parts := [][]byte{
		putString(string(b.Account)), putString(string(b.Instance)),
		putBytes(b.Root), putU32(b.LeafCount),
	}
	for _, k := range b.LeafKeys {
		parts = append(parts, putBytes(k))
	}
	return hashDomain(domainOPKBlock, enc(parts...))
}

// CheckpointSigningBytes 返回检查点的待签名规范字节。
func CheckpointSigningBytes(c *Checkpoint) []byte {
	return hashDomain(domainCheckpoint, enc(
		putString(c.LogID), putU64(c.TreeSize), putBytes(c.TreeRoot),
	))
}

// ManifestHash 计算清单的规范哈希（见证签名覆盖它）。
func ManifestHash(m *Manifest) []byte {
	parts := [][]byte{
		putBytes(m.SessionID), putBytes(m.DirectoryVerifier), putBytes(m.WitnessVerifier),
		putU64(uint64(len(m.RecordHashes))),
	}
	for _, h := range m.RecordHashes {
		parts = append(parts, putBytes(h))
	}
	return hashDomain(domainManifest, enc(parts...))
}

// DeriveSessionID 从握手摘要与会话参数派生稳定的会话标识。
func DeriveSessionID(handshakeDigest, commitment []byte, inst InstanceID, acct AccountID) []byte {
	body := enc(
		putBytes(handshakeDigest), putBytes(commitment),
		putString(string(inst)), putString(string(acct)),
	)
	return hashDomain(domainSessionBind, body)
}
