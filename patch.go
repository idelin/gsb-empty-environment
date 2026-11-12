package netsec

import (
	"fmt"
	"strings"
)

// 补丁操作类型,语义对齐 JSON Patch (RFC 6902) 的子集。
const (
	OpAdd     = "add"
	OpRemove  = "remove"
	OpReplace = "replace"
	OpMove    = "move"
)

// 授权依据原因码,随每项变化返回给控制宿主。
const (
	ReasonApprovedMutable   = "approved-mutable-field" // 字段在批准的可变集合内
	ReasonSecurityBound     = "security-bound"         // 字段被审批绑定,禁止变更
	ReasonNotMutable        = "not-in-approved-set"    // 非安全字段但不在批准可变集合内
	ReasonDefaultRestored   = "default-restored"       // 删除后恢复批准默认值
	ReasonMoveAuthorized    = "move-authorized"        // 移动两端均在可变集合内
	ReasonRevisionConflict  = "revision-conflict"      // 并发更新版本冲突
	ReasonRequiresReapprove = "requires-reapproval"    // 安全字段变更需重新审批
	ReasonMissingSource     = "missing-source"         // 移动/替换的源字段不存在
)

// PatchOp 为一条结构化补丁操作。
// Path 形如 "/env/LOG_LEVEL";Move 使用 From 作为源路径。
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	From  string `json:"from,omitempty"`
	Value any    `json:"value,omitempty"`
}

// Decision 为单项变化的授权依据。
type Decision struct {
	Op       PatchOp `json:"op"`
	Allowed  bool    `json:"allowed"`
	Reason   string  `json:"reason"`
	Basis    string  `json:"basis"`
	OldValue any     `json:"oldValue,omitempty"`
	NewValue any     `json:"newValue,omitempty"`
}

// splitPath 将 "/a/b" 或 "a/b" 拆为路径段。
func splitPath(p string) ([]string, error) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil, fmt.Errorf("netsec: 路径不能为空")
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("netsec: 路径 %q 含空段", p)
		}
	}
	return segs, nil
}

func pathRoot(segs []string) string { return segs[0] }

// isSecurityPath 判断路径是否落在审批绑定的安全字段内。
func isSecurityPath(segs []string) bool {
	return securityBoundRoots[pathRoot(segs)]
}

// isMutable 判断路径是否被批准的可变集合覆盖(按段前缀匹配)。
func isMutable(mutable []string, segs []string) bool {
	for _, m := range mutable {
		ms, err := splitPath(m)
		if err != nil || len(ms) > len(segs) {
			continue
		}
		match := true
		for i := range ms {
			if ms[i] != segs[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func getField(m map[string]any, segs []string) (any, bool) {
	cur := m
	for i, s := range segs {
		v, ok := cur[s]
		if !ok {
			return nil, false
		}
		if i == len(segs)-1 {
			return v, true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

func setField(m map[string]any, segs []string, v any) {
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = v
}

func deleteField(m map[string]any, segs []string) {
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, segs[len(segs)-1])
}

func deepCopyFields(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			out[k] = deepCopyFields(sub)
		} else {
			out[k] = v
		}
	}
	return out
}

// authorizePath 对单一路径做授权分类,返回判定与是否放行。
func authorizePath(mutable []string, segs []string, approvalID string) (Decision, bool) {
	d := Decision{}
	switch {
	case isSecurityPath(segs):
		d.Allowed = false
		d.Reason = ReasonSecurityBound
		d.Basis = fmt.Sprintf("字段 /%s 由审批 %s 绑定(镜像摘要/身份/安全选项),禁止变更", strings.Join(segs, "/"), approvalID)
	case isMutable(mutable, segs):
		d.Allowed = true
		d.Reason = ReasonApprovedMutable
		d.Basis = fmt.Sprintf("字段 /%s 在审批 %s 批准的可变非安全字段集合内", strings.Join(segs, "/"), approvalID)
	default:
		d.Allowed = false
		d.Reason = ReasonNotMutable
		d.Basis = fmt.Sprintf("字段 /%s 非安全字段但不在审批 %s 批准的可变集合内", strings.Join(segs, "/"), approvalID)
	}
	return d, d.Allowed
}

// applyOps 在 fields 上依次执行补丁,逐项返回授权依据。
// defaults 为批准模板的字段快照,用于删除后的默认值恢复。
// 任一操作被拒绝时整体失败(原子性),但仍返回全部判定。
func applyOps(fields, defaults map[string]any, mutable []string, approvalID string, ops []PatchOp) ([]Decision, bool) {
	decisions := make([]Decision, 0, len(ops))
	allAllowed := true
	for _, op := range ops {
		segs, err := splitPath(op.Path)
		if err != nil {
			decisions = append(decisions, Decision{Op: op, Allowed: false, Reason: ReasonNotMutable, Basis: err.Error()})
			allAllowed = false
			continue
		}
		auth, ok := authorizePath(mutable, segs, approvalID)
		auth.Op = op
		if !ok {
			decisions = append(decisions, auth)
			allAllowed = false
			continue
		}
		switch op.Op {
		case OpAdd, OpReplace:
			old, _ := getField(fields, segs)
			auth.OldValue = old
			setField(fields, segs, op.Value)
			auth.NewValue = op.Value
		case OpRemove:
			old, existed := getField(fields, segs)
			auth.OldValue = old
			deleteField(fields, segs)
			if def, ok := getField(defaults, segs); ok {
				setField(fields, segs, def)
				auth.NewValue = def
				auth.Reason = ReasonDefaultRestored
				auth.Basis = fmt.Sprintf("删除 /%s 后恢复审批 %s 的默认值 %v", strings.Join(segs, "/"), approvalID, def)
			} else if !existed {
				auth.Basis += "(字段本不存在,删除为空操作)"
			}
		case OpMove:
			fromSegs, err := splitPath(op.From)
			if err != nil {
				auth.Allowed = false
				auth.Reason = ReasonMissingSource
				auth.Basis = err.Error()
				allAllowed = false
				break
			}
			fromAuth, fromOK := authorizePath(mutable, fromSegs, approvalID)
			if !fromOK {
				auth.Allowed = false
				auth.Reason = fromAuth.Reason
				auth.Basis = fmt.Sprintf("移动源端未授权: %s", fromAuth.Basis)
				allAllowed = false
				break
			}
			val, existed := getField(fields, fromSegs)
			if !existed {
				auth.Allowed = false
				auth.Reason = ReasonMissingSource
				auth.Basis = fmt.Sprintf("移动源 /%s 不存在", strings.Join(fromSegs, "/"))
				allAllowed = false
				break
			}
			deleteField(fields, fromSegs)
			setField(fields, segs, val)
			auth.OldValue = val
			auth.NewValue = val
			auth.Reason = ReasonMoveAuthorized
			auth.Basis = fmt.Sprintf("移动 /%s -> /%s,两端均在审批 %s 批准的可变集合内",
				strings.Join(fromSegs, "/"), strings.Join(segs, "/"), approvalID)
		default:
			auth.Allowed = false
			auth.Reason = ReasonNotMutable
			auth.Basis = fmt.Sprintf("不支持的补丁操作 %q", op.Op)
			allAllowed = false
		}
		decisions = append(decisions, auth)
	}
	return decisions, allAllowed
}
