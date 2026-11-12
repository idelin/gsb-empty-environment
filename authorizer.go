package netsec

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
)

// 内置角色名。
const (
	RoleAdmin    = "admin"    // 管理员：可用 human-monitor-command 等全部命令。
	RoleOperator = "operator" // 普通调用方：可执行变更类命令，但路径受限。
	RoleReadonly = "readonly" // 只读：仅允许 query- 前缀命令。
)

// 拒绝原因，便于代理侧记录审计日志。
const (
	ReasonMalformedJSON      = "malformed_json"
	ReasonMissingExecute     = "missing_execute"
	ReasonUnknownPeer        = "unknown_peer"
	ReasonUnsafeSocketMode   = "unsafe_socket_mode"
	ReasonCapabilitiesFirst  = "capabilities_not_negotiated"
	ReasonDuplicateCapab     = "duplicate_qmp_capabilities"
	ReasonReadonlyForbidden  = "readonly_command_forbidden"
	ReasonAdminOnly          = "admin_only_command"
	ReasonPathOutsideAllowed = "path_outside_allowed_dirs"
	ReasonPathNotAbsolute    = "path_not_absolute"
	ReasonDangerousDevice    = "dangerous_device_path"
	ReasonEmptyArguments     = "empty_arguments"
)

// PeerCred 是连接对端的身份与套接字文件信息。
//
// UID/GID/PID 来自 getsockopt(SO_PEERCRED)（Linux）；Mode 是套接字
// inode 的权限位（如 os.FileMode(0o660)），用于检测全局可写的不安全套接字。
type PeerCred struct {
	UID  uint32
	GID  uint32
	PID  int32
	Mode os.FileMode
}

// RolePolicy 描述单个角色被授予的能力。
type RolePolicy struct {
	// Name 为角色名；内置角色使用 Role* 常量，匹配区分大小写。
	Name string

	// Admin 为 true 时允许 human-monitor-command，并跳过只读限制。
	Admin bool

	// QueryOnly 为 true 时只允许 "query-" 前缀命令（qmp_capabilities 除外）。
	QueryOnly bool

	// AllowedDirs 是 getfd、add-fd、blockdev-add 路径参数允许落在的登记目录。
	// 目录必须是绝对路径，会在 NewAuthorizer 中统一清洗。
	AllowedDirs []string
}

// Policy 是代理进程级的完整授权配置。
type Policy struct {
	// Roles 按角色名索引全部角色策略。
	Roles map[string]*RolePolicy

	// UIDRoles 把 SO_PEERCRED 的 uid 映射到角色名；命中优先级高于 GIDRoles。
	UIDRoles map[uint32]string

	// GIDRoles 把 gid 映射到角色名；uid 未命中时回退使用。
	GIDRoles map[uint32]string

	// RequireSafeSocketMode 为 true 时，套接字文件权限位不得含其他用户写位
	// （mode&0o002 != 0），否则该对端的一切命令都被拒绝。
	RequireSafeSocketMode bool
}

// Decision 是单条 QMP 命令的授权结果。
type Decision struct {
	// Allowed 为 true 表示放行。
	Allowed bool

	// Reason 在拒绝时给出机器可读原因；放行时为空。
	Reason string

	// Detail 在拒绝时给出人类可读的补充说明。
	Detail string

	// Command 是实际请求的 execute 值（解析失败时为空）。
	Command string

	// ID 是原始请求中的 id 字段，原样透传，方便代理把结果对应回请求。
	ID json.RawMessage
}

// qmpCommand 是 QMP 请求 JSON 的最小解析结构。
type qmpCommand struct {
	Execute   string          `json:"execute"`
	Arguments json.RawMessage `json:"arguments"`
	ID        json.RawMessage `json:"id"`
}

// Authorizer 为一个 QMP 代理持有策略，并按对端跟踪能力协商状态。
// 一个对端连接对应一个逻辑会话；不同连接共享同一个 Authorizer 即可，
// 协商状态按 PeerCred 键隔离。
type Authorizer struct {
	policy *Policy

	mu       sync.Mutex
	sessions map[peerKey]bool
}

type peerKey struct {
	uid uint32
	gid uint32
	pid int32
}

// NewAuthorizer 校验并清洗策略，返回可用的 Authorizer。
func NewAuthorizer(p *Policy) (*Authorizer, error) {
	if p == nil {
		return nil, fmt.Errorf("netsec: nil policy")
	}
	if len(p.Roles) == 0 {
		return nil, fmt.Errorf("netsec: policy has no roles")
	}

	for name, role := range p.Roles {
		if role == nil {
			return nil, fmt.Errorf("netsec: nil role policy for %q", name)
		}
		if role.Name == "" {
			role.Name = name
		}
		if role.Name != name {
			return nil, fmt.Errorf("netsec: role map key %q does not match Name %q", name, role.Name)
		}
		dirs := make([]string, 0, len(role.AllowedDirs))
		for _, dir := range role.AllowedDirs {
			if !path.IsAbs(dir) {
				return nil, fmt.Errorf("netsec: role %q has non-absolute allowed dir %q", name, dir)
			}
			dirs = append(dirs, cleanPath(dir))
		}
		role.AllowedDirs = dirs
	}

	for uid, role := range p.UIDRoles {
		if _, ok := p.Roles[role]; !ok {
			return nil, fmt.Errorf("netsec: uid %d maps to undefined role %q", uid, role)
		}
	}
	for gid, role := range p.GIDRoles {
		if _, ok := p.Roles[role]; !ok {
			return nil, fmt.Errorf("netsec: gid %d maps to undefined role %q", gid, role)
		}
	}

	return &Authorizer{
		policy:   p,
		sessions: make(map[peerKey]bool),
	}, nil
}

// Reset 清除指定对端的能力协商状态；连接关闭或重连时调用。
func (a *Authorizer) Reset(peer PeerCred) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, peerKey{peer.UID, peer.GID, peer.PID})
}

// Authorize 判定单条 QMP 请求。raw 为单个请求对象的 JSON 字节。
func (a *Authorizer) Authorize(peer PeerCred, raw json.RawMessage) Decision {
	return a.authorizeOne(peer, raw)
}

// AuthorizeBatch 判定一条批量消息。raw 可以是单个 JSON 对象，也可以是
// JSON 数组；数组中的每条命令独立判定，某条被绝不影响其余命令的结果，
// 返回切片与数组元素顺序一一对应。单对象输入返回长度为 1 的切片。
func (a *Authorizer) AuthorizeBatch(peer PeerCred, raw json.RawMessage) []Decision {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return []Decision{{
				Allowed: false,
				Reason:  ReasonMalformedJSON,
				Detail:  err.Error(),
			}}
		}
		decisions := make([]Decision, 0, len(items))
		for _, item := range items {
			decisions = append(decisions, a.authorizeOne(peer, item))
		}
		return decisions
	}
	return []Decision{a.authorizeOne(peer, raw)}
}

func (a *Authorizer) authorizeOne(peer PeerCred, raw json.RawMessage) Decision {
	var cmd qmpCommand
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return Decision{Allowed: false, Reason: ReasonMalformedJSON, Detail: err.Error()}
	}
	if cmd.Execute == "" {
		return Decision{
			Allowed: false,
			Reason:  ReasonMissingExecute,
			Detail:  "request has no execute field",
			ID:      cmd.ID,
		}
	}

	role, ok := a.resolveRole(peer)
	if !ok {
		return deny(cmd, ReasonUnknownPeer,
			fmt.Sprintf("no role mapping for uid=%d gid=%d", peer.UID, peer.GID))
	}

	if a.policy.RequireSafeSocketMode && peer.Mode&0o002 != 0 {
		return deny(cmd, ReasonUnsafeSocketMode,
			fmt.Sprintf("socket mode %#o is writable by others", uint32(peer.Mode)))
	}

	key := peerKey{peer.UID, peer.GID, peer.PID}

	// qmp_capabilities 是协商命令：任何已识别角色在协商前都可发送，
	// 但每个会话只允许成功一次；命令名区分大小写。
	if cmd.Execute == "qmp_capabilities" {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.sessions[key] {
			return deny(cmd, ReasonDuplicateCapab, "qmp_capabilities already negotiated")
		}
		a.sessions[key] = true
		return Decision{Allowed: true, Command: cmd.Execute, ID: cmd.ID}
	}

	a.mu.Lock()
	negotiated := a.sessions[key]
	a.mu.Unlock()
	if !negotiated {
		return deny(cmd, ReasonCapabilitiesFirst,
			"qmp_capabilities must complete before any other command")
	}

	// human-monitor-command 仅 admin 角色可用。
	if cmd.Execute == "human-monitor-command" && !role.Admin {
		return deny(cmd, ReasonAdminOnly,
			fmt.Sprintf("role %q may not use human-monitor-command", role.Name))
	}

	// 只读角色只允许 query- 前缀命令（精确大小写匹配）。
	if role.QueryOnly && !strings.HasPrefix(cmd.Execute, "query-") {
		return deny(cmd, ReasonReadonlyForbidden,
			fmt.Sprintf("readonly role %q may only run query-* commands", role.Name))
	}

	args, reason, detail := parseArguments(cmd)
	if reason != "" {
		return deny(cmd, reason, detail)
	}

	// 全局危险参数检查：driver 为 host_device/file 且指向 /dev 或 /proc。
	if reason, detail := findDangerousReference(args); reason != "" {
		return deny(cmd, reason, detail)
	}

	// 路径登记目录限制：getfd、add-fd、blockdev-add。
	if isPathRestrictedCommand(cmd.Execute) {
		if reason, detail := checkPathContainment(args, role); reason != "" {
			return deny(cmd, reason, detail)
		}
	}

	return Decision{Allowed: true, Command: cmd.Execute, ID: cmd.ID}
}

func (a *Authorizer) resolveRole(peer PeerCred) (*RolePolicy, bool) {
	if name, ok := a.policy.UIDRoles[peer.UID]; ok {
		role, found := a.policy.Roles[name]
		return role, found
	}
	if name, ok := a.policy.GIDRoles[peer.GID]; ok {
		role, found := a.policy.Roles[name]
		return role, found
	}
	return nil, false
}

func parseArguments(cmd qmpCommand) (map[string]any, string, string) {
	if len(cmd.Arguments) == 0 {
		return map[string]any{}, "", ""
	}
	var args map[string]any
	if err := json.Unmarshal(cmd.Arguments, &args); err != nil {
		return nil, ReasonMalformedJSON, "arguments must be a JSON object: " + err.Error()
	}
	if args == nil {
		return nil, ReasonEmptyArguments, "arguments must be a JSON object, not null"
	}
	return args, "", ""
}

// isPathRestrictedCommand 命令名区分大小写，精确匹配。
func isPathRestrictedCommand(execute string) bool {
	switch execute {
	case "getfd", "add-fd", "blockdev-add":
		return true
	default:
		return false
	}
}

// pathKeys 是 arguments 中承载文件路径的已知字段名（嵌套递归匹配）。
var pathKeys = map[string]bool{
	"filename": true,
	"path":     true,
	"dir":      true,
	"dirname":  true,
}

// dangerousDrivers 需要配合 /dev、/proc 路径检查的 driver 取值（区分大小写）。
var dangerousDrivers = map[string]bool{
	"host_device": true,
	"file":        true,
}

// findDangerousReference 递归遍历 arguments，查找 driver 为 host_device/file
// 且路径字段指向 /dev、/proc 的组合。
func findDangerousReference(args map[string]any) (string, string) {
	var reason, detail string
	walkMaps(args, func(node map[string]any) {
		if reason != "" {
			return
		}
		driver, _ := node["driver"].(string)
		if !dangerousDrivers[driver] {
			return
		}
		for key, value := range node {
			p, ok := value.(string)
			if !ok || !pathKeys[key] {
				continue
			}
			if isProtectedPath(p) {
				reason = ReasonDangerousDevice
				detail = fmt.Sprintf("driver %q must not reference %s via %q=%q",
					driver, protectedRoot(p), key, p)
				return
			}
		}
	})
	return reason, detail
}

// checkPathContainment 递归收集 arguments 中所有路径字段，要求每个路径
// 都是绝对路径且位于角色登记的某个允许目录之内。
func checkPathContainment(args map[string]any, role *RolePolicy) (string, string) {
	var rejected, detail string
	walkMaps(args, func(node map[string]any) {
		if rejected != "" {
			return
		}
		for key, value := range node {
			p, ok := value.(string)
			if !ok || !pathKeys[key] {
				continue
			}
			if !path.IsAbs(p) {
				rejected = ReasonPathNotAbsolute
				detail = fmt.Sprintf("%s=%q must be an absolute path", key, p)
				return
			}
			if !withinAllowedDirs(p, role.AllowedDirs) {
				rejected = ReasonPathOutsideAllowed
				detail = fmt.Sprintf("%s=%q is outside registered dirs of role %q", key, p, role.Name)
				return
			}
		}
	})
	return rejected, detail
}

func withinAllowedDirs(target string, allowedDirs []string) bool {
	cleanedTarget := cleanPath(target)
	for _, dir := range allowedDirs {
		if cleanedTarget == dir || strings.HasPrefix(cleanedTarget, dir+"/") {
			return true
		}
	}
	return false
}

// cleanPath 做词法清洗：解析 .. 与 .，不触碰文件系统、不解析符号链接。
func cleanPath(p string) string {
	cleaned := path.Clean(p)
	if cleaned == "/" {
		return cleaned
	}
	return strings.TrimSuffix(cleaned, "/")
}

func isProtectedPath(p string) bool {
	return protectedRoot(p) != ""
}

// protectedRoot 返回路径所在的受保护根（/dev 或 /proc），不在则返回空串。
func protectedRoot(p string) string {
	if !path.IsAbs(p) {
		return ""
	}
	cleaned := cleanPath(p)
	switch {
	case cleaned == "/dev", strings.HasPrefix(cleaned, "/dev/"):
		return "/dev"
	case cleaned == "/proc", strings.HasPrefix(cleaned, "/proc/"):
		return "/proc"
	default:
		return ""
	}
}

// walkMaps 深度优先遍历任意 JSON 值，对遇到的每个对象调用 visit。
func walkMaps(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkMaps(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkMaps(child, visit)
		}
	}
}

func deny(cmd qmpCommand, reason, detail string) Decision {
	return Decision{
		Allowed: false,
		Reason:  reason,
		Detail:  detail,
		Command: cmd.Execute,
		ID:      cmd.ID,
	}
}
