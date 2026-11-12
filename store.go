package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// ErrRevisionConflict 表示并发模板更新的乐观并发冲突。
var ErrRevisionConflict = errors.New("netsec: 模板版本冲突")

// Store 为版本化模板存储,供 Go 控制宿主并发使用。
type Store struct {
	mu   sync.Mutex
	tmpl Template
}

// NewStore 以规范化模板初始化存储,版本从 1 开始。
func NewStore(t Template) (*Store, error) {
	norm, err := Normalize(t)
	if err != nil {
		return nil, err
	}
	norm.Revision = 1
	return &Store{tmpl: norm}, nil
}

// Current 返回当前模板快照。
func (s *Store) Current() Template {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tmpl
}

// Update 以结构化补丁并发安全地更新模板。
// expectedRevision 不匹配时返回 ErrRevisionConflict;
// 安全字段变更被拒绝并给出"需重新审批"的授权依据;
// 非安全字段更新成功后版本递增,既有能力随之失效。
func (s *Store) Update(expectedRevision int64, ops []PatchOp) (Template, []Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedRevision != s.tmpl.Revision {
		d := Decision{
			Allowed: false,
			Reason:  ReasonRevisionConflict,
			Basis:   fmt.Sprintf("期望版本 %d,当前版本 %d,并发更新冲突需重试", expectedRevision, s.tmpl.Revision),
		}
		return s.tmpl, []Decision{d}, ErrRevisionConflict
	}
	fields := deepCopyFields(s.tmpl.Fields)
	// 模板更新不借助既有能力:非安全字段允许演进,
	// 安全字段一律拒绝并要求重新审批。
	mutable := make([]string, 0, len(s.tmpl.Fields))
	for k := range s.tmpl.Fields {
		mutable = append(mutable, k)
	}
	decisions, ok := applyOps(fields, s.tmpl.Fields, mutable, "template-store", ops)
	for i := range decisions {
		if decisions[i].Reason == ReasonSecurityBound {
			decisions[i].Reason = ReasonRequiresReapprove
			decisions[i].Basis += ";模板安全字段变更必须走重新审批流程"
		}
	}
	if !ok {
		return s.tmpl, decisions, ErrUnauthorizedChange
	}
	s.tmpl.Fields = fields
	s.tmpl.Revision++
	return s.tmpl, decisions, nil
}

// Approve 对当前模板版本签发能力。
func (s *Store) Approve(mutable []string) (Capability, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Approve(s.tmpl, mutable)
}

// Derive 校验能力未因模板更新而失效后派生实例。
func (s *Store) Derive(cap Capability, ops []PatchOp) (Instance, []Decision, error) {
	s.mu.Lock()
	cur := s.tmpl
	s.mu.Unlock()
	if cap.TemplateRevision != cur.Revision || cap.BindingDigest != BindingDigest(cur) {
		d := Decision{
			Allowed: false,
			Reason:  ReasonRequiresReapprove,
			Basis: fmt.Sprintf("能力 %s 绑定版本 %d/摘要 %.12s,当前版本 %d/摘要 %.12s,审批后模板已变更",
				cap.ApprovalID, cap.TemplateRevision, cap.BindingDigest, cur.Revision, BindingDigest(cur)),
		}
		return Instance{}, []Decision{d}, ErrStaleCapability
	}
	return Derive(cap, ops)
}
