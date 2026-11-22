// Package recovery 实现加密信箱的历史会话恢复验收包。
//
// 验收包只负责“验收与放行”：核对签过名的目录检查点、设备增删事件、
// 一次性公钥承诺树、发送时检查点与消费回执，只有当同一公钥承诺在会话
// 发起时仍获授权、且只被该会话摘要消费一次时，才通过回调把恢复密钥
// 交给消息宿主。解封与业务处理由宿主自行完成。
package recovery

// Hash 是各类默克尔树与承诺使用的 32 字节摘要。
type Hash []byte

// AccountID 是永久账户标识，生命周期独立于任何设备实例。
type AccountID string

// DeviceID 是设备实例标识。每次注册都产生全新的实例标识；
// 重新注册同名设备会得到新的 DeviceID，不得继承旧实例的密钥。
type DeviceID string

// DeviceName 是给人看的设备名，可在不同实例间复用，不参与授权判定。
type DeviceName string

// EventKind 描述设备目录事件类型。
type EventKind string

const (
	// EventAdd 注册一个新设备实例。
	EventAdd EventKind = "add"
	// EventRotate 轮换设备身份钥与一次性预密钥树（设备实例不变）。
	EventRotate EventKind = "rotate"
	// EventRemove 删除设备实例；删除后该实例不再获得任何授权。
	EventRemove EventKind = "remove"
)

// Event 是设备目录日志中的一条设备增删/换钥事件。
//
// Seq 是“该设备实例自己”的单调序号，从 0 开始；不同设备实例之间
// 允许出现相同序号。全局日志位置由事件在目录树中的下标唯一确定。
type Event struct {
	// GlobalIndex 是事件在目录日志中的全局位置，由目录签名链与包含证明锚定。
	GlobalIndex uint64     `json:"global_index"`
	Seq         uint64     `json:"seq"`
	Account     AccountID  `json:"account"`
	Device      DeviceID   `json:"device"`
	Name        DeviceName `json:"name"`
	Kind        EventKind  `json:"kind"`
	IdentityPK  []byte     `json:"identity_pk"`
	PrekeyRoot  Hash       `json:"prekey_root"`
	Epoch       uint64     `json:"epoch"`
}

// Checkpoint 是目录日志在某一 TreeSize 下的默克尔树根。
type Checkpoint struct {
	TreeSize uint64 `json:"tree_size"`
	RootHash Hash   `json:"root_hash"`
	// Prev 是上一个签名检查点的规范化摘要；size=0 的创世检查点 Prev 为空。
	// 同一 TreeSize 出现两个内容不同的签名检查点，即构成冲突后缀（分叉）。
	Prev Hash `json:"prev"`
	// DeviceTail 记录每个“账户|设备实例”在当前 TreeSize 下最后一条事件的
	// 全局位置。它由目录签名背书，验证方无需导入无关设备事件即可判定
	// 目标设备事件链是否完整。
	DeviceTail map[string]uint64 `json:"device_tail,omitempty"`
}

// DeviceTailKey 是 Checkpoint.DeviceTail 映射的规范化键。
func DeviceTailKey(account AccountID, device DeviceID) string {
	return string(account) + "\x00" + string(device)
}

// SignedCheckpoint 是目录身份钥签名后的检查点。
type SignedCheckpoint struct {
	Checkpoint
	Signature []byte `json:"signature"`
}

// InclusionProof 是 RFC 6962 风格的日志包含证明。
type InclusionProof struct {
	Index uint64 `json:"index"`
	Size  uint64 `json:"size"`
	// RootHash 是该包含证明所锚定的目录树根。
	RootHash Hash   `json:"root_hash"`
	Hashes   []Hash `json:"hashes"`
}

// EventRecord 把一条事件与其锚定到某个签名检查点的包含证明绑定。
type EventRecord struct {
	Event Event          `json:"event"`
	Proof InclusionProof `json:"proof"`
}

// PrekeyProof 证明某一次性公钥承诺属于某设备某代预密钥承诺树。
type PrekeyProof struct {
	Device DeviceID `json:"device"`
	Epoch  uint64   `json:"epoch"`
	Index  uint64   `json:"index"`
	Size   uint64   `json:"size"`
	Leaf   Hash     `json:"leaf"`
	Hashes []Hash   `json:"hashes"`
}

// Receipt 是设备对一次性公钥承诺被某会话消费所签发的回执。
type Receipt struct {
	SessionDigest Hash     `json:"session_digest"`
	Device        DeviceID `json:"device"`
	IdentityPK    []byte   `json:"identity_pk"`
	Commitment    Hash     `json:"commitment"`
	SendTreeSize  uint64   `json:"send_tree_size"`
	Signature     []byte   `json:"signature"`
}

// ReceiptCheckpoint 是离线见证对回执稀疏树签发的检查点。
type ReceiptCheckpoint struct {
	Sequence  uint64 `json:"sequence"`
	RootHash  Hash   `json:"root_hash"`
	Signature []byte `json:"signature"`
}

// SparseProof 是回执稀疏默克尔树的包含/非包含证明（256 层）。
type SparseProof struct {
	Key      Hash      `json:"key"`
	Value    Hash      `json:"value"`
	Exists   bool      `json:"exists"`
	Siblings [256]Hash `json:"siblings"`
}

// RecordKind 枚举最小记录子集里的记录类型。
type RecordKind string

const (
	RecCheckpoint        RecordKind = "checkpoint"
	RecEvent             RecordKind = "event"
	RecPrekeyProof       RecordKind = "prekey_proof"
	RecReceiptCheckpoint RecordKind = "receipt_checkpoint"
	RecSparseProof       RecordKind = "sparse_proof"
	RecReceipt           RecordKind = "receipt"
)

// Record 是可导入、可序列化的一条验收记录。
type Record struct {
	Kind              RecordKind         `json:"kind"`
	Checkpoint        *SignedCheckpoint  `json:"checkpoint,omitempty"`
	Event             *EventRecord       `json:"event,omitempty"`
	PrekeyProof       *PrekeyProof       `json:"prekey_proof,omitempty"`
	ReceiptCheckpoint *ReceiptCheckpoint `json:"receipt_checkpoint,omitempty"`
	SparseProof       *SparseProof       `json:"sparse_proof,omitempty"`
	Receipt           *Receipt           `json:"receipt,omitempty"`
}

// TrustAnchor 是宿主带外获得的长期信任锚。
type TrustAnchor struct {
	DirectoryKey []byte `json:"directory_key"`
	WitnessKey   []byte `json:"witness_key"`
}

// RecoveryRequest 是消息携带的历史会话恢复请求。
//
// WrappedRecoveryKey 是交给宿主自行解封的恢复密钥材料；验收器只在
// 全部证明通过时才把它经回调交付，本身不做解封。
type RecoveryRequest struct {
	SessionDigest      Hash             `json:"session_digest"`
	Account            AccountID        `json:"account"`
	Device             DeviceID         `json:"device"`
	Commitment         Hash             `json:"commitment"`
	SendCheckpoint     SignedCheckpoint `json:"send_checkpoint"`
	WrappedRecoveryKey []byte           `json:"wrapped_recovery_key"`
}

// SessionIdentity 是跨分支必须保持一致的会话身份。
type SessionIdentity struct {
	Account       AccountID `json:"account"`
	Device        DeviceID  `json:"device"`
	IdentityPK    Hash      `json:"identity_pk"`
	PrekeyRoot    Hash      `json:"prekey_root"`
	Commitment    Hash      `json:"commitment"`
	SessionDigest Hash      `json:"session_digest"`
}

// Status 是验收结论三态。
type Status string

const (
	// StatusAccepted 证明齐备且全部通过，回调可交付恢复密钥。
	StatusAccepted Status = "accepted"
	// StatusPending 本地记录不足（如缺少包含证明），补记录后可重新验收。
	StatusPending Status = "pending"
	// StatusRejected 已有记录足以否定该恢复请求。
	StatusRejected Status = "rejected"
)

// Reason 细化验收结论。缺少包含证明与确有双重消费使用不同原因码。
type Reason string

const (
	ReasonNone                Reason = ""
	ReasonBadSignature        Reason = "bad_signature"
	ReasonBadProof            Reason = "bad_proof"
	ReasonMissingCheckpoint   Reason = "missing_checkpoint"
	ReasonMissingEventProof   Reason = "missing_event_inclusion_proof"
	ReasonMissingPrekeyProof  Reason = "missing_prekey_inclusion_proof"
	ReasonMissingReceiptProof Reason = "missing_receipt_proof"
	ReasonMissingReceipt      Reason = "missing_receipt_record"
	ReasonNoReceipt           Reason = "no_receipt"
	ReasonDoubleSpend         Reason = "double_spend"
	ReasonBranchConflict      Reason = "branch_conflict"
	ReasonDeviceRemoved       Reason = "device_removed"
	ReasonNotAuthorized       Reason = "not_authorized"
	ReasonReceiptSignature    Reason = "receipt_signature_invalid"
	ReasonEmptyRequest        Reason = "empty_request"
)

// Decision 是一次验收的完整结论。
type Decision struct {
	Status         Status           `json:"status"`
	Reason         Reason           `json:"reason"`
	Request        *RecoveryRequest `json:"request,omitempty"`
	Identity       *SessionIdentity `json:"identity,omitempty"`
	ViableBranches int              `json:"viable_branches"`
}

// RecoveryCallback 只在验收通过时被调用，宿主在回调中取得恢复密钥材料。
type RecoveryCallback func(decision Decision)

// Bundle 是可独立验证的最小必要记录子集。
type Bundle struct {
	Trust   TrustAnchor     `json:"trust"`
	Request RecoveryRequest `json:"request"`
	Records []Record        `json:"records"`
}
