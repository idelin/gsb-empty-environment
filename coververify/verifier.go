package coververify

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误：Submit 仅在回执本身不被受理时返回非 nil 错误。
var (
	// ErrInvalidReceipt 表示回执缺少模型版本、前状态、输入或输出要素。
	ErrInvalidReceipt = errors.New("coververify: receipt missing required field")

	// ErrReceiptIDReused 表示显式回执 ID 被一份四要素不同的回执复用。
	ErrReceiptIDReused = errors.New("coververify: explicit receipt id reused with different content")
)

type entry struct {
	receipt Receipt
	uid     string
	edge    TransitionKey

	dup     bool
	dupOf   string
	invalid bool

	status Status
	reject bool
	detail string
}

type replayKey struct {
	model string
	prev  Digest
	input Digest
}

// Verifier 是覆盖核验器。零值不可用，必须通过 [New] 构造；方法可并发安全调用。
type Verifier struct {
	mu     sync.Mutex
	oracle OracleFunc

	roots    map[string]map[Digest]struct{}
	required map[TransitionKey]bool

	entries []*entry
	byUID   map[string]*entry
	byID    map[string]*entry

	// 只缓存成功的重放结果：错误/panic 不缓存，便于宿主修复回调后重试。
	replayCache map[replayKey]ReplayResult
}

// New 构造一个以 oracle 为确定性重放回调的核验器。
func New(oracle OracleFunc) *Verifier {
	if oracle == nil {
		panic("coververify: oracle callback is required")
	}
	return &Verifier{
		oracle:      oracle,
		roots:       make(map[string]map[Digest]struct{}),
		required:    make(map[TransitionKey]bool),
		byUID:       make(map[string]*entry),
		byID:        make(map[string]*entry),
		replayCache: make(map[replayKey]ReplayResult),
	}
}

// RegisterRoot 登记某模型版本的可信根状态。链只能从根状态出发逐跳续接。
func (v *Verifier) RegisterRoot(modelVersion string, state Digest) {
	if modelVersion == "" || state == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	set, ok := v.roots[modelVersion]
	if !ok {
		set = make(map[Digest]struct{})
		v.roots[modelVersion] = set
	}
	set[state] = struct{}{}
}

// RequireTransition 声明一条必须覆盖的普通转换边。
func (v *Verifier) RequireTransition(modelVersion string, prev, input Digest) {
	v.RequireEdge(TransitionKey{ModelVersion: modelVersion, PrevState: prev, Input: input}, false)
}

// RequireEdge 声明一条必须覆盖的转换边，expectReject 标明是否期望其为拒绝转换。
// 拒绝边不会因为普通转换被覆盖而满足，反之亦然。
func (v *Verifier) RequireEdge(edge TransitionKey, expectReject bool) {
	if edge.ModelVersion == "" || edge.PrevState == "" || edge.Input == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.required[edge] = expectReject
}

// Submit 受理一份转换回执并重新核验全链。
//
// 四要素缺失或显式 ID 复用会返回非 nil 错误，回执仍以 [StatusInvalid] 留痕；
// 内容与既有回执完全相同时识别为重复，不新增转换边、不重复计数。
func (v *Verifier) Submit(rcpt Receipt) (SubmitResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if rcpt.ModelVersion == "" || rcpt.PrevState == "" || rcpt.Input == "" || rcpt.Output == "" {
		bad := &entry{receipt: rcpt, invalid: true, status: StatusInvalid, detail: ErrInvalidReceipt.Error()}
		v.entries = append(v.entries, bad)
		return SubmitResult{Status: StatusInvalid, Detail: bad.detail}, ErrInvalidReceipt
	}

	uid := contentUID(rcpt)
	if master, ok := v.byUID[uid]; ok {
		dup := &entry{
			receipt: rcpt,
			uid:     uid,
			edge:    edgeOf(rcpt),
			dup:     true,
			dupOf:   uid,
			status:  StatusPending,
		}
		v.entries = append(v.entries, dup)
		// 即便回执重复也重新核验全链：让回调修复、冲突解除等变化能回溯生效。
		v.rebalance()
		return SubmitResult{
			UID:         uid,
			Status:      master.status,
			Duplicate:   true,
			DuplicateOf: uid,
			Detail:      master.detail,
		}, nil
	}

	if rcpt.ID != "" {
		if other, ok := v.byID[rcpt.ID]; ok && other.uid != uid {
			bad := &entry{
				receipt: rcpt,
				uid:     uid,
				edge:    edgeOf(rcpt),
				invalid: true,
				status:  StatusInvalid,
				detail:  fmt.Sprintf("%v: id=%q already bound to %s", ErrReceiptIDReused, rcpt.ID, other.uid),
			}
			v.entries = append(v.entries, bad)
			return SubmitResult{Status: StatusInvalid, Detail: bad.detail}, ErrReceiptIDReused
		}
	}

	newEntry := &entry{receipt: rcpt, uid: uid, edge: edgeOf(rcpt), status: StatusPending}
	v.byUID[uid] = newEntry
	v.byID[firstNonEmpty(rcpt.ID, uid)] = newEntry
	v.entries = append(v.entries, newEntry)

	v.rebalance()
	return SubmitResult{UID: uid, Status: newEntry.status, Detail: newEntry.detail}, nil
}
