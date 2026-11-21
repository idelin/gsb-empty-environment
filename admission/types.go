package admission

import "errors"

// 权限以位掩码表示，本包只裁决执行位；更新只允许收紧，不得授予新权限。
const (
	PermNone uint8 = 0
	PermExec uint8 = 1 << iota
)

// Trust 描述内容来源的可信级别。
type Trust uint8

const (
	TrustUnknown   Trust = iota
	TrustTrusted         // 发布者可信且签名通过
	TrustUntrusted       // 签名通过但发布者不在可信集合内
	TrustRejected        // 签名验证失败
)

// Kind 区分更新收据的类型。
type Kind uint8

const (
	KindInvalid    Kind = iota
	KindContent         // 修改映像内容
	KindPermission      // 仅收紧执行权限
)

// ContentEntry 是内容更新对一段分片对齐区间的声明。
// 同一摘要可被多个位置引用，形成别名页。
type ContentEntry struct {
	Pages  Range
	Digest []byte
}

// Receipt 是一条片段化的签名更新收据。
// 无论哪种类型，都绑定发布者、旧根、新根与作用页区间。
type Receipt struct {
	Publisher  string
	OldRoot    []byte
	NewRoot    []byte
	Pages      Range
	Kind       Kind
	Signature  []byte
	Content    []ContentEntry // 仅 KindContent 使用，可声明 Pages 的若干子区间
	RevokeExec []Range        // 仅 KindPermission 使用，被撤销执行权的页区间
}

// PermissionRange 是检查点中某个映像位置区间的初始权限。
type PermissionRange struct {
	Pages Range
	Perm  uint8
}

// BaseContent 是检查点已知的一段基线内容，必须与分片边界对齐。
type BaseContent struct {
	Pages  Range
	Digest []byte
	Trust  Trust
}

// Checkpoint 是恢复起点：旧映像已知的内容、权限与可信摘要。
type Checkpoint struct {
	Root        []byte
	Shards      []Range           // 宿主可核验的分片边界，完整覆盖 48 位空间
	Permissions []PermissionRange // 初始权限，必须完整覆盖页空间
	Base        []BaseContent     // 基线内容，分片对齐、互不重叠，可只覆盖部分空间
}

// Verifier 由宿主实现：负责密码学签名验证与发布者信任判定。
// 返回的 Trust 只能是 TrustTrusted、TrustUntrusted 或 TrustRejected。
type Verifier interface {
	VerifyReceipt(r Receipt) Trust
}

// VerifierFunc 让普通函数满足 Verifier 接口。
type VerifierFunc func(r Receipt) Trust

func (f VerifierFunc) VerifyReceipt(r Receipt) Trust { return f(r) }

// Verdict 是单页级别的三分类结论。
type Verdict uint8

const (
	VerdictUnknown    Verdict = iota
	VerdictExecutable         // 所有相容历史中均为同一可信内容且权限足够
	VerdictForbidden          // 所有相容历史中均不可执行
)

// Decision 是请求区间内按结论合并后的精确子区间。
type Decision struct {
	Verdict Verdict
	Pages   Range
}

// AdmissionInput 是一次恢复准入请求。
type AdmissionInput struct {
	Checkpoint Checkpoint
	Receipts   []Receipt
	Heads      [][]byte // 宿主认为可能是当前根的集合；为空时取所有叶根
	Need       uint8    // 所需权限位，零值按 PermExec 处理
	Request    Range    // 待裁决页区间
	Verifier   Verifier
}

// 输入校验错误。宿主可用 errors.Is 区分拒绝原因。
var (
	ErrBadRequest      = errors.New("admission: invalid request range")
	ErrBadShards       = errors.New("admission: shards must partition the full 48-bit page space")
	ErrBadPermissions  = errors.New("admission: checkpoint permissions must cover the full page space")
	ErrBadReceipt      = errors.New("admission: malformed receipt")
	ErrUnreachable     = errors.New("admission: receipt or head is not reachable from checkpoint")
	ErrRootConflict    = errors.New("admission: conflicting receipts declare the same new root")
	ErrGraphCycle      = errors.New("admission: update graph contains a cycle")
	ErrNoHistory       = errors.New("admission: no history is compatible with the checkpoint")
	ErrMissingVerifier = errors.New("admission: verifier is required")
)
