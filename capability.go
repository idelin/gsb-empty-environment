package netsec

import (
	"errors"
	"fmt"
)

// ErrUnauthorizedChange 表示补丁含未授权变化,派生整体失败。
var ErrUnauthorizedChange = errors.New("netsec: 补丁含未授权变化")

// ErrStaleCapability 表示审批后模板已变更,能力失效需重新审批。
var ErrStaleCapability = errors.New("netsec: 审批后模板已变更,需重新审批")

// Capability 为审批产物:绑定规范化模板、镜像摘要、
// 服务身份与安全选项,并声明允许派生实例修改的非安全字段。
type Capability struct {
	ApprovalID       string   `json:"approvalID"`
	TemplateRevision int64    `json:"templateRevision"`
	BindingDigest    string   `json:"bindingDigest"`
	Mutable          []string `json:"mutable"`
	Template         Template `json:"template"`
}

// Approve 规范化模板并签发能力。mutable 为允许派生实例
// 修改的非安全字段路径;安全字段一律不可列入。
func Approve(t Template, mutable []string) (Capability, error) {
	norm, err := Normalize(t)
	if err != nil {
		return Capability{}, err
	}
	for _, m := range mutable {
		segs, err := splitPath(m)
		if err != nil {
			return Capability{}, err
		}
		if isSecurityPath(segs) {
			return Capability{}, fmt.Errorf("netsec: 安全字段 /%s 不可列入可变集合", m)
		}
	}
	digest := BindingDigest(norm)
	return Capability{
		ApprovalID:       "appr-" + digest[:12],
		TemplateRevision: norm.Revision,
		BindingDigest:    digest,
		Mutable:          append([]string(nil), mutable...),
		Template:         norm,
	}, nil
}

// Instance 为派生出的工作负载实例:安全部分原样继承
// 批准模板,Fields 为授权补丁应用后的结果。
type Instance struct {
	ApprovalID string          `json:"approvalID"`
	Image      ImageRef        `json:"image"`
	Identity   Identity        `json:"identity"`
	Security   SecurityOptions `json:"security"`
	Fields     map[string]any  `json:"fields"`
}

// Derive 依据能力派生实例:逐项评估补丁授权,
// 全部放行才产出实例,并返回每项变化的授权依据。
func Derive(cap Capability, ops []PatchOp) (Instance, []Decision, error) {
	fields := deepCopyFields(cap.Template.Fields)
	decisions, ok := applyOps(fields, cap.Template.Fields, cap.Mutable, cap.ApprovalID, ops)
	if !ok {
		return Instance{}, decisions, ErrUnauthorizedChange
	}
	inst := Instance{
		ApprovalID: cap.ApprovalID,
		Image:      cap.Template.Image,
		Identity:   cap.Template.Identity,
		Security:   cap.Template.Security,
		Fields:     fields,
	}
	return inst, decisions, nil
}
