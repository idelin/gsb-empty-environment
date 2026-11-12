package netsec

import (
	"errors"
	"path"
)

// ViolationCode 标识被哪条安全规则拒绝。
type ViolationCode string

const (
	// ViolationSensitiveClone 表示 open_tree 在敏感前缀下克隆挂载树。
	ViolationSensitiveClone ViolationCode = "sensitive_path_clone"
	// ViolationUnknownDescriptor 表示 move_mount/mount_setattr 引用了
	// 本序列内未登记或已被替换掉的描述符。
	ViolationUnknownDescriptor ViolationCode = "unknown_descriptor"
	// ViolationInvalidSource 表示 move_mount 的来源描述符不是由
	// open_tree 克隆或 fsmount 产生（例如普通 open_tree 路径句柄）。
	ViolationInvalidSource ViolationCode = "invalid_move_source"
	// ViolationDescriptorReused 表示同一来源描述符被第二次用于 move_mount。
	ViolationDescriptorReused ViolationCode = "descriptor_reused"
	// ViolationInodeMismatch 表示目标路径检查时与使用时解析到不同 inode。
	ViolationInodeMismatch ViolationCode = "target_inode_mismatch"
	// ViolationSharedSubtree 表示递归克隆带入的 shared 子树在
	// move_mount 前未被改为 private 或 slave。
	ViolationSharedSubtree ViolationCode = "shared_subtree"
	// ViolationDescriptorClosed 表示描述符关闭后仍被使用。
	ViolationDescriptorClosed ViolationCode = "descriptor_closed"
)

// Violation 是一次校验失败的结构化描述，实现了 error 接口。
type Violation struct {
	Code       ViolationCode
	Index      int    // 触发违规的事件在序列中的下标（从 0 开始）
	Path       string // 相关路径（敏感克隆 / 目标替换时填写）
	Descriptor int    // 相关描述符（描述符类违规时填写）
	Detail     string // 人类可读的说明
	Event      Event  // 触发违规的原始事件
}

func (v *Violation) Error() string {
	if v == nil {
		return "<nil>"
	}
	return v.Detail
}

// fdState 跟踪一个已登记描述符的安全状态。
type fdState struct {
	moveSource    bool // 是否可作为 move_mount 来源（克隆或 fsmount）
	closed        bool
	used          bool // 是否已被 move_mount 消费
	carriesShared bool // 树下是否仍带 shared 传播的子挂载
}

// Validator 对新挂载接口（open_tree / fsmount / mount_setattr /
// move_mount / close）的事件序列做调用方安全策略校验。
//
// 一个 Validator 实例维护单条序列的状态，可通过 Feed 增量喂入事件，
// 也可直接使用无状态的 ValidateSequence 一次性校验完整序列。
type Validator struct {
	prefixes []string
	fds      map[int]*fdState
}

// NewValidator 创建校验器。sensitivePrefixes 为调用方给出的敏感路径
// 前缀（如 "/proc"、"/sys"、宿主根等），会做路径清洗和去重；
// 前缀匹配按路径组件进行，"/proc" 不会误伤 "/procure"。
func NewValidator(sensitivePrefixes []string) *Validator {
	seen := make(map[string]struct{}, len(sensitivePrefixes))
	prefixes := make([]string, 0, len(sensitivePrefixes))
	for _, prefix := range sensitivePrefixes {
		cleaned := cleanPath(prefix)
		if cleaned == "" || cleaned == "." {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		prefixes = append(prefixes, cleaned)
	}
	return &Validator{
		prefixes: prefixes,
		fds:      make(map[int]*fdState),
	}
}

// Reset 清空已记录的描述符状态，使校验器可复用。敏感前缀保持不变。
func (v *Validator) Reset() {
	v.fds = make(map[int]*fdState)
}

// ValidateSequence 校验完整事件序列，返回第一个违规；序列合法时返回 nil。
func ValidateSequence(sensitivePrefixes []string, events []Event) *Violation {
	validator := NewValidator(sensitivePrefixes)
	for index, event := range events {
		if violation := validator.feed(index, event); violation != nil {
			return violation
		}
	}
	return nil
}

// Feed 增量喂入一个事件并立即校验。eventIndex 为该事件在整条序列中的
// 下标，仅用于回填 Violation.Index。返回首个违规后，校验器状态保留在
// 违规发生前一刻，调用方不应再继续喂入后续事件。
func (v *Validator) Feed(eventIndex int, event Event) *Violation {
	return v.feed(eventIndex, event)
}

func (v *Validator) feed(index int, event Event) *Violation {
	switch e := event.(type) {
	case OpenTreeEvent:
		return v.handleOpenTree(index, e)
	case FSMountEvent:
		return v.handleFSMount(index, e)
	case MountSetattrEvent:
		return v.handleMountSetattr(index, e)
	case MoveMountEvent:
		return v.handleMoveMount(index, e)
	case CloseEvent:
		v.handleClose(e)
		return nil
	default:
		return &Violation{
			Code:   ViolationUnknownDescriptor,
			Index:  index,
			Detail: "unknown event type in mount call sequence",
			Event:  event,
		}
	}
}

func (v *Validator) handleOpenTree(index int, e OpenTreeEvent) *Violation {
	clone := e.Flags.Has(OpenTreeClone)

	// 规则一：克隆路径落在敏感前缀下即拒绝。非克隆的路径句柄不复制挂载树，
	// 不构成宿主挂载点泄露，因此不受此限。
	if clone && v.isSensitive(e.Path) {
		return &Violation{
			Code:       ViolationSensitiveClone,
			Index:      index,
			Path:       e.Path,
			Descriptor: e.Descriptor,
			Detail:     "open_tree clone path is under a sensitive prefix: " + e.Path,
			Event:      e,
		}
	}

	v.fds[e.Descriptor] = &fdState{
		moveSource:    clone,
		carriesShared: clone && e.CarriesShared,
	}
	return nil
}

func (v *Validator) handleFSMount(index int, e FSMountEvent) *Violation {
	// fsmount 产生的是全新挂载，不携带宿主任何 shared 子树。
	v.fds[e.Descriptor] = &fdState{moveSource: true}
	return nil
}

func (v *Validator) handleMountSetattr(index int, e MountSetattrEvent) *Violation {
	state, ok := v.fds[e.Descriptor]
	if !ok {
		return &Violation{
			Code:       ViolationUnknownDescriptor,
			Index:      index,
			Descriptor: e.Descriptor,
			Detail:     "mount_setattr references descriptor not produced in this sequence",
			Event:      e,
		}
	}
	if state.closed {
		return &Violation{
			Code:       ViolationDescriptorClosed,
			Index:      index,
			Descriptor: e.Descriptor,
			Detail:     "mount_setattr uses a closed descriptor",
			Event:      e,
		}
	}

	// 规则四：只有递归地把传播改为 private/slave，才能保证克隆树深处
	// 的 shared 子挂载也被一并收敛。非递归修改无法触及子挂载，保持 fail-closed。
	if e.Recursive && (e.Propagation == PropagationPrivate || e.Propagation == PropagationSlave) {
		state.carriesShared = false
	}
	return nil
}

func (v *Validator) handleMoveMount(index int, e MoveMountEvent) *Violation {
	state, ok := v.fds[e.SourceDescriptor]

	// 规则二/五：来源必须是本序列内登记且仍然有效的描述符。
	if !ok {
		return &Violation{
			Code:       ViolationUnknownDescriptor,
			Index:      index,
			Descriptor: e.SourceDescriptor,
			Detail:     "move_mount source descriptor was not produced in this sequence",
			Event:      e,
		}
	}
	if state.closed {
		return &Violation{
			Code:       ViolationDescriptorClosed,
			Index:      index,
			Path:       e.TargetPath,
			Descriptor: e.SourceDescriptor,
			Detail:     "move_mount source descriptor has already been closed",
			Event:      e,
		}
	}
	if !state.moveSource {
		return &Violation{
			Code:       ViolationInvalidSource,
			Index:      index,
			Path:       e.TargetPath,
			Descriptor: e.SourceDescriptor,
			Detail:     "move_mount source must be an open_tree clone or an fsmount descriptor",
			Event:      e,
		}
	}
	if state.used {
		return &Violation{
			Code:       ViolationDescriptorReused,
			Index:      index,
			Path:       e.TargetPath,
			Descriptor: e.SourceDescriptor,
			Detail:     "move_mount source descriptor can only be used once",
			Event:      e,
		}
	}

	// 规则三：检查与使用之间目标路径的解析落点必须一致。
	if !e.CheckInode.Equal(e.UseInode) {
		return &Violation{
			Code:       ViolationInodeMismatch,
			Index:      index,
			Path:       e.TargetPath,
			Descriptor: e.SourceDescriptor,
			Detail:     "target path resolved to a different inode between check and use",
			Event:      e,
		}
	}

	// 规则四：仍带 shared 子树的克隆不允许挂入目标命名空间。
	if state.carriesShared {
		return &Violation{
			Code:       ViolationSharedSubtree,
			Index:      index,
			Path:       e.TargetPath,
			Descriptor: e.SourceDescriptor,
			Detail:     "recursive clone carries a shared subtree; make it private or slave first",
			Event:      e,
		}
	}

	state.used = true
	return nil
}

func (v *Validator) handleClose(e CloseEvent) {
	if state, ok := v.fds[e.Descriptor]; ok {
		state.closed = true
	}
	// 关闭未登记描述符不属于本序列策略范围，忽略。
}

// isSensitive 按路径组件判断 p 是否位于任一敏感前缀之下（含前缀自身）。
func (v *Validator) isSensitive(p string) bool {
	cleaned := cleanPath(p)
	for _, prefix := range v.prefixes {
		if cleaned == prefix || hasPathPrefix(cleaned, prefix) {
			return true
		}
	}
	return false
}

func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	cleaned := path.Clean(p)
	// path.Clean 会把相对路径保留为相对形式；容器路径均为绝对路径，
	// 缺少前导斜杠时补回，保证前缀比较语义稳定。
	if len(p) > 0 && p[0] == '/' && len(cleaned) > 0 && cleaned[0] != '/' {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// hasPathPrefix 判断 cleaned 是否等于 prefix 或位于 prefix 的子目录，
// 按组件边界比较，避免 "/proc" 误匹配 "/procure"。
func hasPathPrefix(cleaned, prefix string) bool {
	if prefix == "/" {
		return len(cleaned) > 0 && cleaned[0] == '/'
	}
	if len(cleaned) <= len(prefix) || cleaned[:len(prefix)] != prefix {
		return false
	}
	return cleaned[len(prefix)] == '/'
}

// AsViolation 从 error 中提取结构化 Violation，便于调用方按 Code 分支处理。
func AsViolation(err error) (*Violation, bool) {
	var violation *Violation
	if errors.As(err, &violation) {
		return violation, true
	}
	return nil, false
}
